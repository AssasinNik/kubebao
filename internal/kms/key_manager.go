// Менеджер ключей Kuznyechik — хранение в OpenBao KV.
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
	// DefaultKVPathPrefix — префикс пути в KV OpenBao для ключей Kuznyechik, если в конфиге пусто.
	DefaultKVPathPrefix = "kubebao/kms-keys"
)

// KeyManager — доступ к сырому ключу Kuznyechik в OpenBao KV с кешированием в памяти процесса.
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

// KeyInfo — метаданные записи ключа в KV (версия и факт существования).
type KeyInfo struct {
	Version int
	Exists  bool
}

// NewKeyManager строит полный путь kvPathPrefix/keyName и сохраняет флаги создания ключа.
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

// GetOrCreateKey — читает ключ из OpenBao KV. Если ключа нет и createIfNotExists — генерирует 256 бит и сохраняет.
func (km *KeyManager) GetOrCreateKey(ctx context.Context) ([]byte, int, error) {
	km.mu.RLock()
	if km.cachedKey != nil {
		key := make([]byte, len(km.cachedKey))
		copy(key, km.cachedKey)
		version := km.keyVersion
		km.mu.RUnlock()
		km.logger.Debug("Ключ Кузнечик: из кеша", "version", version, "keySize", len(key)*8)
		return key, version, nil
	}
	km.mu.RUnlock()

	km.mu.Lock()
	defer km.mu.Unlock()

	// Повторная проверка под эксклюзивной блокировкой: другая горутина могла заполнить кеш.
	if km.cachedKey != nil {
		key := make([]byte, len(km.cachedKey))
		copy(key, km.cachedKey)
		return key, km.keyVersion, nil
	}

	// Сначала пытаемся прочитать существующую запись из KV без создания.
	data, err := km.client.KVRead(ctx, km.kvPath)
	if err == nil {
		key, version, err := km.parseKeyData(data)
		if err != nil {
			return nil, 0, fmt.Errorf("parse key: %w", err)
		}
		km.cachedKey = key
		km.keyVersion = version
		km.logger.Info("Ключ Кузнечик загружен из OpenBao KV",
			"path", km.kvPath,
			"version", version,
			"keySize", len(key)*8,
		)
		keyCopy := make([]byte, len(key))
		copy(keyCopy, key)
		return keyCopy, version, nil
	}

	// Записи нет: либо создаём новый ключ (crypto/rand), либо возвращаем ошибку политики.
	if !km.createIfNotExists {
		return nil, 0, fmt.Errorf("key not found and createKeyIfNotExists is false")
	}

	km.logger.Info("Генерация нового ключа Кузнечик (256 бит, crypto/rand)",
		"path", km.kvPath,
		"algorithm", "ГОСТ Р 34.12-2015",
		"keySize", crypto.KuznyechikKeySize*8,
	)

	key := make([]byte, crypto.KuznyechikKeySize)
	if _, err := rand.Read(key); err != nil {
		return nil, 0, fmt.Errorf("generate key: %w", err)
	}

	writeData := map[string]interface{}{
		"key":     base64.StdEncoding.EncodeToString(key),
		"version": 1,
	}

	if err := km.client.KVWrite(ctx, km.kvPath, writeData); err != nil {
		return nil, 0, fmt.Errorf("write key to OpenBao: %w", err)
	}

	km.logger.Info("Ключ Кузнечик создан и сохранён в OpenBao KV",
		"path", km.kvPath,
		"version", 1,
	)

	km.cachedKey = key
	km.keyVersion = 1

	keyCopy := make([]byte, len(key))
	copy(keyCopy, key)
	return keyCopy, 1, nil
}

// parseKeyData извлекает из map поля "key" (base64) и "version" (число), проверяет длину ключа Kuznyechik.
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

// GetKeyInfo читает KV напрямую (без обновления in-memory кеша) — для health и отображения версии.
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

// InvalidateCache обнуляет кешированный ключ в памяти (без удаления из OpenBao).
func (km *KeyManager) InvalidateCache() {
	km.mu.Lock()
	defer km.mu.Unlock()

	if km.cachedKey != nil {
		// Явное затирание байтов ключа перед освобождением слайса (снижение окна утечки в куче).
		for i := range km.cachedKey {
			km.cachedKey[i] = 0
		}
		km.cachedKey = nil
	}
}
