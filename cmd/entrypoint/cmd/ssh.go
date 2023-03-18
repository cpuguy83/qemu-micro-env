package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/cpuguy83/pipes"
	"github.com/sirupsen/logrus"
	"golang.org/x/crypto/ssh"
	"golang.org/x/sys/unix"
)

func generateKeys() ([]byte, []byte, error) {
	priv, err := rsa.GenerateKey(rand.Reader, 4096)
	if err != nil {
		return nil, nil, fmt.Errorf("error generating private key: %w", err)
	}

	privateKeyPEM := &pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(priv)}
	pem := pem.EncodeToMemory(privateKeyPEM)

	pubK, err := ssh.NewPublicKey(priv.Public())
	if err != nil {
		return nil, nil, fmt.Errorf("error creating public key: %w", err)
	}
	pub := ssh.MarshalAuthorizedKey(pubK)

	return pub, pem, nil
}

func doSSH(ctx context.Context, sockDir string, port string, uid, gid int) error {
	logrus.Debug("Preparing SSH")
	fifoPath := filepath.Join(sockDir, "authorized_keys")

	if err := os.MkdirAll(sockDir, 0700); err != nil {
		return fmt.Errorf("error creating socket directory: %w", err)
	}

	ch, err := pipes.AsyncOpenFifo(fifoPath, os.O_WRONLY|os.O_CREATE, 0600)
	if err != nil {
		return fmt.Errorf("error opening fifo: %s: %w", fifoPath, err)
	}

	logrus.Debug("Generating keys")
	pub, priv, err := generateKeys()
	if err != nil {
		return err
	}

	logrus.Debug("Writing authorized keys")
	chAuth := make(chan error, 1)
	go func() {
		defer close(chAuth)
		chAuth <- func() error {
			logrus.Info("Waiting for fifo to be ready...")
			select {
			case <-ctx.Done():
				return ctx.Err()
			case result := <-ch:
				if result.Err != nil {
					return fmt.Errorf("error opening fifo: %w", result.Err)
				}
				defer result.W.Close()
				if _, err := result.W.Write(append(pub, '\n')); err != nil {
					return fmt.Errorf("error writing public key to authorized_keys: %w", err)
				}
				logrus.Debug("Public key written to authorized_keys fifo")
			}
			return nil
		}()
	}()

	select {
	case <-ctx.Done():
	case err := <-chAuth:
		if err != nil {
			return err
		}
	}

	agentSock := filepath.Join(sockDir, "agent.sock")
	unix.Unlink(agentSock)
	agentCmd := exec.CommandContext(ctx, "ssh-agent", "-a", agentSock)

	agentCmd.SysProcAttr = &syscall.SysProcAttr{
		Credential: &syscall.Credential{
			Uid: uint32(uid),
			Gid: uint32(gid),
		},
	}

	out, err := agentCmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("error starting ssh-agent: %w: %s", err, out)
	}
	cmd := exec.Command("/bin/sh", "-c", "eval \""+string(out)+"\" && ssh-add -")

	cmd.Stdin = bytes.NewReader(priv)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("error adding private key to ssh-agent: %s: %w", out, err)
	}

	sock := filepath.Join(sockDir, "docker.sock")
	unix.Unlink(sock)
	logrus.Debug(string(out))

	sockKV, _, found := strings.Cut(string(out), ";")
	if !found {
		return fmt.Errorf("error parsing ssh-agent output: %s", out)
	}

	for i := 0; ; i++ {
		cmd = exec.Command(
			"/usr/bin/ssh",
			"-f",
			"-nNT",
			"-o", "BatchMode=yes",
			"-o", "StrictHostKeyChecking=no",
			"-o", "ExitOnForwardFailure=yes",
			"-L", sock+":/run/docker.sock",
			"127.0.0.1", "-p", port,
		)
		cmd.Env = append(cmd.Env, sockKV)
		if out, err := cmd.CombinedOutput(); err != nil {
			if strings.Contains(string(out), "Connection refused") || strings.Contains(string(out), "Connection reset by peer") {
				if i == 100 {
					logrus.WithError(err).Warn(string(out))
					i = 0
				}
				time.Sleep(100 * time.Millisecond)
				continue
			}
			return fmt.Errorf("error setting up ssh tunnel: %w: %s", err, string(out))
		}
		break
	}

	if err := os.Chown(sock, uid, gid); err != nil {
		return fmt.Errorf("error setting ownership on proxied docker socket: %w", err)
	}

	return nil
}
