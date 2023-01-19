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

	if err := flSet.Parse(args); err != nil {
		return err
	}

	canUseHostCPU()

	cmd := exec.Command(flSet.Arg(0), flSet.Args()[1:]...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	fmt.Fprintln(os.Stderr, "executing command:", flSet.Arg(0), flSet.Args()[1:])

	return cmd.Run()
}
