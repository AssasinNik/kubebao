// Клиент OpenBao — KV, Transit, аутентификация Kubernetes.
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

// Config — конфигурация клиента OpenBao.
type Config struct {
	Address string `yaml:"address"` // URL сервера OpenBao (например http://openbao:8200)

	Token string `yaml:"token"` // Root/статический токен (если не используется Kubernetes auth)

	TLSConfig *TLSConfig `yaml:"tls,omitempty"` // Сертификаты, CA, небезопасный режим

	KubernetesAuth *KubernetesAuthConfig `yaml:"kubernetesAuth,omitempty"` // Роль, mount path, путь к JWT

	TransitMount string `yaml:"transitMount"` // Путь к Transit engine (по умолчанию "transit")

	KVMount string `yaml:"kvMount"` // Путь к KV v2 (по умолчанию "secret")

	Namespace string `yaml:"namespace,omitempty"` // Namespace OpenBao (Enterprise)

	MaxRetries int `yaml:"maxRetries"` // Повторы при сетевых ошибках

	Timeout time.Duration `yaml:"timeout"` // Таймаут HTTP-запросов
}

// TLSConfig — параметры TLS для HTTPS.
type TLSConfig struct {
	CACert        string `yaml:"caCert"`
	CAPath        string `yaml:"caPath"`
	ClientCert    string `yaml:"clientCert"`
	ClientKey     string `yaml:"clientKey"`
	TLSServerName string `yaml:"tlsServerName"`
	Insecure      bool   `yaml:"insecure"`
}

// KubernetesAuthConfig — параметры Kubernetes auth (JWT из ServiceAccount).
type KubernetesAuthConfig struct {
	Role      string `yaml:"role"`      // Роль OpenBao для входа
	MountPath string `yaml:"mountPath"`  // Путь auth (по умолчанию "kubernetes")
	TokenPath string `yaml:"tokenPath"`  // Путь к файлу JWT (обычно /var/run/secrets/.../token)
}

// Client — обёртка над api.Client с автоматическим обновлением токена и методами KV/Transit.
type Client struct {
	client     *api.Client
	config     *Config
	logger     hclog.Logger
	mu         sync.RWMutex
	tokenExpiry time.Time
}

// NewClient — создаёт клиент, подключается к OpenBao и выполняет аутентификацию.
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

// authenticate — выбирает метод аутентификации: токен из конфига, Kubernetes auth, env (OPENBAO_TOKEN/VAULT_TOKEN).
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

// authenticateKubernetes — читает JWT из TokenPath, отправляет auth/kubernetes/login, сохраняет ClientToken.
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

	c.logger.Info("Успешная аутентификация Kubernetes auth",
		"role", k8sAuth.Role,
		"lease_duration", secret.Auth.LeaseDuration)

	return nil
}

// RefreshToken — если токен истекает в течение 5 минут, продлевает или повторно аутентифицируется.
func (c *Client) RefreshToken(ctx context.Context) error {
	c.mu.RLock()
	expiry := c.tokenExpiry
	c.mu.RUnlock()

	// If no expiry set or not close to expiring, skip refresh
	if expiry.IsZero() || time.Until(expiry) > 5*time.Minute {
		return nil
	}

	c.logger.Debug("Обновление токена аутентификации")

	// Try to renew the token first
	secret, err := c.client.Auth().Token().RenewSelfWithContext(ctx, 0)
	if err == nil && secret != nil && secret.Auth != nil {
		c.mu.Lock()
		c.tokenExpiry = time.Now().Add(time.Duration(secret.Auth.LeaseDuration) * time.Second)
		c.mu.Unlock()
		c.logger.Debug("Токен успешно обновлён")
		return nil
	}

	// If renewal fails, re-authenticate
	c.logger.Debug("Обновление токена не удалось, повторная аутентификация")
	return c.authenticate()
}

// TransitEncrypt — шифрует plaintext через transit/encrypt/{keyName}, возвращает ciphertext (база64).
func (c *Client) TransitEncrypt(ctx context.Context, keyName string, plaintext []byte) (string, error) {
	if err := c.RefreshToken(ctx); err != nil {
		c.logger.Warn("Не удалось обновить токен перед шифрованием", "error", err)
	}

	path := fmt.Sprintf("%s/encrypt/%s", c.config.TransitMount, keyName)
	c.logger.Debug("Transit encrypt", "path", path, "plaintextLen", len(plaintext))

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

// TransitDecrypt — дешифрует ciphertext через transit/decrypt/{keyName}.
func (c *Client) TransitDecrypt(ctx context.Context, keyName string, ciphertext string) ([]byte, error) {
	if err := c.RefreshToken(ctx); err != nil {
		c.logger.Warn("Не удалось обновить токен перед дешифрованием", "error", err)
	}

	path := fmt.Sprintf("%s/decrypt/%s", c.config.TransitMount, keyName)
	c.logger.Debug("Transit decrypt", "path", path)

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

// TransitGetKeyInfo — читает transit/keys/{keyName}, возвращает latest_version, type.
func (c *Client) TransitGetKeyInfo(ctx context.Context, keyName string) (*TransitKeyInfo, error) {
	if err := c.RefreshToken(ctx); err != nil {
		c.logger.Warn("Не удалось обновить токен при GetKeyInfo", "error", err)
	}

	path := fmt.Sprintf("%s/keys/%s", c.config.TransitMount, keyName)
	c.logger.Debug("Transit GetKeyInfo", "path", path)

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
		c.logger.Warn("Не удалось обновить токен при создании ключа", "error", err)
	}

	path := fmt.Sprintf("%s/keys/%s", c.config.TransitMount, keyName)
	c.logger.Debug("Transit CreateKey", "path", path, "type", keyType)
	data := map[string]interface{}{}
	if keyType != "" {
		data["type"] = keyType
	}

	_, err := c.client.Logical().WriteWithContext(ctx, path, data)
	if err != nil {
		return fmt.Errorf("failed to create transit key: %w", err)
	}

	c.logger.Info("Transit ключ создан", "name", keyName, "type", keyType)
	return nil
}

// KVRead — читает секрет из KV v2 по пути {kvMount}/data/{path}, возвращает data (без metadata).
func (c *Client) KVRead(ctx context.Context, path string) (map[string]interface{}, error) {
	if err := c.RefreshToken(ctx); err != nil {
		c.logger.Warn("Не удалось обновить токен при KVRead", "error", err)
	}

	fullPath := fmt.Sprintf("%s/data/%s", c.config.KVMount, path)
	c.logger.Debug("KVRead", "path", fullPath)
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

// KVWrite — записывает секрет в KV v2 по пути {kvMount}/data/{path}.
func (c *Client) KVWrite(ctx context.Context, path string, data map[string]interface{}) error {
	if err := c.RefreshToken(ctx); err != nil {
		c.logger.Warn("Не удалось обновить токен при KVWrite", "error", err)
	}

	fullPath := fmt.Sprintf("%s/data/%s", c.config.KVMount, path)
	c.logger.Debug("KVWrite", "path", fullPath)
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
