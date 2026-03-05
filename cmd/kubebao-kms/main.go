/*
Copyright 2024 KubeBao Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/hashicorp/go-hclog"
	"github.com/kubebao/kubebao/internal/kms"
)

var (
	// Version is set at build time
	Version = "dev"
)

func main() {
	var (
		configFile string
		logLevel   string
		logFormat  string
		showVersion bool
	)

	flag.StringVar(&configFile, "config", "", "Path to configuration file")
	flag.StringVar(&logLevel, "log-level", "info", "Log level (debug, info, warn, error)")
	flag.StringVar(&logFormat, "log-format", "text", "Log format (text, json)")
	flag.BoolVar(&showVersion, "version", false, "Show version and exit")
	flag.Parse()

	if showVersion {
		fmt.Printf("kubebao-kms version %s\n", Version)
		os.Exit(0)
	}

	// Setup logger
	loggerOpts := &hclog.LoggerOptions{
		Name:  "kubebao-kms",
		Level: hclog.LevelFromString(logLevel),
	}

	if logFormat == "json" {
		loggerOpts.JSONFormat = true
	}

	logger := hclog.New(loggerOpts)

	logger.Info("starting kubebao-kms", "version", Version)

	// Load configuration
	var config *kms.Config
	var err error

	if configFile != "" {
		logger.Info("loading configuration from file", "path", configFile)
		config, err = kms.LoadConfig(configFile)
		if err != nil {
			logger.Error("failed to load configuration", "error", err)
			os.Exit(1)
		}
	} else {
		logger.Info("loading configuration from environment variables")
		config = kms.LoadConfigFromEnv()
	}

	// Validate configuration
	if err := config.Validate(); err != nil {
		logger.Error("invalid configuration", "error", err)
		os.Exit(1)
	}

	// Create KMS server
	server, err := kms.NewServer(config, logger)
	if err != nil {
		logger.Error("failed to create KMS server", "error", err)
		os.Exit(1)
	}

	// Setup context with signal handling
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Handle signals
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		sig := <-sigChan
		logger.Info("received signal, shutting down", "signal", sig)
		cancel()
	}()

	// Run the server
	if err := server.Run(ctx); err != nil {
		logger.Error("KMS server error", "error", err)
		os.Exit(1)
	}

	logger.Info("kubebao-kms stopped")
}
