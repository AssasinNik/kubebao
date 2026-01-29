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
	"os"
	"testing"
	"time"

	"github.com/hashicorp/go-hclog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDefaultConfig(t *testing.T) {
	cfg := DefaultConfig()

	assert.Equal(t, "http://127.0.0.1:8200", cfg.Address)
	assert.Equal(t, "transit", cfg.TransitMount)
	assert.Equal(t, "secret", cfg.KVMount)
	assert.Equal(t, 3, cfg.MaxRetries)
	assert.Equal(t, 30*time.Second, cfg.Timeout)
}

func TestConfigValidate(t *testing.T) {
	tests := []struct {
		name    string
		config  *Config
		wantErr bool
	}{
		{
			name: "valid config with token",
			config: &Config{
				Address: "http://127.0.0.1:8200",
				Token:   "test-token",
			},
			wantErr: false,
		},
		{
			name: "valid config with kubernetes auth",
			config: &Config{
				Address: "http://127.0.0.1:8200",
				KubernetesAuth: &KubernetesAuthConfig{
					Role: "test-role",
				},
			},
			wantErr: false,
		},
		{
			name: "missing address",
			config: &Config{
				Token: "test-token",
			},
			wantErr: true,
		},
		{
			name: "kubernetes auth without role",
			config: &Config{
				Address: "http://127.0.0.1:8200",
				KubernetesAuth: &KubernetesAuthConfig{
					MountPath: "kubernetes",
				},
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.config.Validate()
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestLoadConfigFromEnv(t *testing.T) {
	// Set environment variables
	os.Setenv("OPENBAO_ADDR", "https://openbao.example.com:8200")
	os.Setenv("OPENBAO_TOKEN", "test-token")
	os.Setenv("KUBEBAO_TRANSIT_MOUNT", "my-transit")
	os.Setenv("KUBEBAO_KV_MOUNT", "my-secret")
	defer func() {
		os.Unsetenv("OPENBAO_ADDR")
		os.Unsetenv("OPENBAO_TOKEN")
		os.Unsetenv("KUBEBAO_TRANSIT_MOUNT")
		os.Unsetenv("KUBEBAO_KV_MOUNT")
	}()

	cfg := LoadConfigFromEnv()

	assert.Equal(t, "https://openbao.example.com:8200", cfg.Address)
	assert.Equal(t, "test-token", cfg.Token)
	assert.Equal(t, "my-transit", cfg.TransitMount)
	assert.Equal(t, "my-secret", cfg.KVMount)
}

func TestLoadConfigFromEnvWithVaultVars(t *testing.T) {
	// Test backward compatibility with VAULT_* env vars
	os.Setenv("VAULT_ADDR", "https://vault.example.com:8200")
	os.Setenv("VAULT_TOKEN", "vault-token")
	defer func() {
		os.Unsetenv("VAULT_ADDR")
		os.Unsetenv("VAULT_TOKEN")
	}()

	cfg := LoadConfigFromEnv()

	assert.Equal(t, "https://vault.example.com:8200", cfg.Address)
	assert.Equal(t, "vault-token", cfg.Token)
}

func TestConfigMergeWithEnv(t *testing.T) {
	cfg := &Config{
		Address:      "http://localhost:8200",
		TransitMount: "transit",
	}

	os.Setenv("OPENBAO_ADDR", "https://openbao.example.com:8200")
	os.Setenv("KUBEBAO_TRANSIT_MOUNT", "custom-transit")
	defer func() {
		os.Unsetenv("OPENBAO_ADDR")
		os.Unsetenv("KUBEBAO_TRANSIT_MOUNT")
	}()

	cfg.MergeWithEnv()

	assert.Equal(t, "https://openbao.example.com:8200", cfg.Address)
	assert.Equal(t, "custom-transit", cfg.TransitMount)
}

func TestNewClientValidation(t *testing.T) {
	logger := hclog.NewNullLogger()

	// Test nil config
	_, err := NewClient(nil, logger)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "config cannot be nil")

	// Test missing address
	_, err = NewClient(&Config{Token: "test"}, logger)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "address is required")
}

func TestGetEnv(t *testing.T) {
	os.Setenv("TEST_VAR_1", "value1")
	defer os.Unsetenv("TEST_VAR_1")

	// First key has value
	assert.Equal(t, "value1", getEnv("TEST_VAR_1", "TEST_VAR_2"))

	// First key empty, second has value
	os.Setenv("TEST_VAR_2", "value2")
	defer os.Unsetenv("TEST_VAR_2")
	assert.Equal(t, "value1", getEnv("TEST_VAR_1", "TEST_VAR_2"))

	os.Unsetenv("TEST_VAR_1")
	assert.Equal(t, "value2", getEnv("TEST_VAR_1", "TEST_VAR_2"))

	// Both empty
	os.Unsetenv("TEST_VAR_2")
	assert.Equal(t, "", getEnv("TEST_VAR_1", "TEST_VAR_2"))
}

func TestGetEnvDefault(t *testing.T) {
	// Env var not set
	assert.Equal(t, "default", getEnvDefault("NONEXISTENT_VAR", "default"))

	// Env var set
	os.Setenv("EXISTENT_VAR", "actual")
	defer os.Unsetenv("EXISTENT_VAR")
	assert.Equal(t, "actual", getEnvDefault("EXISTENT_VAR", "default"))
}
