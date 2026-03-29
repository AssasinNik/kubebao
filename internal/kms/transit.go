// Клиент OpenBao Transit: обёртка над HTTP API для шифрования/дешифрования без хранения ключа в плагине.
// Ключи живут в движке transit; тип ключа (aes256-gcm96 и т.д.) задаётся при создании.
package kms

import (
	"context"
	"fmt"
	"time"

	"github.com/hashicorp/go-hclog"
	"github.com/kubebao/kubebao/internal/openbao"
)

// TransitKeyInfo — снимок метаданных ключа в Transit (имя, последняя версия, тип, экспортируемость).
type TransitKeyInfo struct {
	Name          string
	LatestVersion int
	Type          string
	Exportable    bool
}

// TransitClient держит общий openbao.Client и использует его методы Transit* для KMS-операций.
type TransitClient struct {
	client *openbao.Client
	logger hclog.Logger
}

// NewTransitClient проверяет наличие config/OpenBao и поднимает HTTP-клиент к OpenBao.
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

// Encrypt вызывает TransitEncrypt: OpenBao шифрует данные ключом keyName и возвращает vault-токен ciphertext.
func (t *TransitClient) Encrypt(ctx context.Context, keyName string, plaintext []byte) (string, error) {
	start := time.Now()
	defer func() {
		t.logger.Debug("Transit шифрование завершено", "keyName", keyName, "duration", time.Since(start))
	}()

	ciphertext, err := t.client.TransitEncrypt(ctx, keyName, plaintext)
	if err != nil {
		return "", fmt.Errorf("transit encrypt failed: %w", err)
	}

	return ciphertext, nil
}

// Decrypt вызывает TransitDecrypt и возвращает исходный plaintext после проверки на стороне OpenBao.
func (t *TransitClient) Decrypt(ctx context.Context, keyName string, ciphertext string) ([]byte, error) {
	start := time.Now()
	defer func() {
		t.logger.Debug("Transit дешифрование завершено", "keyName", keyName, "duration", time.Since(start))
	}()

	plaintext, err := t.client.TransitDecrypt(ctx, keyName, ciphertext)
	if err != nil {
		return nil, fmt.Errorf("transit decrypt failed: %w", err)
	}

	return plaintext, nil
}

// GetKeyInfo читает transit/keys/:name и мапит ответ в локальную структуру для Server.initialize/health.
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

// CreateKey регистрирует новый ключ в движке transit с указанным keyType (см. Validate в config).
func (t *TransitClient) CreateKey(ctx context.Context, keyName string, keyType string) error {
	return t.client.TransitCreateKey(ctx, keyName, keyType)
}

// RotateKey выполняет POST rotate: новая версия ключа, старые ciphertext остаются читаемыми.
func (t *TransitClient) RotateKey(ctx context.Context, keyName string) error {
	path := fmt.Sprintf("transit/keys/%s/rotate", keyName)
	_, err := t.client.WriteSecret(ctx, path, nil)
	if err != nil {
		return fmt.Errorf("failed to rotate key: %w", err)
	}

	t.logger.Info("Transit ключ повёрнут", "keyName", keyName)
	return nil
}

// UpdateKeyConfig пишет произвольные параметры ключа (min_decryption_version, deletion_allowed и т.д.).
func (t *TransitClient) UpdateKeyConfig(ctx context.Context, keyName string, config map[string]interface{}) error {
	path := fmt.Sprintf("transit/keys/%s/config", keyName)
	_, err := t.client.WriteSecret(ctx, path, config)
	if err != nil {
		return fmt.Errorf("failed to update key config: %w", err)
	}

	return nil
}

// Health делегирует в общий health OpenBao (доступность API), не привязан строго к одному ключу.
func (t *TransitClient) Health(ctx context.Context) error {
	_, err := t.client.Health(ctx)
	return err
}
