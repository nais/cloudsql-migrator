package main

import (
	"context"
	"fmt"
	"github.com/nais/cloudsql-migrator/internal/pkg/config"
	"github.com/sethvargo/go-envconfig"
	"os"

	"log/slog"
)

func main() {
	var conf struct {
		config.CommonConfig
	}

	ctx := context.Background()

	if err := envconfig.Process(ctx, &conf); err != nil {
		fmt.Printf("Invalid configuration: %v", err)
		os.Exit(125)
	}

	// TODO: configure logger
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))

	logger.Info("Setup started", "config", conf)
}
