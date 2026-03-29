// Конфигурация плагина KMS: YAML-файл, переменные окружения, значения по умолчанию и Validate.
// LoadConfig применяет setDefaults+Validate; LoadConfigFromEnv — только заполнение из env без Validate.
package kms

import (
	"fmt"
	"os"
	"time"

	"github.com/kubebao/kubebao/internal/openbao"
	"gopkg.in/yaml.v3"
)

// Идентификаторы провайдеров шифрования в конфиге (строковые литералы в YAML).
const (
	ProviderTransit    = "transit"
	ProviderKuznyechik = "kuznyechik"
)

// Config — конфигурация KMS-плагина.
type Config struct {
	SocketPath string `yaml:"socketPath"` // Unix socket для gRPC (например /var/run/kubebao/kms.sock)

	KeyName string `yaml:"keyName"` // Имя ключа в Transit или путь в KV (для Kuznyechik)

	KeyType string `yaml:"keyType"` // aes256-gcm96, aes128-gcm96, chacha20-poly1305 (Transit), kuznyechik

	EncryptionProvider string `yaml:"encryptionProvider"` // "transit" или "kuznyechik"

	KVPathPrefix string `yaml:"kvPathPrefix"` // Префикс в KV для хранения ключей Kuznyechik (например kubebao/kms-keys)

	CreateKeyIfNotExists bool `yaml:"createKeyIfNotExists"` // Создавать ключ при первом Encrypt, если не существует

	HealthCheckInterval time.Duration `yaml:"healthCheckInterval"` // Интервал проверки доступности ключа

	OpenBao *openbao.Config `yaml:"openbao"` // Адрес, токен, TLS для OpenBao
}

// LoadConfig читает YAML по пути path, применяет значения по умолчанию и Validate.
func LoadConfig(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read config file: %w", err)
	}

	var config Config
	if err := yaml.Unmarshal(data, &config); err != nil {
		return nil, fmt.Errorf("failed to parse config file: %w", err)
	}

	config.setDefaults()

	if err := config.Validate(); err != nil {
		return nil, fmt.Errorf("invalid configuration: %w", err)
	}

	return &config, nil
}

// LoadConfigFromEnv собирает конфигурацию только из переменных окружения (префикс KUBEBAO_KMS_*).
// В отличие от LoadConfig, валидация не вызывается — вызывающий код должен вызвать Validate при необходимости.
func LoadConfigFromEnv() *Config {
	config := &Config{
		SocketPath:           getEnvDefault("KUBEBAO_KMS_SOCKET", "/var/run/kubebao/kms.sock"),
		KeyName:              getEnvDefault("KUBEBAO_KMS_KEY_NAME", "kubebao-kms"),
		KeyType:              getEnvDefault("KUBEBAO_KMS_KEY_TYPE", "kuznyechik"),
		EncryptionProvider:   getEnvDefault("KUBEBAO_KMS_PROVIDER", ProviderKuznyechik),
		KVPathPrefix:         getEnvDefault("KUBEBAO_KMS_KV_PREFIX", "kubebao/kms-keys"),
		CreateKeyIfNotExists: getEnvBool("KUBEBAO_KMS_CREATE_KEY", true),
		HealthCheckInterval:  getDurationEnv("KUBEBAO_KMS_HEALTH_INTERVAL", 30*time.Second),
		OpenBao:              openbao.LoadConfigFromEnv(),
	}

	return config
}

// setDefaults заполняет пустые поля разумными значениями для локальной разработки и Helm-чартов.
func (c *Config) setDefaults() {
	if c.SocketPath == "" {
		c.SocketPath = "/var/run/kubebao/kms.sock"
	}

	if c.KeyName == "" {
		c.KeyName = "kubebao-kms"
	}

	if c.EncryptionProvider == "" {
		c.EncryptionProvider = ProviderKuznyechik
	}

	if c.KeyType == "" {
		c.KeyType = "kuznyechik"
	}

	if c.KVPathPrefix == "" {
		c.KVPathPrefix = "kubebao/kms-keys"
	}

	if c.HealthCheckInterval == 0 {
		c.HealthCheckInterval = 30 * time.Second
	}

	if c.OpenBao == nil {
		c.OpenBao = openbao.LoadConfigFromEnv()
	}
}

// Validate проверяет обязательные поля, допустимость provider/keyType и вложенный openbao.Config.
func (c *Config) Validate() error {
	if c.SocketPath == "" {
		return fmt.Errorf("socketPath is required")
	}

	if c.KeyName == "" {
		return fmt.Errorf("keyName is required")
	}

	validProviders := map[string]bool{
		ProviderTransit:    true,
		ProviderKuznyechik: true,
	}

	if !validProviders[c.EncryptionProvider] {
		return fmt.Errorf("invalid encryptionProvider: %s, must be one of: transit, kuznyechik", c.EncryptionProvider)
	}

	validKeyTypes := map[string]bool{
		"aes128-gcm96":      true,
		"aes256-gcm96":      true,
		"chacha20-poly1305": true,
		"kuznyechik":        true,
	}

	if !validKeyTypes[c.KeyType] {
		return fmt.Errorf("invalid keyType: %s, must be one of: aes128-gcm96, aes256-gcm96, chacha20-poly1305, kuznyechik", c.KeyType)
	}

	if c.EncryptionProvider == ProviderKuznyechik && c.KeyType != "kuznyechik" {
		return fmt.Errorf("keyType must be kuznyechik when using kuznyechik provider")
	}

	if c.OpenBao == nil {
		return fmt.Errorf("openbao configuration is required")
	}

	if err := c.OpenBao.Validate(); err != nil {
		return fmt.Errorf("invalid openbao configuration: %w", err)
	}

	return nil
}

// getEnvDefault возвращает значение переменной key или defaultValue, если переменная пустая.
func getEnvDefault(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}

// getEnvBool парсит true/1/yes как истину; пустая строка — defaultValue.
func getEnvBool(key string, defaultValue bool) bool {
	value := os.Getenv(key)
	if value == "" {
		return defaultValue
	}
	return value == "true" || value == "1" || value == "yes"
}

// getDurationEnv разбирает длительность через time.ParseDuration; при ошибке — defaultValue.
func getDurationEnv(key string, defaultValue time.Duration) time.Duration {
	value := os.Getenv(key)
	if value == "" {
		return defaultValue
	}
	d, err := time.ParseDuration(value)
	if err != nil {
		return defaultValue
	}
	return d
}

// DefaultConfig возвращает полностью заполненный объект для тестов и встраивания без файла.
func DefaultConfig() *Config {
	return &Config{
		SocketPath:           "/var/run/kubebao/kms.sock",
		KeyName:              "kubebao-kms",
		KeyType:              "kuznyechik",
		EncryptionProvider:   ProviderKuznyechik,
		KVPathPrefix:         "kubebao/kms-keys",
		CreateKeyIfNotExists: true,
		HealthCheckInterval:  30 * time.Second,
		OpenBao:              openbao.DefaultConfig(),
	}
}
