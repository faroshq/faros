package main

import (
	"context"
	"os"

	"github.com/faroshq/faros/pkg/cli"
)

func main() {
	ctx := context.Background()
	// errors are being printed by CLI handlers
	err := run(ctx)
	if err != nil {
		os.Exit(1)
	}
}

func run(ctx context.Context) error {
	return cli.RunCLI(ctx)
}
