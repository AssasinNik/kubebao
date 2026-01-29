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
	"context"
	"encoding/base64"
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/hashicorp/go-hclog"
	"github.com/openbao/openbao/api/v2"
)

// Config holds the configuration for the OpenBao client
type Config struct {
	// Address is the address of the OpenBao server
	Address string `yaml:"address"`

	// Token is the authentication token (optional, can use other auth methods)
	Token string `yaml:"token"`

	// TLSConfig holds TLS configuration
	TLSConfig *TLSConfig `yaml:"tls,omitempty"`

	// Kubernetes auth configuration
	KubernetesAuth *KubernetesAuthConfig `yaml:"kubernetesAuth,omitempty"`

	// TransitMount is the mount path for transit secrets engine
	TransitMount string `yaml:"transitMount"`

	// KVMount is the mount path for KV secrets engine
	KVMount string `yaml:"kvMount"`

	// Namespace is the OpenBao namespace (enterprise feature)
	Namespace string `yaml:"namespace,omitempty"`

	// MaxRetries is the maximum number of retries for API calls
	MaxRetries int `yaml:"maxRetries"`

	// Timeout for API calls
	Timeout time.Duration `yaml:"timeout"`
}

// TLSConfig holds TLS configuration
type TLSConfig struct {
	CACert        string `yaml:"caCert"`
	CAPath        string `yaml:"caPath"`
	ClientCert    string `yaml:"clientCert"`
	ClientKey     string `yaml:"clientKey"`
	TLSServerName string `yaml:"tlsServerName"`
	Insecure      bool   `yaml:"insecure"`
}

// KubernetesAuthConfig holds Kubernetes authentication configuration
type KubernetesAuthConfig struct {
	// Role is the OpenBao role to authenticate as
	Role string `yaml:"role"`

	// MountPath is the mount path for the Kubernetes auth method
	MountPath string `yaml:"mountPath"`

	// TokenPath is the path to the service account token
	TokenPath string `yaml:"tokenPath"`
}

// Client wraps the OpenBao API client with additional functionality
type Client struct {
	client     *api.Client
	config     *Config
	logger     hclog.Logger
	mu         sync.RWMutex
	tokenExpiry time.Time
}

// NewClient creates a new OpenBao client
func NewClient(cfg *Config, logger hclog.Logger) (*Client, error) {
	if cfg == nil {
		return nil, fmt.Errorf("config cannot be nil")
	}

	if cfg.Address == "" {
		return nil, fmt.Errorf("OpenBao address is required")
	}

	// Set defaults
	if cfg.TransitMount == "" {
		cfg.TransitMount = "transit"
	}
	if cfg.KVMount == "" {
		cfg.KVMount = "secret"
	}
	if cfg.MaxRetries == 0 {
		cfg.MaxRetries = 3
	}
	if cfg.Timeout == 0 {
		cfg.Timeout = 30 * time.Second
	}

	// Create API config
	apiConfig := api.DefaultConfig()
	apiConfig.Address = cfg.Address
	apiConfig.MaxRetries = cfg.MaxRetries
	apiConfig.Timeout = cfg.Timeout

	// Configure TLS if provided
	if cfg.TLSConfig != nil {
		tlsConfig := &api.TLSConfig{
			CACert:        cfg.TLSConfig.CACert,
			CAPath:        cfg.TLSConfig.CAPath,
			ClientCert:    cfg.TLSConfig.ClientCert,
			ClientKey:     cfg.TLSConfig.ClientKey,
			TLSServerName: cfg.TLSConfig.TLSServerName,
			Insecure:      cfg.TLSConfig.Insecure,
		}
		if err := apiConfig.ConfigureTLS(tlsConfig); err != nil {
			return nil, fmt.Errorf("failed to configure TLS: %w", err)
		}
	}

	// Create client
	client, err := api.NewClient(apiConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to create OpenBao client: %w", err)
	}

	// Set namespace if provided
	if cfg.Namespace != "" {
		client.SetNamespace(cfg.Namespace)
	}

	c := &Client{
		client: client,
		config: cfg,
		logger: logger,
	}

	// Authenticate
	if err := c.authenticate(); err != nil {
		return nil, fmt.Errorf("failed to authenticate: %w", err)
	}

	return c, nil
}

// authenticate performs authentication to OpenBao
func (c *Client) authenticate() error {
	// If token is provided directly, use it
	if c.config.Token != "" {
		c.client.SetToken(c.config.Token)
		return nil
	}

	// If Kubernetes auth is configured, use it
	if c.config.KubernetesAuth != nil {
		return c.authenticateKubernetes()
	}

	// Check for OPENBAO_TOKEN environment variable
	if token := os.Getenv("OPENBAO_TOKEN"); token != "" {
		c.client.SetToken(token)
		return nil
	}

	// Check for VAULT_TOKEN for backward compatibility
	if token := os.Getenv("VAULT_TOKEN"); token != "" {
		c.client.SetToken(token)
		return nil
	}

	return fmt.Errorf("no authentication method configured")
}

