package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/exec"
)

func doExec(ctx context.Context, args []string) error {
	flSet := flag.NewFlagSet("exec", flag.ExitOnError)

	sockPathFl := flSet.String("docker-socket-path", "", "Path to docker socket")
	flSet.Parse(args)

	f, err := os.OpenFile(*sockPathFl, os.O_RDWR, 0)
	if err != nil {
		return fmt.Errorf("could not open docker socket file: %w", err)
	}
	defer f.Close()

	cmd := exec.Command(flSet.Arg(0), flSet.Args()[1:]...)
	cmd.ExtraFiles = []*os.File{f}
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	return cmd.Run()
}
