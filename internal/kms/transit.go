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
	"context"
	"fmt"
	"time"

	"github.com/hashicorp/go-hclog"
	"github.com/kubebao/kubebao/internal/openbao"
)

// TransitKeyInfo holds information about a transit key
type TransitKeyInfo struct {
	Name          string
	LatestVersion int
	Type          string
	Exportable    bool
}

// TransitClient wraps OpenBao client for transit operations
type TransitClient struct {
	client *openbao.Client
	logger hclog.Logger
}

// NewTransitClient creates a new transit client
func NewTransitClient(config *Config, logger hclog.Logger) (*TransitClient, error) {
	if config == nil || config.OpenBao == nil {
		return nil, fmt.Errorf("config and openbao config are required")
	}

	client, err := openbao.NewClient(config.OpenBao, logger)
	if err != nil {
		return nil, fmt.Errorf("failed to create openbao client: %w", err)
	}

	return &TransitClient{
		client: client,
		logger: logger,
	}, nil
}

// Encrypt encrypts the plaintext using the transit secrets engine
func (t *TransitClient) Encrypt(ctx context.Context, keyName string, plaintext []byte) (string, error) {
	start := time.Now()
	defer func() {
		t.logger.Debug("transit encrypt completed", "keyName", keyName, "duration", time.Since(start))
	}()

	ciphertext, err := t.client.TransitEncrypt(ctx, keyName, plaintext)
	if err != nil {
		return "", fmt.Errorf("transit encrypt failed: %w", err)
	}

	return ciphertext, nil
}

// Decrypt decrypts the ciphertext using the transit secrets engine
func (t *TransitClient) Decrypt(ctx context.Context, keyName string, ciphertext string) ([]byte, error) {
	start := time.Now()
	defer func() {
		t.logger.Debug("transit decrypt completed", "keyName", keyName, "duration", time.Since(start))
	}()

	plaintext, err := t.client.TransitDecrypt(ctx, keyName, ciphertext)
	if err != nil {
		return nil, fmt.Errorf("transit decrypt failed: %w", err)
	}

	return plaintext, nil
}

// GetKeyInfo retrieves information about a transit key
func (t *TransitClient) GetKeyInfo(ctx context.Context, keyName string) (*TransitKeyInfo, error) {
	info, err := t.client.TransitGetKeyInfo(ctx, keyName)
	if err != nil {
		return nil, err
	}

	return &TransitKeyInfo{
		Name:          info.Name,
		LatestVersion: info.LatestVersion,
		Type:          info.Type,
		Exportable:    info.Exportable,
	}, nil
}

// CreateKey creates a new transit encryption key
func (t *TransitClient) CreateKey(ctx context.Context, keyName string, keyType string) error {
	return t.client.TransitCreateKey(ctx, keyName, keyType)
}

// RotateKey rotates a transit key
func (t *TransitClient) RotateKey(ctx context.Context, keyName string) error {
	path := fmt.Sprintf("transit/keys/%s/rotate", keyName)
	_, err := t.client.WriteSecret(ctx, path, nil)
	if err != nil {
		return fmt.Errorf("failed to rotate key: %w", err)
	}

	t.logger.Info("transit key rotated", "keyName", keyName)
	return nil
}

// UpdateKeyConfig updates the configuration of a transit key
func (t *TransitClient) UpdateKeyConfig(ctx context.Context, keyName string, config map[string]interface{}) error {
	path := fmt.Sprintf("transit/keys/%s/config", keyName)
	_, err := t.client.WriteSecret(ctx, path, config)
	if err != nil {
		return fmt.Errorf("failed to update key config: %w", err)
	}

	return nil
}

// Health checks the health of the transit secrets engine
func (t *TransitClient) Health(ctx context.Context) error {
	_, err := t.client.Health(ctx)
	return err
}
