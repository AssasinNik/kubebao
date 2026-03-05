// Kubebao KMS — gRPC-сервер шифрования секретов Kubernetes (Transit или Kuznyechik).
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

	logger.Info("Запуск kubebao-kms", "version", Version)

	// Загрузка конфигурации
	var config *kms.Config
	var err error

	if configFile != "" {
		logger.Info("Загрузка конфигурации из файла", "path", configFile)
		config, err = kms.LoadConfig(configFile)
		if err != nil {
			logger.Error("Ошибка загрузки конфигурации", "error", err)
			os.Exit(1)
		}
		logger.Info("Конфигурация успешно загружена из файла")
	} else {
		logger.Info("Загрузка конфигурации из переменных окружения")
		config = kms.LoadConfigFromEnv()
	}

	// Проверка конфигурации
	if err := config.Validate(); err != nil {
		logger.Error("Неверная конфигурация", "error", err)
		os.Exit(1)
	}
	logger.Info("Конфигурация проверена успешно", "provider", config.EncryptionProvider, "keyName", config.KeyName)

	// Создание KMS сервера
	server, err := kms.NewServer(config, logger)
	if err != nil {
		logger.Error("Ошибка создания KMS сервера", "error", err)
		os.Exit(1)
	}
	logger.Info("KMS сервер создан успешно")

	// Обработка сигналов завершения
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Handle signals
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		sig := <-sigChan
		logger.Info("Получен сигнал завершения, остановка сервера", "signal", sig)
		cancel()
	}()

	// Запуск KMS сервера
	if err := server.Run(ctx); err != nil {
		logger.Error("Ошибка KMS сервера", "error", err)
		os.Exit(1)
	}

	logger.Info("kubebao-kms остановлен")
}
