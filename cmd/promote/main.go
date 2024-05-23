package main

import (
	"context"
	"fmt"
	"github.com/nais/cloudsql-migrator/internal/pkg/common_main"
	"github.com/nais/cloudsql-migrator/internal/pkg/config"
	"github.com/nais/cloudsql-migrator/internal/pkg/promote"
	"github.com/sethvargo/go-envconfig"
	"os"
)

func main() {
	ctx := context.Background()

	cfg := config.CommonConfig{}

	if err := envconfig.Process(ctx, &cfg); err != nil {
		fmt.Printf("invalid configuration: %v", err)
		os.Exit(1)
	}

	logger := config.SetupLogging(&cfg)
	mgr, err := common_main.Main(ctx, &cfg, logger)
	if err != nil {
		logger.Error("failed to complete configuration", "error", err)
		os.Exit(2)
	}

	mgr.Logger.Info("promote started", "config", cfg)

	err = promote.Promote(ctx, &cfg, mgr)
	if err != nil {
		mgr.Logger.Error("failed to promote", "error", err)
		os.Exit(1)
	}
}
