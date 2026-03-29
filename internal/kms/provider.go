// Интерфейс провайдера шифрования — общий контракт для Transit и Kuznyechik.
package kms

import "context"

// EncryptionProvider — абстракция над внешним хранилищем ключей (OpenBao Transit или KV + ГОСТ).
//
// Encrypt/Decrypt работают с именем ключа keyName из конфигурации сервера.
// GetKeyInfo приводится к TransitKeyInfo для единообразия с движком transit (имя, версия, тип).
type EncryptionProvider interface {
	Encrypt(ctx context.Context, keyName string, plaintext []byte) (string, error)
	Decrypt(ctx context.Context, keyName string, ciphertext string) ([]byte, error)
	GetKeyInfo(ctx context.Context, keyName string) (*TransitKeyInfo, error)
}
