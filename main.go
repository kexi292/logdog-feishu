package main

import (
	"flag"
	"fmt"
	"github.com/zhjx922/alert/app"
	"github.com/zhjx922/alert/configure"
	"os"
)

func main() {
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
	configFile := flag.String("c", "", "Config file path")
	flag.CommandLine.Parse(args)

	if *configFile == "" {
		flag.Usage()
		os.Exit(1)
	}

	alert := app.NewAlert(*configFile)
	if err := alert.Run(); err != nil {
		os.Exit(1)
	}
}
