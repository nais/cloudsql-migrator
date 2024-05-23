package main

import (
	"context"
	"fmt"
	"github.com/nais/cloudsql-migrator/internal/pkg/common_main"
	"github.com/nais/cloudsql-migrator/internal/pkg/config"
	"github.com/sethvargo/go-envconfig"
	"os"
)

func main() {
	cfg := config.CommonConfig{}

	ctx := context.Background()

	if err := envconfig.Process(ctx, &cfg); err != nil {
		fmt.Printf("Invalid configuration: %v", err)
		os.Exit(1)
	}

	logger := config.SetupLogging(&cfg)
	mgr, err := common_main.Main(ctx, &cfg, logger)
	if err != nil {
		logger.Error("failed to complete configuration", "error", err)
		os.Exit(2)
	}

	mgr.Logger.Info("cleanup started", "config", cfg)
}
