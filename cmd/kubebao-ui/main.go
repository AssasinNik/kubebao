package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/hashicorp/go-hclog"
	"github.com/kubebao/kubebao/internal/ui"
)

func main() {
	logger := hclog.New(&hclog.LoggerOptions{
		Name:  "kubebao-ui",
		Level: hclog.LevelFromString(getEnv("LOG_LEVEL", "info")),
	})

	cfg := &ui.Config{
		ListenAddr:     getEnv("KUBEBAO_UI_LISTEN", ":8443"),
		OpenBaoAddr:    getEnv("OPENBAO_ADDR", "http://openbao.openbao.svc.cluster.local:8200"),
		OpenBaoToken:   os.Getenv("OPENBAO_TOKEN"),
		KMSKeyName:     getEnv("KUBEBAO_KMS_KEY_NAME", "kubebao-kms"),
		KVPathPrefix:   getEnv("KUBEBAO_KMS_KV_PREFIX", "kubebao/kms-keys"),
		KubeNamespace:  getEnv("KUBEBAO_NAMESPACE", "kubebao-system"),
	}

	srv, err := ui.NewServer(cfg, logger)
	if err != nil {
		logger.Error("Failed to create server", "error", err)
		os.Exit(1)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		<-sigCh
		logger.Info("Shutting down")
		cancel()
	}()

	logger.Info("Starting KubeBao UI", "listen", cfg.ListenAddr)
	if err := srv.Run(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "server error: %v\n", err)
		os.Exit(1)
	}
}

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
