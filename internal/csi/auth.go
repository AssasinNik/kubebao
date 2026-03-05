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
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/hashicorp/go-hclog"
	"github.com/openbao/openbao/api/v2"
)

// AuthConfig holds authentication configuration
type AuthConfig struct {
	// OpenBaoAddress is the address of the OpenBao server
	OpenBaoAddress string

	// AuthMethod is the authentication method
	AuthMethod string

	// AuthMountPath is the mount path for the auth method
	AuthMountPath string

	// Role is the role to authenticate as
	Role string

	// Namespace is the OpenBao namespace
	Namespace string

	// ServiceAccountToken is the Kubernetes service account token
	ServiceAccountToken string

	// Audience is the intended audience for JWT tokens
	Audience string

	// TLSConfig holds TLS configuration
	TLSConfig *TLSConfig
}

// TLSConfig holds TLS configuration
type TLSConfig struct {
	CACert        string
	CAPath        string
	ClientCert    string
	ClientKey     string
	TLSServerName string
	Insecure      bool
}

// AuthenticatedClient wraps an authenticated OpenBao client
type AuthenticatedClient struct {
	client      *api.Client
	config      *AuthConfig
	logger      hclog.Logger
	mu          sync.RWMutex
	tokenExpiry time.Time
}

// NewAuthenticatedClient creates a new authenticated OpenBao client
func NewAuthenticatedClient(ctx context.Context, config *AuthConfig, logger hclog.Logger) (*AuthenticatedClient, error) {
	if config == nil {
		return nil, fmt.Errorf("config cannot be nil")
	}

	if config.OpenBaoAddress == "" {
		return nil, fmt.Errorf("OpenBao address is required")
	}

	// Create API config
	apiConfig := api.DefaultConfig()
	apiConfig.Address = config.OpenBaoAddress

	// Configure TLS if provided
	if config.TLSConfig != nil {
		tlsConfig := &api.TLSConfig{
			CACert:        config.TLSConfig.CACert,
			CAPath:        config.TLSConfig.CAPath,
			ClientCert:    config.TLSConfig.ClientCert,
			ClientKey:     config.TLSConfig.ClientKey,
			TLSServerName: config.TLSConfig.TLSServerName,
			Insecure:      config.TLSConfig.Insecure,
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
	if config.Namespace != "" {
		client.SetNamespace(config.Namespace)
	}

	authClient := &AuthenticatedClient{
		client: client,
		config: config,
		logger: logger,
	}

	// Perform authentication
	if err := authClient.authenticate(ctx); err != nil {
		return nil, fmt.Errorf("authentication failed: %w", err)
	}

	return authClient, nil
}

// authenticate performs authentication to OpenBao
func (c *AuthenticatedClient) authenticate(ctx context.Context) error {
	switch c.config.AuthMethod {
	case "kubernetes":
		return c.authenticateKubernetes(ctx)
	case "jwt":
		return c.authenticateJWT(ctx)
	case "token":
		return c.authenticateToken()
	default:
		return fmt.Errorf("unsupported auth method: %s", c.config.AuthMethod)
	}
}

// authenticateKubernetes performs Kubernetes authentication
func (c *AuthenticatedClient) authenticateKubernetes(ctx context.Context) error {
	jwt := c.config.ServiceAccountToken

	// If no token provided, try to read from default location
	if jwt == "" {
		tokenPath := "/var/run/secrets/kubernetes.io/serviceaccount/token"
		tokenBytes, err := os.ReadFile(tokenPath)
		if err != nil {
			return fmt.Errorf("failed to read service account token: %w", err)
		}
		jwt = string(tokenBytes)
	}

	mountPath := c.config.AuthMountPath
	if mountPath == "" {
		mountPath = "kubernetes"
	}

	loginPath := fmt.Sprintf("auth/%s/login", mountPath)
	loginData := map[string]interface{}{
		"role": c.config.Role,
		"jwt":  jwt,
	}

	secret, err := c.client.Logical().WriteWithContext(ctx, loginPath, loginData)
	if err != nil {
		return fmt.Errorf("kubernetes auth login failed: %w", err)
	}

	if secret == nil || secret.Auth == nil {
		return fmt.Errorf("no auth info returned from kubernetes login")
	}

	c.client.SetToken(secret.Auth.ClientToken)

	// Set token expiry
	if secret.Auth.LeaseDuration > 0 {
		c.mu.Lock()
		c.tokenExpiry = time.Now().Add(time.Duration(secret.Auth.LeaseDuration) * time.Second)
		c.mu.Unlock()
	}

	c.logger.Debug("kubernetes authentication successful", "role", c.config.Role)
	return nil
}

// authenticateJWT performs JWT authentication
func (c *AuthenticatedClient) authenticateJWT(ctx context.Context) error {
	jwt := c.config.ServiceAccountToken
	if jwt == "" {
		return fmt.Errorf("JWT token is required for jwt auth")
	}

	mountPath := c.config.AuthMountPath
	if mountPath == "" {
		mountPath = "jwt"
	}

	loginPath := fmt.Sprintf("auth/%s/login", mountPath)
	loginData := map[string]interface{}{
		"role": c.config.Role,
		"jwt":  jwt,
	}

	secret, err := c.client.Logical().WriteWithContext(ctx, loginPath, loginData)
	if err != nil {
		return fmt.Errorf("jwt auth login failed: %w", err)
	}

	if secret == nil || secret.Auth == nil {
		return fmt.Errorf("no auth info returned from jwt login")
	}

	c.client.SetToken(secret.Auth.ClientToken)

	// Set token expiry
	if secret.Auth.LeaseDuration > 0 {
		c.mu.Lock()
		c.tokenExpiry = time.Now().Add(time.Duration(secret.Auth.LeaseDuration) * time.Second)
		c.mu.Unlock()
	}

	c.logger.Debug("jwt authentication successful", "role", c.config.Role)
	return nil
}

// authenticateToken uses a provided token directly
func (c *AuthenticatedClient) authenticateToken() error {
	// Check for token in environment
	token := os.Getenv("OPENBAO_TOKEN")
	if token == "" {
		token = os.Getenv("VAULT_TOKEN")
	}

	if token == "" {
		return fmt.Errorf("no token available for token auth")
	}

	c.client.SetToken(token)
	return nil
}

// RefreshToken refreshes the authentication token if needed
func (c *AuthenticatedClient) RefreshToken(ctx context.Context) error {
	c.mu.RLock()
	expiry := c.tokenExpiry
	c.mu.RUnlock()

	// If no expiry set or not close to expiring, skip refresh
	if expiry.IsZero() || time.Until(expiry) > 5*time.Minute {
		return nil
	}

	// Try to renew the token first
	secret, err := c.client.Auth().Token().RenewSelfWithContext(ctx, 0)
	if err == nil && secret != nil && secret.Auth != nil {
		c.mu.Lock()
		c.tokenExpiry = time.Now().Add(time.Duration(secret.Auth.LeaseDuration) * time.Second)
		c.mu.Unlock()
		return nil
	}

	// If renewal fails, re-authenticate
	return c.authenticate(ctx)
}

// ReadSecret reads a secret from OpenBao
func (c *AuthenticatedClient) ReadSecret(ctx context.Context, path string) (*api.Secret, error) {
	if err := c.RefreshToken(ctx); err != nil {
		c.logger.Warn("failed to refresh token", "error", err)
	}

	return c.client.Logical().ReadWithContext(ctx, path)
}

// WriteSecret writes data to OpenBao
func (c *AuthenticatedClient) WriteSecret(ctx context.Context, path string, data map[string]interface{}) (*api.Secret, error) {
	if err := c.RefreshToken(ctx); err != nil {
		c.logger.Warn("failed to refresh token", "error", err)
	}

	return c.client.Logical().WriteWithContext(ctx, path, data)
}

// GetClient returns the underlying API client
func (c *AuthenticatedClient) GetClient() *api.Client {
	return c.client
}

