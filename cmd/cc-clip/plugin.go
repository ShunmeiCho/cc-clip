package main

import (
	"log"
	"os"

	"github.com/shunmei/cc-clip/internal/plugin"
)

// cmdPlugin dispatches `cc-clip plugin run <name>`.
func cmdPlugin() {
	if len(os.Args) < 3 || os.Args[2] != "run" {
		log.Fatal("usage: cc-clip plugin run <name>")
	}
	if len(os.Args) < 4 {
		log.Fatal("usage: cc-clip plugin run <name>")
	}
	name := os.Args[3]
	port := getPort()
	if err := plugin.Run(name, port, os.Stdin, os.Stdout); err != nil {
		log.Fatalf("plugin run failed: %v", err)
	}
}