// authenticateKubernetes performs Kubernetes authentication
func (c *Client) authenticateKubernetes() error {
	k8sAuth := c.config.KubernetesAuth

	// Set defaults
	mountPath := k8sAuth.MountPath
	if mountPath == "" {
		mountPath = "kubernetes"
	}

	tokenPath := k8sAuth.TokenPath
	if tokenPath == "" {
		tokenPath = "/var/run/secrets/kubernetes.io/serviceaccount/token"
	}

	// Read the service account token
	jwt, err := os.ReadFile(tokenPath)
	if err != nil {
		return fmt.Errorf("failed to read service account token: %w", err)
	}

	// Login with Kubernetes auth
	loginPath := fmt.Sprintf("auth/%s/login", mountPath)
	loginData := map[string]interface{}{
		"role": k8sAuth.Role,
		"jwt":  string(jwt),
	}

	secret, err := c.client.Logical().Write(loginPath, loginData)
	if err != nil {
		return fmt.Errorf("failed to login with Kubernetes auth: %w", err)
	}

	if secret == nil || secret.Auth == nil {
		return fmt.Errorf("no auth info returned from Kubernetes login")
	}

	c.client.SetToken(secret.Auth.ClientToken)

	// Calculate token expiry
	if secret.Auth.LeaseDuration > 0 {
		c.mu.Lock()
		c.tokenExpiry = time.Now().Add(time.Duration(secret.Auth.LeaseDuration) * time.Second)
		c.mu.Unlock()
	}

	c.logger.Info("successfully authenticated with Kubernetes auth",
		"role", k8sAuth.Role,
		"lease_duration", secret.Auth.LeaseDuration)

	return nil
}

// RefreshToken refreshes the authentication token if needed
func (c *Client) RefreshToken(ctx context.Context) error {
	c.mu.RLock()
	expiry := c.tokenExpiry
	c.mu.RUnlock()

	// If no expiry set or not close to expiring, skip refresh
	if expiry.IsZero() || time.Until(expiry) > 5*time.Minute {
		return nil
	}

	c.logger.Debug("refreshing authentication token")

	// Try to renew the token first
	secret, err := c.client.Auth().Token().RenewSelfWithContext(ctx, 0)
	if err == nil && secret != nil && secret.Auth != nil {
		c.mu.Lock()
		c.tokenExpiry = time.Now().Add(time.Duration(secret.Auth.LeaseDuration) * time.Second)
		c.mu.Unlock()
		c.logger.Debug("token renewed successfully")
		return nil
	}

	// If renewal fails, re-authenticate
	c.logger.Debug("token renewal failed, re-authenticating")
	return c.authenticate()
}

// TransitEncrypt encrypts data using the Transit secrets engine
func (c *Client) TransitEncrypt(ctx context.Context, keyName string, plaintext []byte) (string, error) {
	if err := c.RefreshToken(ctx); err != nil {
		c.logger.Warn("failed to refresh token", "error", err)
	}

	path := fmt.Sprintf("%s/encrypt/%s", c.config.TransitMount, keyName)

	data := map[string]interface{}{
		"plaintext": base64.StdEncoding.EncodeToString(plaintext),
	}

	secret, err := c.client.Logical().WriteWithContext(ctx, path, data)
	if err != nil {
		return "", fmt.Errorf("failed to encrypt data: %w", err)
	}

	if secret == nil || secret.Data == nil {
		return "", fmt.Errorf("no data returned from encrypt operation")
	}

	ciphertext, ok := secret.Data["ciphertext"].(string)
	if !ok {
		return "", fmt.Errorf("ciphertext not found in response")
	}

	return ciphertext, nil
}

// TransitDecrypt decrypts data using the Transit secrets engine
func (c *Client) TransitDecrypt(ctx context.Context, keyName string, ciphertext string) ([]byte, error) {
	if err := c.RefreshToken(ctx); err != nil {
		c.logger.Warn("failed to refresh token", "error", err)
	}

	path := fmt.Sprintf("%s/decrypt/%s", c.config.TransitMount, keyName)

	data := map[string]interface{}{
		"ciphertext": ciphertext,
	}

	secret, err := c.client.Logical().WriteWithContext(ctx, path, data)
	if err != nil {
		return nil, fmt.Errorf("failed to decrypt data: %w", err)
	}

	if secret == nil || secret.Data == nil {
		return nil, fmt.Errorf("no data returned from decrypt operation")
	}

	plaintextB64, ok := secret.Data["plaintext"].(string)
	if !ok {
		return nil, fmt.Errorf("plaintext not found in response")
	}

	plaintext, err := base64.StdEncoding.DecodeString(plaintextB64)
	if err != nil {
		return nil, fmt.Errorf("failed to decode plaintext: %w", err)
	}

	return plaintext, nil
}

