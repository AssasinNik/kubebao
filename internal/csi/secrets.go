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
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/hashicorp/go-hclog"
)

// SecretsFetcher handles fetching secrets from OpenBao
type SecretsFetcher struct {
	config *Config
	logger hclog.Logger
	cache  *secretsCache
}

// secretsCache provides caching for fetched secrets
type secretsCache struct {
	mu      sync.RWMutex
	entries map[string]*cacheEntry
	ttl     time.Duration
}

type cacheEntry struct {
	secret    *FetchedSecret
	expiresAt time.Time
}

// NewSecretsFetcher creates a new secrets fetcher
func NewSecretsFetcher(config *Config, logger hclog.Logger) (*SecretsFetcher, error) {
	cache := &secretsCache{
		entries: make(map[string]*cacheEntry),
		ttl:     config.CacheTTL,
	}

	return &SecretsFetcher{
		config: config,
		logger: logger,
		cache:  cache,
	}, nil
}

// FetchSecrets fetches multiple secrets from OpenBao
func (f *SecretsFetcher) FetchSecrets(ctx context.Context, client *AuthenticatedClient, objects []SecretObject) ([]*FetchedSecret, error) {
	var secrets []*FetchedSecret
	var fetchErrors []error

	for _, obj := range objects {
		secret, err := f.fetchSecret(ctx, client, obj)
		if err != nil {
			fetchErrors = append(fetchErrors, fmt.Errorf("failed to fetch %s: %w", obj.ObjectName, err))
			continue
		}
		secrets = append(secrets, secret)
	}

	if len(fetchErrors) > 0 {
		// Return partial results with error
		errMsg := "some secrets failed to fetch:"
		for _, err := range fetchErrors {
			errMsg += "\n  - " + err.Error()
		}
		return secrets, errors.New(errMsg)
	}

	return secrets, nil
}

// fetchSecret fetches a single secret from OpenBao
func (f *SecretsFetcher) fetchSecret(ctx context.Context, client *AuthenticatedClient, obj SecretObject) (*FetchedSecret, error) {
	// Check cache first
	cacheKey := f.cacheKey(obj)
	if cached := f.cache.get(cacheKey); cached != nil {
		f.logger.Debug("cache hit", "objectName", obj.ObjectName)
		return cached, nil
	}

	f.logger.Debug("fetching secret", "objectName", obj.ObjectName, "path", obj.SecretPath)

	// Determine the secret engine type from path
	secret, version, err := f.readFromOpenBao(ctx, client, obj)
	if err != nil {
		return nil, err
	}

	// Parse file permission
	mode := parseFilePermission(obj.FilePermission)

	fetchedSecret := &FetchedSecret{
		ObjectName: obj.ObjectName,
		Content:    secret,
		Version:    version,
		Mode:       mode,
	}

	// Cache the result
	f.cache.set(cacheKey, fetchedSecret)

	return fetchedSecret, nil
}

// readFromOpenBao reads a secret from OpenBao
func (f *SecretsFetcher) readFromOpenBao(ctx context.Context, client *AuthenticatedClient, obj SecretObject) ([]byte, string, error) {
	path := obj.SecretPath

	// Handle KV v2 paths
	// If path doesn't start with a mount prefix, assume it's under "secret/" mount
	if !strings.HasPrefix(path, "secret/") && !strings.HasPrefix(path, "kv/") {
		// Add default mount and data path
		path = fmt.Sprintf("secret/data/%s", path)
	} else if !strings.Contains(path, "/data/") {
		// Path has mount prefix but missing /data/
		parts := strings.SplitN(path, "/", 2)
		if len(parts) == 2 {
			path = fmt.Sprintf("%s/data/%s", parts[0], parts[1])
		}
	}

	// Read the secret
	var secret interface{}
	var version string

	if len(obj.SecretArgs) > 0 {
		// Write request for dynamic secrets (database, pki, etc.)
		data := make(map[string]interface{})
		for k, v := range obj.SecretArgs {
			data[k] = v
		}

		resp, err := client.WriteSecret(ctx, path, data)
		if err != nil {
			return nil, "", fmt.Errorf("failed to write to path: %w", err)
		}

		if resp == nil || resp.Data == nil {
			return nil, "", fmt.Errorf("no data returned from path: %s", path)
		}

		secret = resp.Data
		version = resp.RequestID[:8] // Use request ID as version for dynamic secrets
	} else {
		// Read request for static secrets
		resp, err := client.ReadSecret(ctx, path)
		if err != nil {
			return nil, "", fmt.Errorf("failed to read path: %w", err)
		}

		if resp == nil || resp.Data == nil {
			return nil, "", fmt.Errorf("no data found at path: %s", path)
		}

		// For KV v2, data is nested under "data"
		if data, ok := resp.Data["data"]; ok {
			secret = data
			// Get version from metadata
			if metadata, ok := resp.Data["metadata"].(map[string]interface{}); ok {
				if v, ok := metadata["version"].(json.Number); ok {
					version = v.String()
				}
			}
		} else {
			secret = resp.Data
		}

		if version == "" {
			version = "1"
		}
	}

	// Extract specific key if requested
	content, err := f.extractContent(secret, obj)
	if err != nil {
		return nil, "", err
	}

	return content, version, nil
}

// extractContent extracts content from the secret data
func (f *SecretsFetcher) extractContent(data interface{}, obj SecretObject) ([]byte, error) {
	dataMap, ok := data.(map[string]interface{})
	if !ok {
		return nil, fmt.Errorf("unexpected data format")
	}

	var content []byte

	if obj.SecretKey != "" {
		// Extract specific key
		value, ok := dataMap[obj.SecretKey]
		if !ok {
			return nil, fmt.Errorf("key %s not found in secret", obj.SecretKey)
		}
		content = []byte(fmt.Sprintf("%v", value))
	} else {
		// Return entire secret as JSON
		jsonData, err := json.Marshal(dataMap)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal secret data: %w", err)
		}
		content = jsonData
	}

	// Apply encoding if specified
	switch obj.Encoding {
	case "base64":
		encoded := make([]byte, base64.StdEncoding.EncodedLen(len(content)))
		base64.StdEncoding.Encode(encoded, content)
		content = encoded
	case "text", "":
		// No encoding needed
	default:
		return nil, fmt.Errorf("unsupported encoding: %s", obj.Encoding)
	}

	return content, nil
}

// cacheKey generates a cache key for a secret object
func (f *SecretsFetcher) cacheKey(obj SecretObject) string {
	return fmt.Sprintf("%s:%s:%s", obj.SecretPath, obj.SecretKey, obj.ObjectName)
}

// get retrieves a secret from the cache
func (c *secretsCache) get(key string) *FetchedSecret {
	c.mu.RLock()
	defer c.mu.RUnlock()

	entry, ok := c.entries[key]
	if !ok {
		return nil
	}

	if time.Now().After(entry.expiresAt) {
		return nil
	}

	return entry.secret
}

// set stores a secret in the cache
func (c *secretsCache) set(key string, secret *FetchedSecret) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.entries[key] = &cacheEntry{
		secret:    secret,
		expiresAt: time.Now().Add(c.ttl),
	}
}

