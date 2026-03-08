package main

import (
	"fmt"
	"os"

	cli "github.com/urfave/cli/v2"
)

var version = "dev"

func main() {
	app := &cli.App{
		Name:    "sb",
		Usage:   "Docker sandbox tool for coding agents",
		Version: version,
		Action: func(ctx *cli.Context) error {
			_, err := fmt.Fprintln(ctx.App.Writer, ctx.App.Version)
			return err
		},
	}

	if err := app.Run(os.Args); err != nil {
		_, _ = fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
