package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/chowyu12/aiclaw/internal/bootstrap"
	"github.com/chowyu12/aiclaw/internal/daemon"
	"github.com/chowyu12/aiclaw/internal/selfupdate"
)

var version = "dev"

func main() {
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "version":
			fmt.Printf("aiclaw %s\n", version)
			return
		case "update":
			selfupdate.Run(version)
			return
		case "start":
			daemon.Start()
			return
		case "stop":
			daemon.Stop()
			return
		case "status":
			daemon.Status()
			return
		}
	}

	configFile := flag.String("config", "", "config file path (default: ~/.aiclaw/config.yaml)")
	flag.Parse()
	bootstrap.Run(bootstrap.Options{ConfigFlag: *configFile})
}