// TransitGetKeyInfo gets information about a transit key
func (c *Client) TransitGetKeyInfo(ctx context.Context, keyName string) (*TransitKeyInfo, error) {
	if err := c.RefreshToken(ctx); err != nil {
		c.logger.Warn("failed to refresh token", "error", err)
	}

	path := fmt.Sprintf("%s/keys/%s", c.config.TransitMount, keyName)

	secret, err := c.client.Logical().ReadWithContext(ctx, path)
	if err != nil {
		return nil, fmt.Errorf("failed to read key info: %w", err)
	}

	if secret == nil || secret.Data == nil {
		return nil, fmt.Errorf("key not found: %s", keyName)
	}

	info := &TransitKeyInfo{
		Name: keyName,
	}

	if latestVersion, ok := secret.Data["latest_version"].(float64); ok {
		info.LatestVersion = int(latestVersion)
	}

	if keyType, ok := secret.Data["type"].(string); ok {
		info.Type = keyType
	}

	if exportable, ok := secret.Data["exportable"].(bool); ok {
		info.Exportable = exportable
	}

	return info, nil
}

// TransitKeyInfo holds information about a transit key
type TransitKeyInfo struct {
	Name          string
	LatestVersion int
	Type          string
	Exportable    bool
}

// TransitCreateKey creates a new transit encryption key
func (c *Client) TransitCreateKey(ctx context.Context, keyName string, keyType string) error {
	if err := c.RefreshToken(ctx); err != nil {
		c.logger.Warn("failed to refresh token", "error", err)
	}

	path := fmt.Sprintf("%s/keys/%s", c.config.TransitMount, keyName)

	data := map[string]interface{}{}
	if keyType != "" {
		data["type"] = keyType
	}

	_, err := c.client.Logical().WriteWithContext(ctx, path, data)
	if err != nil {
		return fmt.Errorf("failed to create transit key: %w", err)
	}

	c.logger.Info("created transit key", "name", keyName, "type", keyType)
	return nil
}

// KVRead reads a secret from the KV secrets engine (v2)
func (c *Client) KVRead(ctx context.Context, path string) (map[string]interface{}, error) {
	if err := c.RefreshToken(ctx); err != nil {
		c.logger.Warn("failed to refresh token", "error", err)
	}

	fullPath := fmt.Sprintf("%s/data/%s", c.config.KVMount, path)

	secret, err := c.client.Logical().ReadWithContext(ctx, fullPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read secret: %w", err)
	}

	if secret == nil || secret.Data == nil {
		return nil, fmt.Errorf("secret not found: %s", path)
	}

	// KV v2 returns data nested under "data" key
	data, ok := secret.Data["data"].(map[string]interface{})
	if !ok {
		return nil, fmt.Errorf("invalid secret format")
	}

	return data, nil
}

// KVReadWithVersion reads a specific version of a secret from the KV secrets engine (v2)
func (c *Client) KVReadWithVersion(ctx context.Context, path string, version int) (map[string]interface{}, error) {
	if err := c.RefreshToken(ctx); err != nil {
		c.logger.Warn("failed to refresh token", "error", err)
	}

	fullPath := fmt.Sprintf("%s/data/%s?version=%d", c.config.KVMount, path, version)

	secret, err := c.client.Logical().ReadWithContext(ctx, fullPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read secret: %w", err)
	}

	if secret == nil || secret.Data == nil {
		return nil, fmt.Errorf("secret not found: %s", path)
	}

	data, ok := secret.Data["data"].(map[string]interface{})
	if !ok {
		return nil, fmt.Errorf("invalid secret format")
	}

	return data, nil
}

// KVWrite writes a secret to the KV secrets engine (v2)
func (c *Client) KVWrite(ctx context.Context, path string, data map[string]interface{}) error {
	if err := c.RefreshToken(ctx); err != nil {
		c.logger.Warn("failed to refresh token", "error", err)
	}

	fullPath := fmt.Sprintf("%s/data/%s", c.config.KVMount, path)

	writeData := map[string]interface{}{
		"data": data,
	}

	_, err := c.client.Logical().WriteWithContext(ctx, fullPath, writeData)
	if err != nil {
		return fmt.Errorf("failed to write secret: %w", err)
	}

	return nil
}

// ReadSecret reads a secret from any path (generic)
func (c *Client) ReadSecret(ctx context.Context, path string) (*api.Secret, error) {
	if err := c.RefreshToken(ctx); err != nil {
		c.logger.Warn("failed to refresh token", "error", err)
	}

	secret, err := c.client.Logical().ReadWithContext(ctx, path)
	if err != nil {
		return nil, fmt.Errorf("failed to read secret: %w", err)
	}

	return secret, nil
}

// WriteSecret writes data to any path (generic)
func (c *Client) WriteSecret(ctx context.Context, path string, data map[string]interface{}) (*api.Secret, error) {
	if err := c.RefreshToken(ctx); err != nil {
		c.logger.Warn("failed to refresh token", "error", err)
	}

	secret, err := c.client.Logical().WriteWithContext(ctx, path, data)
	if err != nil {
		return nil, fmt.Errorf("failed to write secret: %w", err)
	}

	return secret, nil
}

// Health checks the health of the OpenBao server
func (c *Client) Health(ctx context.Context) (*api.HealthResponse, error) {
	health, err := c.client.Sys().HealthWithContext(ctx)
	if err != nil {
		return nil, fmt.Errorf("health check failed: %w", err)
	}
	return health, nil
}

// GetClient returns the underlying OpenBao API client
func (c *Client) GetClient() *api.Client {
	return c.client
}
