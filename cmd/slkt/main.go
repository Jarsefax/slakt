package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"

	"github.com/Jarsefax/slakt/internal/cli"
	"github.com/Jarsefax/slakt/internal/render"
)

var version = "0.1.0-dev"

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()
	if err := cli.New(version).ExecuteContext(ctx); err != nil {
		fmt.Fprintln(os.Stderr, "slkt: "+render.Safe(err.Error()))
		if errors.Is(err, context.Canceled) || ctx.Err() != nil {
			os.Exit(130)
		}
		os.Exit(1)
	}
}
