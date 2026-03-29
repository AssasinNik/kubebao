// Kuznyechik-провайдер — AEAD-шифрование по ГОСТ Р 34.12-2015 + ГОСТ Р 34.13-2015.
// Ключи хранятся в OpenBao KV.
package kms

import (
	"context"
	"fmt"

	"github.com/hashicorp/go-hclog"
	"github.com/kubebao/kubebao/internal/crypto"
)

// KuznyechikProvider — AEAD на базе ГОСТ Р 34.12-2015 (блок 128 бит) и режима из ГОСТ Р 34.13-2015;
// материал ключа берётся из OpenBao KV через KeyManager.
type KuznyechikProvider struct {
	keyManager *KeyManager
	logger     hclog.Logger
}

// NewKuznyechikProvider связывает менеджер ключей и логгер; keyManager не может быть nil (паника при использовании).
func NewKuznyechikProvider(keyManager *KeyManager, logger hclog.Logger) *KuznyechikProvider {
	return &KuznyechikProvider{
		keyManager: keyManager,
		logger:     logger,
	}
}

// Encrypt шифрует plaintext алгоритмом Кузнечик-CTR + CMAC (ГОСТ Р 34.13-2015).
func (p *KuznyechikProvider) Encrypt(ctx context.Context, keyName string, plaintext []byte) (string, error) {
	key, version, err := p.keyManager.GetOrCreateKey(ctx)
	if err != nil {
		return "", fmt.Errorf("get key: %w", err)
	}
	defer zeroBytes(key)

	p.logger.Info("Кузнечик: шифрование",
		"keyName", keyName,
		"keyVersion", version,
		"keySize", len(key)*8,
		"plaintextSize", len(plaintext),
		"algorithm", "Кузнечик-CTR + CMAC (ГОСТ Р 34.12/34.13-2015)",
	)

	aead, err := crypto.NewKuznyechikAEAD(key)
	if err != nil {
		return "", fmt.Errorf("create aead: %w", err)
	}

	ciphertext, err := aead.Encrypt(plaintext)
	if err != nil {
		return "", fmt.Errorf("encrypt: %w", err)
	}

	p.logger.Info("Кузнечик: шифрование завершено",
		"ciphertextSize", len(ciphertext),
		"overhead", len(ciphertext)-len(plaintext),
	)

	return string(ciphertext), nil
}

// Decrypt дешифрует и проверяет CMAC-тег (ГОСТ Р 34.13-2015).
func (p *KuznyechikProvider) Decrypt(ctx context.Context, keyName string, ciphertextStr string) ([]byte, error) {
	ciphertext := []byte(ciphertextStr)

	key, version, err := p.keyManager.GetOrCreateKey(ctx)
	if err != nil {
		return nil, fmt.Errorf("get key: %w", err)
	}
	defer zeroBytes(key)

	p.logger.Info("Кузнечик: дешифрование",
		"keyName", keyName,
		"keyVersion", version,
		"ciphertextSize", len(ciphertext),
	)

	aead, err := crypto.NewKuznyechikAEAD(key)
	if err != nil {
		return nil, fmt.Errorf("create aead: %w", err)
	}

	plaintext, err := aead.Decrypt(ciphertext)
	if err != nil {
		p.logger.Error("Кузнечик: CMAC верификация не пройдена — данные повреждены или ключ неверный", "error", err)
		return nil, fmt.Errorf("decrypt: %w", err)
	}

	p.logger.Info("Кузнечик: дешифрование завершено, CMAC верифицирован",
		"plaintextSize", len(plaintext),
	)

	return plaintext, nil
}

// GetKeyInfo отдаёт сведения о ключе в формате TransitKeyInfo для единого контракта EncryptionProvider.
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

// zeroBytes затирает срез нулями после использования ключа в стеке вызовов Encrypt/Decrypt.
func zeroBytes(b []byte) {
	for i := range b {
		b[i] = 0
	}
}
