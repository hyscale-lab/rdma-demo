package main

import (
	"errors"
	"flag"
	"log"
	"os"

	"github.com/hyscale-lab/rdma-demo/internal/inmems3/app"
)

func main() {
	cfg := app.DefaultConfig()
	fs := app.NewFlagSet(os.Args[0], os.Stderr, &cfg)
	if err := fs.Parse(os.Args[1:]); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return
		}
		log.Fatal(err)
	}

	if err := app.Run(cfg); err != nil {
		log.Fatal(err)
	}
}
