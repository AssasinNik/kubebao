package kuznyechik

import (
	"bytes"
	"encoding/hex"
	"testing"
)

// ГОСТ Р 34.12-2015, Приложение А.1 — тестовые векторы шифра «Кузнечик».
func TestEncrypt_GOST_TestVector(t *testing.T) {
	key, _ := hex.DecodeString("8899aabbccddeeff0011223344556677fedcba98765432100123456789abcdef")
	plaintext, _ := hex.DecodeString("1122334455667700ffeeddccbbaa9988")
	expected, _ := hex.DecodeString("7f679d90bebc24305a468d42b9d4edcd")

	block, err := NewCipher(key)
	if err != nil {
		t.Fatal(err)
	}

	dst := make([]byte, BlockSize)
	block.Encrypt(dst, plaintext)

	if !bytes.Equal(dst, expected) {
		t.Errorf("Encrypt:\n  got  %s\n  want %s", hex.EncodeToString(dst), hex.EncodeToString(expected))
	}
}

func TestDecrypt_GOST_TestVector(t *testing.T) {
	key, _ := hex.DecodeString("8899aabbccddeeff0011223344556677fedcba98765432100123456789abcdef")
	ciphertext, _ := hex.DecodeString("7f679d90bebc24305a468d42b9d4edcd")
	expected, _ := hex.DecodeString("1122334455667700ffeeddccbbaa9988")

	block, err := NewCipher(key)
	if err != nil {
		t.Fatal(err)
	}

	dst := make([]byte, BlockSize)
	block.Decrypt(dst, ciphertext)

	if !bytes.Equal(dst, expected) {
		t.Errorf("Decrypt:\n  got  %s\n  want %s", hex.EncodeToString(dst), hex.EncodeToString(expected))
	}
}

func TestEncryptDecrypt_Roundtrip(t *testing.T) {
	key, _ := hex.DecodeString("8899aabbccddeeff0011223344556677fedcba98765432100123456789abcdef")

	block, err := NewCipher(key)
	if err != nil {
		t.Fatal(err)
	}

	original := []byte{0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x08,
		0x09, 0x0a, 0x0b, 0x0c, 0x0d, 0x0e, 0x0f, 0x10}

	encrypted := make([]byte, BlockSize)
	decrypted := make([]byte, BlockSize)

	block.Encrypt(encrypted, original)
	block.Decrypt(decrypted, encrypted)

	if !bytes.Equal(decrypted, original) {
		t.Errorf("roundtrip failed:\n  original  %x\n  decrypted %x", original, decrypted)
	}
}

func TestNewCipher_InvalidKeySize(t *testing.T) {
	tests := []int{0, 1, 15, 16, 31, 33, 64}
	for _, size := range tests {
		key := make([]byte, size)
		_, err := NewCipher(key)
		if err == nil {
			t.Errorf("NewCipher(%d bytes): expected error, got nil", size)
		}
	}
}

func TestBlockSize(t *testing.T) {
	key := make([]byte, KeySize)
	block, err := NewCipher(key)
	if err != nil {
		t.Fatal(err)
	}
	if block.BlockSize() != BlockSize {
		t.Errorf("BlockSize() = %d, want %d", block.BlockSize(), BlockSize)
	}
}

func BenchmarkEncrypt(b *testing.B) {
	key, _ := hex.DecodeString("8899aabbccddeeff0011223344556677fedcba98765432100123456789abcdef")
	block, _ := NewCipher(key)
	src := make([]byte, BlockSize)
	dst := make([]byte, BlockSize)

	b.SetBytes(BlockSize)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		block.Encrypt(dst, src)
	}
}

func BenchmarkDecrypt(b *testing.B) {
	key, _ := hex.DecodeString("8899aabbccddeeff0011223344556677fedcba98765432100123456789abcdef")
	block, _ := NewCipher(key)
	src := make([]byte, BlockSize)
	dst := make([]byte, BlockSize)

	b.SetBytes(BlockSize)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		block.Decrypt(dst, src)
	}
}
