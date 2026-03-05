// Тесты шифрования Kuznyechik.
package crypto

import (
	"crypto/rand"
	"testing"
)

func TestKuznyechikAEAD_EncryptDecrypt(t *testing.T) {
	key := make([]byte, KuznyechikKeySize)
	if _, err := rand.Read(key); err != nil {
		t.Fatal(err)
	}

	aead, err := NewKuznyechikAEAD(key)
	if err != nil {
		t.Fatal(err)
	}

	plaintext := []byte("Hello, ГОСТ Кузнечик!")
	ciphertext, err := aead.Encrypt(plaintext)
	if err != nil {
		t.Fatalf("Encrypt failed: %v", err)
	}

	if len(ciphertext) != NonceSize+len(plaintext)+AuthTagSize {
		t.Errorf("ciphertext length = %d, want %d", len(ciphertext), NonceSize+len(plaintext)+AuthTagSize)
	}

	decrypted, err := aead.Decrypt(ciphertext)
	if err != nil {
		t.Fatalf("Decrypt failed: %v", err)
	}

	if string(decrypted) != string(plaintext) {
		t.Errorf("decrypted = %q, want %q", decrypted, plaintext)
	}
}

func TestKuznyechikAEAD_InvalidKeySize(t *testing.T) {
	_, err := NewKuznyechikAEAD([]byte("short"))
	if err != ErrInvalidKeySize {
		t.Errorf("expected ErrInvalidKeySize, got %v", err)
	}

	_, err = NewKuznyechikAEAD(make([]byte, 64))
	if err != ErrInvalidKeySize {
		t.Errorf("expected ErrInvalidKeySize, got %v", err)
	}
}

func TestKuznyechikAEAD_TamperedCiphertext(t *testing.T) {
	key := make([]byte, KuznyechikKeySize)
	rand.Read(key)

	aead, _ := NewKuznyechikAEAD(key)
	ciphertext, _ := aead.Encrypt([]byte("secret"))

	// Tamper with ciphertext
	ciphertext[NonceSize] ^= 0xff

	_, err := aead.Decrypt(ciphertext)
	if err != ErrAuthFailed {
		t.Errorf("expected ErrAuthFailed on tampered ciphertext, got %v", err)
	}
}

func TestKuznyechikAEAD_ShortCiphertext(t *testing.T) {
	key := make([]byte, KuznyechikKeySize)
	rand.Read(key)

	aead, _ := NewKuznyechikAEAD(key)

	_, err := aead.Decrypt([]byte("short"))
	if err != ErrInvalidCiphertext {
		t.Errorf("expected ErrInvalidCiphertext, got %v", err)
	}
}
