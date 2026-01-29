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

package kms

import (
	"fmt"
	"os"
	"time"

	"github.com/kubebao/kubebao/internal/openbao"
	"gopkg.in/yaml.v3"
)

// Config holds the KMS plugin configuration
type Config struct {
	// SocketPath is the path to the Unix socket for the KMS plugin
	SocketPath string `yaml:"socketPath"`

	// KeyName is the name of the transit key to use for encryption
	KeyName string `yaml:"keyName"`

	// KeyType is the type of key to create if it doesn't exist
	// Supported types: aes128-gcm96, aes256-gcm96, chacha20-poly1305
	KeyType string `yaml:"keyType"`

	// CreateKeyIfNotExists creates the transit key if it doesn't exist
	CreateKeyIfNotExists bool `yaml:"createKeyIfNotExists"`

	// HealthCheckInterval is the interval for health checks
	HealthCheckInterval time.Duration `yaml:"healthCheckInterval"`

	// OpenBao configuration
	OpenBao *openbao.Config `yaml:"openbao"`
}

// LoadConfig loads the KMS configuration from a YAML file
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

// LoadConfigFromEnv loads configuration from environment variables
func LoadConfigFromEnv() *Config {
	config := &Config{
		SocketPath:           getEnvDefault("KUBEBAO_KMS_SOCKET", "/var/run/kubebao/kms.sock"),
		KeyName:              getEnvDefault("KUBEBAO_KMS_KEY_NAME", "kubebao-kms"),
		KeyType:              getEnvDefault("KUBEBAO_KMS_KEY_TYPE", "aes256-gcm96"),
		CreateKeyIfNotExists: getEnvBool("KUBEBAO_KMS_CREATE_KEY", true),
		HealthCheckInterval:  getDurationEnv("KUBEBAO_KMS_HEALTH_INTERVAL", 30*time.Second),
		OpenBao:              openbao.LoadConfigFromEnv(),
	}

	return config
}

// setDefaults sets default values for the configuration
func (c *Config) setDefaults() {
	if c.SocketPath == "" {
		c.SocketPath = "/var/run/kubebao/kms.sock"
	}

	if c.KeyName == "" {
		c.KeyName = "kubebao-kms"
	}

	if c.KeyType == "" {
		c.KeyType = "aes256-gcm96"
	}

	if c.HealthCheckInterval == 0 {
		c.HealthCheckInterval = 30 * time.Second
	}

	if c.OpenBao == nil {
		c.OpenBao = openbao.LoadConfigFromEnv()
	}
}

// Validate validates the KMS configuration
func (c *Config) Validate() error {
	if c.SocketPath == "" {
		return fmt.Errorf("socketPath is required")
	}

	if c.KeyName == "" {
		return fmt.Errorf("keyName is required")
	}

	validKeyTypes := map[string]bool{
		"aes128-gcm96":      true,
		"aes256-gcm96":      true,
		"chacha20-poly1305": true,
	}

	if !validKeyTypes[c.KeyType] {
		return fmt.Errorf("invalid keyType: %s, must be one of: aes128-gcm96, aes256-gcm96, chacha20-poly1305", c.KeyType)
	}

	if c.OpenBao == nil {
		return fmt.Errorf("openbao configuration is required")
	}

	if err := c.OpenBao.Validate(); err != nil {
		return fmt.Errorf("invalid openbao configuration: %w", err)
	}

	return nil
}

// getEnvDefault returns the value of an environment variable or a default value
func getEnvDefault(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}

// getEnvBool returns the boolean value of an environment variable or a default value
func getEnvBool(key string, defaultValue bool) bool {
	value := os.Getenv(key)
	if value == "" {
		return defaultValue
	}
	return value == "true" || value == "1" || value == "yes"
}

// getDurationEnv returns the duration value of an environment variable or a default value
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

// DefaultConfig returns a default KMS configuration
func DefaultConfig() *Config {
	return &Config{
		SocketPath:           "/var/run/kubebao/kms.sock",
		KeyName:              "kubebao-kms",
		KeyType:              "aes256-gcm96",
		CreateKeyIfNotExists: true,
		HealthCheckInterval:  30 * time.Second,
		OpenBao:              openbao.DefaultConfig(),
	}
}
