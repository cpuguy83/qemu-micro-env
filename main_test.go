package main

import (
	"context"
	"os"
	"testing"

	"github.com/cpuguy83/go-docker/buildkitopt"
	"github.com/cpuguy83/go-docker/transport"
	bkclient "github.com/moby/buildkit/client"
)

var client *bkclient.Client

func TestMain(m *testing.M) {
	tr, err := transport.DefaultTransport()
	if err != nil {
		panic(err)
	}
	client, err = bkclient.New(context.Background(), "", buildkitopt.FromDocker(tr)...)
	if err != nil {
		panic(err)
	}
	os.Exit(m.Run())
}
