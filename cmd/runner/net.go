package main

import (
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/cpuguy83/go-vsock"
	"github.com/sirupsen/logrus"
	"golang.org/x/sys/unix"
)

type closeWriter interface {
	CloseWrite() error
}

type closeReader interface {
	CloseRead() error
}

func closeWrite(c io.Closer) error {
	if cw, ok := c.(closeWriter); ok {
		return cw.CloseWrite()
	}
	return c.Close()
}

func closeRead(c io.Closer) error {
	if cr, ok := c.(closeReader); ok {
		return cr.CloseRead()
	}
	return c.Close()
}

func convertPortForwards(ls []int) []string {
	var result []string
	for _, l := range ls {
		result = append(result, strconv.Itoa(l))
	}
	return result
}

func forwardPort(localPort, remotePort int) error {
	l, err := net.Listen("tcp", fmt.Sprintf("0.0.0.0:"+strconv.Itoa(localPort)))
	if err != nil {
		return err
	}

	go func() {
		defer l.Close()

		for {
			conn, err := l.Accept()
			if err != nil {
				return
			}

			go func() {
				remote, err := net.Dial("tcp", "127.0.0.1:"+strconv.Itoa(remotePort))
				if err != nil {
					logrus.WithError(err).Error("dial failed")
					return
				}

				go func() {
					_, err := io.Copy(remote, conn)
					if err != nil {
						logrus.WithError(err).Error("copy failed")
					}
					closeWrite(remote)
					closeRead(conn)
				}()
				go func() {
					_, err := io.Copy(conn, remote)
					if err != nil {
						logrus.WithError(err).Error("copy failed")
					}
					closeWrite(conn)
					closeRead(remote)
				}()
			}()
		}
	}()

	return nil
}

func doVsock(cid uint32, uid, gid int) error {
	sock := "/tmp/sockets/docker.sock"
	l, err := net.Listen("unix", sock)
	if err != nil {
		if !errors.Is(err, unix.EADDRINUSE) {
			return err
		}
		if err := unix.Unlink(sock); err != nil {
			logrus.WithError(err).Error("unlink failed")
		}
		l, err = net.Listen("unix", "/tmp/sockets/docker.sock")
		if err != nil {
			return err
		}
	}

	if err := os.Chown(sock, uid, gid); err != nil {
		return fmt.Errorf("error setting ownership on proxied docker socket: %w", err)
	}

	go func() {
		defer l.Close()

		for {
			conn, err := l.Accept()
			if err != nil {
				logrus.WithError(err).Error("accept failed")
				return
			}
			go func() {
				defer conn.Close()
				var vsConn net.Conn

				for i := 0; ; i++ {
					vsConn, err = vsock.DialVsock(cid, 2375)
					if err != nil {
						if i == 10 {
							logrus.WithError(err).Error("vsock dial failing, retrying...")
							i = 0
						}
						time.Sleep(250 * time.Millisecond)
						continue
					}
					break
				}
				defer vsConn.Close()

				go io.Copy(vsConn, conn)
				io.Copy(conn, vsConn)
			}()
		}
	}()

	return nil
}

func getLocalPorts(forwards []int) ([]int, error) {
	out := make([]int, 0, len(forwards))
	data, err := os.ReadFile("/proc/sys/net/ipv4/ip_local_port_range")
	if err != nil {
		return nil, fmt.Errorf("error reading local port range: %w", err)
	}

	start, err := strconv.Atoi(strings.Fields(string(data))[0])
	if err != nil {
		return nil, fmt.Errorf("error parsing local port range: %w", err)
	}

	for i := range forwards {
		out = append(out, start+i)
	}
	return out, nil
}

func portForwardsToQemuFlag(local, forwards []int) string {
	var out []string
	for i, f := range forwards {
		out = append(out, fmt.Sprintf("hostfwd=tcp::%d-:%d", local[i], f))
	}
	return strings.Join(out, ",")
}
