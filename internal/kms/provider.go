// Интерфейс провайдера шифрования — Transit или Kuznyechik.
package kms

import "context"

// EncryptionProvider — интерфейс провайдера шифрования (Transit или Kuznyechik)
type EncryptionProvider interface {
	Encrypt(ctx context.Context, keyName string, plaintext []byte) (string, error)
	Decrypt(ctx context.Context, keyName string, ciphertext string) ([]byte, error)
	GetKeyInfo(ctx context.Context, keyName string) (*TransitKeyInfo, error)
}
