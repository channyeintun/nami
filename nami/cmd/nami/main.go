package main

import (
	"context"
	"fmt"
	"os"

	"github.com/channyeintun/nami/internal/config"
	"github.com/channyeintun/nami/internal/tui"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		os.Exit(1)
	}
}

func run() error {
	cfg := config.Load()
	return tui.Run(context.Background(), cfg)
}
