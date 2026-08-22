package main

import (
	"fmt"
	"os"

	"github.com/chowyu12/aiclaw/internal/runtimeclient"
	"github.com/chowyu12/aiclaw/internal/selfupdate"
)

var version = "dev"

func main() {
	if len(os.Args) > 1 {
		if os.Args[1] == "runtime" {
			if err := runtimeclient.Run(os.Args[2:], version); err != nil {
				fmt.Fprintln(os.Stderr, err)
				os.Exit(1)
			}
			return
		}
		switch os.Args[1] {
		case "version":
			fmt.Printf("aiclaw %s\n", version)
			return
		case "update":
			selfupdate.Run(version)
			return
		case "start":
			fmt.Fprintln(os.Stderr, "AIClaw is a desktop app; run `make dev` from the repository instead")
			return
		case "stop":
			fmt.Fprintln(os.Stderr, "aiclaw is a local app; use /exit to stop it")
			return
		case "restart":
			fmt.Fprintln(os.Stderr, "aiclaw is a local app; restart it from your terminal")
			return
		case "status":
			fmt.Println("AIClaw runs as a Wails desktop application")
			return
		}
	}
	fmt.Fprintln(os.Stderr, "AIClaw is a desktop app; run `make dev` from the repository instead")
}
