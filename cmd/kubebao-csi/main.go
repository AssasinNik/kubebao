// Kubebao CSI — провайдер Secrets Store CSI Driver для инъекции секретов из OpenBao в поды.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/hashicorp/go-hclog"
	"github.com/kubebao/kubebao/internal/csi"
)

var (
	// Version is set at build time
	Version = "dev"
)

func main() {
	var (
		configFile  string
		logLevel    string
		logFormat   string
		showVersion bool
	)

	flag.StringVar(&configFile, "config", "", "Path to configuration file")
	flag.StringVar(&logLevel, "log-level", "info", "Log level (debug, info, warn, error)")
	flag.StringVar(&logFormat, "log-format", "text", "Log format (text, json)")
	flag.BoolVar(&showVersion, "version", false, "Show version and exit")
	flag.Parse()

	if showVersion {
		fmt.Printf("kubebao-csi version %s\n", Version)
		os.Exit(0)
	}

	// Setup logger
	loggerOpts := &hclog.LoggerOptions{
		Name:  "kubebao-csi",
		Level: hclog.LevelFromString(logLevel),
	}

	if logFormat == "json" {
		loggerOpts.JSONFormat = true
	}

	logger := hclog.New(loggerOpts)

	logger.Info("Запуск kubebao-csi", "version", Version)

	// Загрузка конфигурации
	var config *csi.Config
	var err error

	if configFile != "" {
		logger.Info("Загрузка конфигурации из файла", "path", configFile)
		config, err = csi.LoadConfig(configFile)
		if err != nil {
			logger.Error("Ошибка загрузки конфигурации", "error", err)
			os.Exit(1)
		}
		logger.Info("Конфигурация CSI загружена из файла")
	} else {
		logger.Info("Загрузка конфигурации из переменных окружения")
		config = csi.LoadConfigFromEnv()
	}

	// Проверка конфигурации
	if err := config.Validate(); err != nil {
		logger.Error("Неверная конфигурация", "error", err)
		os.Exit(1)
	}
	logger.Info("Конфигурация CSI проверена успешно", "socket", config.SocketPath)

	// Создание CSI провайдера
	provider, err := csi.NewProvider(config, logger)
	if err != nil {
		logger.Error("Ошибка создания CSI провайдера", "error", err)
		os.Exit(1)
	}
	logger.Info("CSI провайдер создан успешно")

	// Обработка сигналов завершения
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Handle signals
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		sig := <-sigChan
		logger.Info("Получен сигнал завершения, остановка провайдера", "signal", sig)
		cancel()
	}()

	// Запуск CSI провайдера
	if err := provider.Run(ctx); err != nil {
		logger.Error("Ошибка CSI провайдера", "error", err)
		os.Exit(1)
	}

	logger.Info("kubebao-csi остановлен")
}
