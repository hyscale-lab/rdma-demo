package main

import (
	"flag"
	"log"

	"rdma-demo/server-client-demo/internal/inmems3/app"
)

func main() {
	cfg := app.DefaultConfig()
	cfg.RegisterFlags(flag.CommandLine)
	flag.Parse()

	if err := app.Run(cfg); err != nil {
		log.Fatal(err)
	}
}
