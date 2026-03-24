package main

import (
	"flag"

	"github.com/chowyu12/aiclaw/internal/bootstrap"
)

var configFile = flag.String("config", "", "config file path (default: ~/.aiclaw/config.yaml)")

func main() {
	flag.Parse()
	bootstrap.Run(bootstrap.Options{ConfigFlag: *configFile})
}
