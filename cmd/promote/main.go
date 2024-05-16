package main

import (
	"context"
	"fmt"
	"github.com/nais/cloudsql-migrator/internal/pkg/config"
	"github.com/sethvargo/go-envconfig"
	"os"
)

func main() {
	var conf struct {
		config.CommonConfig
	}

	ctx := context.Background()

	if err := envconfig.Process(ctx, &conf); err != nil {
		fmt.Printf("invalid configuration: %v", err)
		os.Exit(125)
	}

	logger := config.SetupLogging(&conf.CommonConfig)
	logger.Info("promote started", "config", conf)
}
