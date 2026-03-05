/*
Copyright 2024 KubeBao Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific License governing permissions and
limitations under the License.
*/

package kms

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"sync"

	"github.com/hashicorp/go-hclog"
	"github.com/kubebao/kubebao/internal/crypto"
	"github.com/kubebao/kubebao/internal/openbao"
)

const (
	// DefaultKVPathPrefix is the default path prefix for KMS keys in OpenBao KV
	DefaultKVPathPrefix = "kubebao/kms-keys"
)

// KeyManager manages encryption keys stored in OpenBao KV.
type KeyManager struct {
	client     *openbao.Client
	kvPath     string
	keyName    string
	createIfNotExists bool
	logger     hclog.Logger
	mu         sync.RWMutex
	cachedKey  []byte
	keyVersion int
}

// KeyInfo holds information about a key in KV storage
type KeyInfo struct {
	Version int
	Exists  bool
}

// NewKeyManager creates a new key manager for Kuznyechik keys in OpenBao KV
func NewKeyManager(client *openbao.Client, kvPathPrefix, keyName string, createIfNotExists bool, logger hclog.Logger) (*KeyManager, error) {
	if client == nil {
		return nil, fmt.Errorf("openbao client cannot be nil")
	}

	if kvPathPrefix == "" {
		kvPathPrefix = DefaultKVPathPrefix
	}

	fullPath := fmt.Sprintf("%s/%s", kvPathPrefix, keyName)

	return &KeyManager{
		client:            client,
		kvPath:            fullPath,
		keyName:           keyName,
		createIfNotExists: createIfNotExists,
		logger:            logger,
	}, nil
}

// GetOrCreateKey retrieves the encryption key from OpenBao KV, creating it if necessary
func (km *KeyManager) GetOrCreateKey(ctx context.Context) ([]byte, int, error) {
	km.mu.RLock()
	if km.cachedKey != nil {
		key := make([]byte, len(km.cachedKey))
		copy(key, km.cachedKey)
		version := km.keyVersion
		km.mu.RUnlock()
		return key, version, nil
	}
	km.mu.RUnlock()

	km.mu.Lock()
	defer km.mu.Unlock()

	// Double-check after acquiring write lock
	if km.cachedKey != nil {
		key := make([]byte, len(km.cachedKey))
		copy(key, km.cachedKey)
		return key, km.keyVersion, nil
	}

	// Try to read existing key
	data, err := km.client.KVRead(ctx, km.kvPath)
	if err == nil {
		key, version, err := km.parseKeyData(data)
		if err != nil {
			return nil, 0, fmt.Errorf("parse key: %w", err)
		}
		km.cachedKey = key
		km.keyVersion = version
		keyCopy := make([]byte, len(key))
		copy(keyCopy, key)
		return keyCopy, version, nil
	}

	// Key not found - create if allowed
	if !km.createIfNotExists {
		return nil, 0, fmt.Errorf("key not found and createKeyIfNotExists is false")
	}

	km.logger.Info("creating new Kuznyechik key in OpenBao KV", "path", km.kvPath)

	// Generate 256-bit key
	key := make([]byte, crypto.KuznyechikKeySize)
	if _, err := rand.Read(key); err != nil {
		return nil, 0, fmt.Errorf("generate key: %w", err)
	}

	// Store in OpenBao KV
	writeData := map[string]interface{}{
		"key":     base64.StdEncoding.EncodeToString(key),
		"version": 1,
	}

	if err := km.client.KVWrite(ctx, km.kvPath, writeData); err != nil {
		return nil, 0, fmt.Errorf("write key to OpenBao: %w", err)
	}

	km.cachedKey = key
	km.keyVersion = 1

	keyCopy := make([]byte, len(key))
	copy(keyCopy, key)
	return keyCopy, 1, nil
}

// parseKeyData parses key data from OpenBao KV response
func (km *KeyManager) parseKeyData(data map[string]interface{}) ([]byte, int, error) {
	keyB64, ok := data["key"].(string)
	if !ok {
		return nil, 0, fmt.Errorf("key field not found or invalid")
	}

	key, err := base64.StdEncoding.DecodeString(keyB64)
	if err != nil {
		return nil, 0, fmt.Errorf("decode key: %w", err)
	}

	if len(key) != crypto.KuznyechikKeySize {
		return nil, 0, fmt.Errorf("invalid key size: want %d, got %d", crypto.KuznyechikKeySize, len(key))
	}

	version := 1
	if v, ok := data["version"].(float64); ok {
		version = int(v)
	}

	return key, version, nil
}

// GetKeyInfo returns key info without caching
func (km *KeyManager) GetKeyInfo(ctx context.Context) (*KeyInfo, error) {
	data, err := km.client.KVRead(ctx, km.kvPath)
	if err != nil {
		return &KeyInfo{Exists: false}, nil
	}

	_, version, err := km.parseKeyData(data)
	if err != nil {
		return nil, err
	}

	return &KeyInfo{
		Version: version,
		Exists:  true,
	}, nil
}

// InvalidateCache clears the cached key
func (km *KeyManager) InvalidateCache() {
	km.mu.Lock()
	defer km.mu.Unlock()

	if km.cachedKey != nil {
		// Zero out the key in memory
		for i := range km.cachedKey {
			km.cachedKey[i] = 0
		}
		km.cachedKey = nil
	}
}
