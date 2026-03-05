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

package openbao

import (
	"fmt"
	"os"
	"time"

	"gopkg.in/yaml.v3"
)

// LoadConfig loads the configuration from a YAML file
func LoadConfig(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read config file: %w", err)
	}

	var config Config
	if err := yaml.Unmarshal(data, &config); err != nil {
		return nil, fmt.Errorf("failed to parse config file: %w", err)
	}

	// Validate required fields
	if config.Address == "" {
		return nil, fmt.Errorf("address is required")
	}

	return &config, nil
}

// LoadConfigFromEnv loads configuration from environment variables
func LoadConfigFromEnv() *Config {
	config := &Config{
		Address:      getEnv("OPENBAO_ADDR", "VAULT_ADDR"),
		Token:        getEnv("OPENBAO_TOKEN", "VAULT_TOKEN"),
		TransitMount: getEnvDefault("KUBEBAO_TRANSIT_MOUNT", "transit"),
		KVMount:      getEnvDefault("KUBEBAO_KV_MOUNT", "secret"),
		Namespace:    getEnv("OPENBAO_NAMESPACE", "VAULT_NAMESPACE"),
		MaxRetries:   3,
		Timeout:      30 * time.Second,
	}

	// TLS configuration
	caCert := getEnv("OPENBAO_CACERT", "VAULT_CACERT")
	caPath := getEnv("OPENBAO_CAPATH", "VAULT_CAPATH")
	clientCert := getEnv("OPENBAO_CLIENT_CERT", "VAULT_CLIENT_CERT")
	clientKey := getEnv("OPENBAO_CLIENT_KEY", "VAULT_CLIENT_KEY")
	tlsServerName := getEnv("OPENBAO_TLS_SERVER_NAME", "VAULT_TLS_SERVER_NAME")
	skipVerify := getEnv("OPENBAO_SKIP_VERIFY", "VAULT_SKIP_VERIFY")

	if caCert != "" || caPath != "" || clientCert != "" || clientKey != "" {
		config.TLSConfig = &TLSConfig{
			CACert:        caCert,
			CAPath:        caPath,
			ClientCert:    clientCert,
			ClientKey:     clientKey,
			TLSServerName: tlsServerName,
			Insecure:      skipVerify == "true" || skipVerify == "1",
		}
	}

	// Kubernetes auth configuration
	k8sRole := os.Getenv("KUBEBAO_K8S_ROLE")
	if k8sRole != "" {
		config.KubernetesAuth = &KubernetesAuthConfig{
			Role:      k8sRole,
			MountPath: getEnvDefault("KUBEBAO_K8S_MOUNT_PATH", "kubernetes"),
			TokenPath: getEnvDefault("KUBEBAO_K8S_TOKEN_PATH", "/var/run/secrets/kubernetes.io/serviceaccount/token"),
		}
	}

	return config
}

// getEnv returns the value of the first non-empty environment variable
func getEnv(keys ...string) string {
	for _, key := range keys {
		if value := os.Getenv(key); value != "" {
			return value
		}
	}
	return ""
}

// getEnvDefault returns the value of an environment variable or a default value
func getEnvDefault(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}

// DefaultConfig returns a default configuration
func DefaultConfig() *Config {
	return &Config{
		Address:      "http://127.0.0.1:8200",
		TransitMount: "transit",
		KVMount:      "secret",
		MaxRetries:   3,
		Timeout:      30 * time.Second,
	}
}

// Validate validates the configuration
func (c *Config) Validate() error {
	if c.Address == "" {
		return fmt.Errorf("address is required")
	}

	// Check that at least one auth method is configured
	if c.Token == "" && c.KubernetesAuth == nil {
		// Check environment variables
		if os.Getenv("OPENBAO_TOKEN") == "" && os.Getenv("VAULT_TOKEN") == "" {
			return fmt.Errorf("no authentication method configured: set token or kubernetes auth")
		}
	}

	if c.KubernetesAuth != nil && c.KubernetesAuth.Role == "" {
		return fmt.Errorf("kubernetes auth role is required")
	}

	return nil
}

// MergeWithEnv merges the configuration with environment variables
// Environment variables take precedence
func (c *Config) MergeWithEnv() {
	if addr := getEnv("OPENBAO_ADDR", "VAULT_ADDR"); addr != "" {
		c.Address = addr
	}

	if token := getEnv("OPENBAO_TOKEN", "VAULT_TOKEN"); token != "" {
		c.Token = token
	}

	if namespace := getEnv("OPENBAO_NAMESPACE", "VAULT_NAMESPACE"); namespace != "" {
		c.Namespace = namespace
	}

	if transitMount := os.Getenv("KUBEBAO_TRANSIT_MOUNT"); transitMount != "" {
		c.TransitMount = transitMount
	}

	if kvMount := os.Getenv("KUBEBAO_KV_MOUNT"); kvMount != "" {
		c.KVMount = kvMount
	}
}
