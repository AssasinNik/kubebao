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

package csi

import (
	"fmt"
	"os"
	"time"

	"github.com/kubebao/kubebao/internal/openbao"
	"gopkg.in/yaml.v3"
)

// Config holds the CSI provider configuration
type Config struct {
	// SocketPath is the path to the Unix socket for the CSI provider
	SocketPath string `yaml:"socketPath"`

	// CacheTTL is the TTL for cached secrets
	CacheTTL time.Duration `yaml:"cacheTTL"`

	// EnableSecretRotation enables automatic secret rotation
	EnableSecretRotation bool `yaml:"enableSecretRotation"`

	// RotationPollInterval is the interval for checking secret rotation
	RotationPollInterval time.Duration `yaml:"rotationPollInterval"`

	// OpenBao configuration
	OpenBao *openbao.Config `yaml:"openbao"`

	// DefaultAuthMethod is the default authentication method
	DefaultAuthMethod string `yaml:"defaultAuthMethod"`

	// DefaultRole is the default role for Kubernetes authentication
	DefaultRole string `yaml:"defaultRole"`
}

// LoadConfig loads the CSI configuration from a YAML file
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
		SocketPath:           getEnvDefault("KUBEBAO_CSI_SOCKET", "/var/run/kubebao/csi.sock"),
		CacheTTL:             getDurationEnv("KUBEBAO_CSI_CACHE_TTL", 5*time.Minute),
		EnableSecretRotation: getEnvBool("KUBEBAO_CSI_ENABLE_ROTATION", true),
		RotationPollInterval: getDurationEnv("KUBEBAO_CSI_ROTATION_INTERVAL", 2*time.Minute),
		DefaultAuthMethod:    getEnvDefault("KUBEBAO_CSI_AUTH_METHOD", "kubernetes"),
		DefaultRole:          os.Getenv("KUBEBAO_CSI_DEFAULT_ROLE"),
		OpenBao:              openbao.LoadConfigFromEnv(),
	}

	return config
}

// setDefaults sets default values for the configuration
func (c *Config) setDefaults() {
	if c.SocketPath == "" {
		c.SocketPath = "/provider/kubebao.sock"
	}

	if c.CacheTTL == 0 {
		c.CacheTTL = 5 * time.Minute
	}

	if c.RotationPollInterval == 0 {
		c.RotationPollInterval = 2 * time.Minute
	}

	if c.DefaultAuthMethod == "" {
		c.DefaultAuthMethod = "kubernetes"
	}

	if c.OpenBao == nil {
		c.OpenBao = openbao.LoadConfigFromEnv()
	}
}

// Validate validates the CSI configuration
func (c *Config) Validate() error {
	if c.SocketPath == "" {
		return fmt.Errorf("socketPath is required")
	}

	if c.OpenBao == nil {
		return fmt.Errorf("openbao configuration is required")
	}

	return nil
}

// DefaultConfig returns a default CSI configuration
func DefaultConfig() *Config {
	return &Config{
		SocketPath:           "/provider/kubebao.sock",
		CacheTTL:             5 * time.Minute,
		EnableSecretRotation: true,
		RotationPollInterval: 2 * time.Minute,
		DefaultAuthMethod:    "kubernetes",
		OpenBao:              openbao.DefaultConfig(),
	}
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
