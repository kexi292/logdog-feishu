package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/kexi292/logdog-feishu/app"
	"github.com/kexi292/logdog-feishu/configure"
)

func main() {
	if len(os.Args) == 1 {
		if err := configure.Run("config.yaml"); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return
	}
	if len(os.Args) > 1 && os.Args[1] == "configure" {
		fs := flag.NewFlagSet("configure", flag.ExitOnError)
		configFile := fs.String("c", "config.yaml", "Config file path")
		_ = fs.Parse(os.Args[2:])
		if err := configure.Run(*configFile); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return
	}
	args := os.Args[1:]
	if len(args) > 0 && args[0] == "run" {
		args = args[1:]
	}
	configFile := flag.String("c", "config.yaml", "Config file path")
	flag.CommandLine.Parse(args)

	if *configFile == "" || flag.NArg() > 0 {
		flag.Usage()
		os.Exit(1)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := app.Run(ctx, *configFile); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
