package main

import (
	"context"
	"fmt"
	"os"

	"agent/internal/cli"
)

func main() {
	if err := cli.RunWithIO(context.Background(), os.Args[1:], cli.IO{Stdin: os.Stdin, Stdout: os.Stdout}); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
