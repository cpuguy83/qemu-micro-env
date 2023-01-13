package main

import (
	"bufio"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/sirupsen/logrus"
	"golang.org/x/sys/unix"
)

type guestCmd struct {
	Execute string          `json:"execute"`
	Args    json.RawMessage `json:"arguments"`
}

type guestResp struct {
	Return interface{} `json:"return,omitempty"`
	Error  *guestError `json:"error,omitempty"`
}

type guestError struct {
	Msg  string `json:"bufb64"`
	Code int    `json:"code"`
	Tag  string `json:"tag"`
}

type guestSSHAddAuthorizedKeys struct {
	Keys  []string `json:"keys"`
	Reset bool     `json:"reset"`
}

func guestAgent() {
	f, err := os.OpenFile("/dev/virtio-ports/org.qemu.guest_agent.0", unix.O_RDWR, 0)
	if err != nil {
		panic(err)
	}
	defer f.Close()

	rdr := bufio.NewReader(f)
	var cmd guestCmd

	l := logrus.WithField("component", "guest-agent")

	for {
		line, err := rdr.ReadBytes('\n')
		if err == io.EOF {
			time.Sleep(100 * time.Millisecond)
			continue
		}
		if err != nil {
			panic(err)
		}

		cmd.Execute = ""
		cmd.Args = nil

		if err := json.Unmarshal(line, &cmd); err != nil {
			l.WithError(err).Error("failed to unmarshal guest agent command")
			continue
		}

		switch cmd.Execute {
		case "guest-ssh-add-authorized-keys":
			var args guestSSHAddAuthorizedKeys
			if err := json.Unmarshal(cmd.Args, &args); err != nil {
				sendError(f, err, l)
				continue
			}

			if err := os.MkdirAll("/root/.ssh", 0700); err != nil {
				panic(err)
			}

			if args.Reset {
				data := []byte(strings.Join(args.Keys, "\n"))
				if err := os.WriteFile("/root/.ssh/authorized_keys", data, 0600); err != nil {
					sendError(f, err, l)
					continue
				}
				sendResponse(f, nil, l)
				continue
			}

			func() {
				authKeys, err := os.OpenFile("/root/.ssh/authorized_keys", os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
				if err != nil {
					sendError(f, fmt.Errorf("could not open authorized_keys file"), l)
					return
				}
				defer authKeys.Close()
				for _, key := range args.Keys {
					if _, err := authKeys.Write(append([]byte(key), '\x0a')); err != nil {
						sendError(f, err, l)
						continue
					}
				}
				sendResponse(f, nil, l)
			}()
		default:
			sendError(f, fmt.Errorf("unsupported"), l)
		}
	}
}

func sendResponse(f *os.File, i interface{}, l *logrus.Entry) {
	var resp guestResp
	resp.Return = i
	data, err := json.Marshal(resp)
	if err != nil {
		sendError(f, err, l)
		return
	}

	if _, err := f.Write(append(data, '\x0a')); err != nil {
		sendError(f, err, l)
		return
	}
}

func sendError(f *os.File, err error, l *logrus.Entry) {
	var resp guestResp
	resp.Error = &guestError{
		Msg:  base64.StdEncoding.EncodeToString([]byte(err.Error())),
		Code: -1, // TODO: find a better code
	}
	data, err := json.Marshal(resp)
	if err != nil {
		l.WithError(err).Error("failed to marshal guest agent response")
		return
	}

	if _, err := f.Write(append(data, '\x0a')); err != nil {
		l.WithError(err).Error("failed to write guest agent response")
		return
	}
}
