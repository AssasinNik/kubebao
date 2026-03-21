// Kuznyechik-провайдер — AEAD-шифрование по ГОСТ Р 34.12-2015 + ГОСТ Р 34.13-2015.
// Ключи хранятся в OpenBao KV.
package kms

import (
	"context"
	"fmt"
	"time"

	"github.com/hashicorp/go-hclog"
	"github.com/kubebao/kubebao/internal/crypto"
)

// KuznyechikProvider provides encryption using Kuznyechik (GOST R 34.12-2015)
// with keys stored in OpenBao KV.
type KuznyechikProvider struct {
	keyManager *KeyManager
	logger     hclog.Logger
}

// NewKuznyechikProvider creates a new Kuznyechik encryption provider
func NewKuznyechikProvider(keyManager *KeyManager, logger hclog.Logger) *KuznyechikProvider {
	return &KuznyechikProvider{
		keyManager: keyManager,
		logger:     logger,
	}
}

// Encrypt шифрует plaintext алгоритмом Кузнечик-CTR + CMAC (ГОСТ Р 34.13-2015).
func (p *KuznyechikProvider) Encrypt(ctx context.Context, keyName string, plaintext []byte) (string, error) {
	start := time.Now()
	defer func() {
		p.logger.Debug("Kuznyechik шифрование завершено", "duration", time.Since(start))
	}()

	key, _, err := p.keyManager.GetOrCreateKey(ctx)
	if err != nil {
		return "", fmt.Errorf("get key: %w", err)
	}
	defer zeroBytes(key)

	aead, err := crypto.NewKuznyechikAEAD(key)
	if err != nil {
		return "", fmt.Errorf("create aead: %w", err)
	}

	ciphertext, err := aead.Encrypt(plaintext)
	if err != nil {
		return "", fmt.Errorf("encrypt: %w", err)
	}

	return string(ciphertext), nil
}

// Decrypt дешифрует и проверяет CMAC-тег (ГОСТ Р 34.13-2015).
func (p *KuznyechikProvider) Decrypt(ctx context.Context, keyName string, ciphertextStr string) ([]byte, error) {
	start := time.Now()
	defer func() {
		p.logger.Debug("Kuznyechik дешифрование завершено", "duration", time.Since(start))
	}()

	ciphertext := []byte(ciphertextStr)

	key, _, err := p.keyManager.GetOrCreateKey(ctx)
	if err != nil {
		return nil, fmt.Errorf("get key: %w", err)
	}
	defer zeroBytes(key)

	aead, err := crypto.NewKuznyechikAEAD(key)
	if err != nil {
		return nil, fmt.Errorf("create aead: %w", err)
	}

	plaintext, err := aead.Decrypt(ciphertext)
	if err != nil {
		p.logger.Error("Ошибка дешифрования Kuznyechik", "error", err)
		return nil, fmt.Errorf("decrypt: %w", err)
	}

	return plaintext, nil
}

// GetKeyInfo returns key information
func (p *KuznyechikProvider) GetKeyInfo(ctx context.Context, keyName string) (*TransitKeyInfo, error) {
	info, err := p.keyManager.GetKeyInfo(ctx)
	if err != nil {
		return nil, err
	}

	if !info.Exists {
		return nil, fmt.Errorf("key not found: %s", keyName)
	}

	return &TransitKeyInfo{
		Name:          keyName,
		LatestVersion: info.Version,
		Type:          "kuznyechik",
		Exportable:    false,
	}, nil
}

// zeroBytes overwrites a byte slice with zeros
func zeroBytes(b []byte) {
	for i := range b {
		b[i] = 0
	}
}
