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

func doSSH(ctx context.Context, sockDir string, port string, uid, gid int, forwards []string) error {
	logrus.Debug("Preparing SSH")
	fifoPath := filepath.Join(sockDir, "authorized_keys")

	if err := mkdirAs(sockDir, 0700, uid, gid); err != nil {
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

	logrus.Debug(string(out))

	sockKV, _, found := strings.Cut(string(out), ";")
	if !found {
		return fmt.Errorf("error parsing ssh-agent output: %s", out)
	}

	for _, f := range forwards {

		go func(f string) {
			err := func() error {
				local := filepath.Join(sockDir, "s", f)
				if err := mkdirAs(filepath.Dir(local), 0750, uid, gid); err != nil {
					return fmt.Errorf("error creating socket directory: %w", err)
				}

				for i := 0; ; i++ {
					cmd = exec.Command(
						"/usr/bin/ssh",
						"-f",
						"-nNT",
						"-o", "BatchMode=yes",
						"-o", "StrictHostKeyChecking=no",
						"-o", "ExitOnForwardFailure=yes",
						"-L", local+":"+f,
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
						return fmt.Errorf("error starting ssh tunnel: %w: %s", err, out)
					}
					break
				}
				if err := os.Chown(local, uid, gid); err != nil {
					return fmt.Errorf("error chowning socket: %w", err)
				}
				return nil
			}()
			if err != nil {
				logrus.WithError(err).Error("error setting up ssh tunnel")
			}
		}(f)
	}

	return nil
}

// mkdirAs is a modified version of https://github.com/moby/moby/blob/9ff00e35f8833f9876e8919977be56a9aa956937/pkg/idtools/idtools_unix.go#L25
// Mostly it just uses uid/gids instead of an "Identity" struct and it always does MkdirAll and chowns all the directories.
func mkdirAs(path string, mode os.FileMode, uid, gid int) error {
	path, err := filepath.Abs(path)
	if err != nil {
		return err
	}

	stat, err := os.Stat(path)
	if err == nil {
		if !stat.IsDir() {
			return &os.PathError{Op: "mkdir", Path: path, Err: syscall.ENOTDIR}
		}

		// short-circuit -- we were called with an existing directory and chown was requested
		return setPermissions(path, mode, uid, gid, stat)
	}

	// make an array containing the original path asked for, plus (for mkAll == true)
	// all path components leading up to the complete path that don't exist before we MkdirAll
	// so that we can chown all of them properly at the end.  If chownExisting is false, we won't
	// chown the full directory path if it exists
	var paths []string
	if os.IsNotExist(err) {
		paths = []string{path}
	}

	// walk back to "/" looking for directories which do not exist
	// and add them to the paths array for chown after creation
	dirPath := path
	for {
		dirPath = filepath.Dir(dirPath)
		if dirPath == "/" {
			break
		}
		if _, err = os.Stat(dirPath); err != nil && os.IsNotExist(err) {
			paths = append(paths, dirPath)
		}
	}
	if err = os.MkdirAll(path, mode); err != nil {
		return err
	}

	// even if it existed, we will chown the requested path + any subpaths that
	// didn't exist when we called MkdirAll
	for _, pathComponent := range paths {
		if err = setPermissions(pathComponent, mode, uid, gid, nil); err != nil {
			return err
		}
	}
	return nil
}

func setPermissions(p string, mode os.FileMode, uid, gid int, stat os.FileInfo) error {
	if stat == nil {
		var err error
		stat, err = os.Stat(p)
		if err != nil {
			return err
		}
	}
	if stat.Mode().Perm() != mode.Perm() {
		if err := os.Chmod(p, mode.Perm()); err != nil {
			return err
		}
	}
	ssi := stat.Sys().(*syscall.Stat_t)
	if ssi.Uid == uint32(uid) && ssi.Gid == uint32(gid) {
		return nil
	}
	return os.Chown(p, uid, gid)
}
