package crypto

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"testing"

	"github.com/kubebao/kubebao/internal/kuznyechik"
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

	expectedLen := Overhead + len(plaintext)
	if len(ciphertext) != expectedLen {
		t.Errorf("ciphertext length = %d, want %d", len(ciphertext), expectedLen)
	}

	if ciphertext[0] != currentVersion {
		t.Errorf("version byte = 0x%02x, want 0x%02x", ciphertext[0], currentVersion)
	}

	decrypted, err := aead.Decrypt(ciphertext)
	if err != nil {
		t.Fatalf("Decrypt failed: %v", err)
	}

	if string(decrypted) != string(plaintext) {
		t.Errorf("decrypted = %q, want %q", decrypted, plaintext)
	}
}

func TestKuznyechikAEAD_EmptyPlaintext(t *testing.T) {
	key := make([]byte, KuznyechikKeySize)
	rand.Read(key)

	aead, _ := NewKuznyechikAEAD(key)

	ciphertext, err := aead.Encrypt([]byte{})
	if err != nil {
		t.Fatalf("Encrypt empty: %v", err)
	}

	if len(ciphertext) != Overhead {
		t.Errorf("empty ciphertext len = %d, want %d", len(ciphertext), Overhead)
	}

	decrypted, err := aead.Decrypt(ciphertext)
	if err != nil {
		t.Fatalf("Decrypt empty: %v", err)
	}

	if len(decrypted) != 0 {
		t.Errorf("decrypted empty len = %d, want 0", len(decrypted))
	}
}

func TestKuznyechikAEAD_LargePlaintext(t *testing.T) {
	key := make([]byte, KuznyechikKeySize)
	rand.Read(key)

	aead, _ := NewKuznyechikAEAD(key)

	plaintext := make([]byte, 1<<16)
	rand.Read(plaintext)

	ciphertext, err := aead.Encrypt(plaintext)
	if err != nil {
		t.Fatalf("Encrypt large: %v", err)
	}

	decrypted, err := aead.Decrypt(ciphertext)
	if err != nil {
		t.Fatalf("Decrypt large: %v", err)
	}

	if !bytes.Equal(decrypted, plaintext) {
		t.Error("large plaintext roundtrip failed")
	}
}

func TestKuznyechikAEAD_InvalidKeySize(t *testing.T) {
	for _, size := range []int{0, 1, 16, 31, 33, 64} {
		_, err := NewKuznyechikAEAD(make([]byte, size))
		if err != ErrInvalidKeySize {
			t.Errorf("NewKuznyechikAEAD(%d bytes): expected ErrInvalidKeySize, got %v", size, err)
		}
	}
}

func TestKuznyechikAEAD_TamperedCiphertext(t *testing.T) {
	key := make([]byte, KuznyechikKeySize)
	rand.Read(key)

	aead, _ := NewKuznyechikAEAD(key)
	ciphertext, _ := aead.Encrypt([]byte("secret data for tamper test"))

	tampered := make([]byte, len(ciphertext))
	copy(tampered, ciphertext)
	tampered[versionSize+ivSize] ^= 0xff

	_, err := aead.Decrypt(tampered)
	if err != ErrAuthFailed {
		t.Errorf("expected ErrAuthFailed on tampered ciphertext, got %v", err)
	}
}

func TestKuznyechikAEAD_TamperedTag(t *testing.T) {
	key := make([]byte, KuznyechikKeySize)
	rand.Read(key)

	aead, _ := NewKuznyechikAEAD(key)
	ciphertext, _ := aead.Encrypt([]byte("secret"))

	tampered := make([]byte, len(ciphertext))
	copy(tampered, ciphertext)
	tampered[len(tampered)-1] ^= 0x01

	_, err := aead.Decrypt(tampered)
	if err != ErrAuthFailed {
		t.Errorf("expected ErrAuthFailed on tampered tag, got %v", err)
	}
}

func TestKuznyechikAEAD_TamperedIV(t *testing.T) {
	key := make([]byte, KuznyechikKeySize)
	rand.Read(key)

	aead, _ := NewKuznyechikAEAD(key)
	ciphertext, _ := aead.Encrypt([]byte("secret"))

	tampered := make([]byte, len(ciphertext))
	copy(tampered, ciphertext)
	tampered[versionSize] ^= 0xff

	_, err := aead.Decrypt(tampered)
	if err != ErrAuthFailed {
		t.Errorf("expected ErrAuthFailed on tampered IV, got %v", err)
	}
}

func TestKuznyechikAEAD_ShortCiphertext(t *testing.T) {
	key := make([]byte, KuznyechikKeySize)
	rand.Read(key)

	aead, _ := NewKuznyechikAEAD(key)

	for _, size := range []int{0, 1, Overhead - 1} {
		_, err := aead.Decrypt(make([]byte, size))
		if err != ErrInvalidCiphertext {
			t.Errorf("Decrypt(%d bytes): expected ErrInvalidCiphertext, got %v", size, err)
		}
	}
}

func TestKuznyechikAEAD_WrongVersion(t *testing.T) {
	key := make([]byte, KuznyechikKeySize)
	rand.Read(key)

	aead, _ := NewKuznyechikAEAD(key)
	ciphertext, _ := aead.Encrypt([]byte("data"))

	tampered := make([]byte, len(ciphertext))
	copy(tampered, ciphertext)
	tampered[0] = 0xFF

	_, err := aead.Decrypt(tampered)
	if err == nil {
		t.Error("expected error on wrong version, got nil")
	}
}

func TestKuznyechikAEAD_DifferentKeysCannotDecrypt(t *testing.T) {
	key1 := make([]byte, KuznyechikKeySize)
	key2 := make([]byte, KuznyechikKeySize)
	rand.Read(key1)
	rand.Read(key2)

	aead1, _ := NewKuznyechikAEAD(key1)
	aead2, _ := NewKuznyechikAEAD(key2)

	ciphertext, _ := aead1.Encrypt([]byte("secret"))

	_, err := aead2.Decrypt(ciphertext)
	if err != ErrAuthFailed {
		t.Errorf("expected ErrAuthFailed with wrong key, got %v", err)
	}
}

func TestKuznyechikAEAD_UniqueIVs(t *testing.T) {
	key := make([]byte, KuznyechikKeySize)
	rand.Read(key)

	aead, _ := NewKuznyechikAEAD(key)
	plaintext := []byte("same plaintext")

	ct1, _ := aead.Encrypt(plaintext)
	ct2, _ := aead.Encrypt(plaintext)

	if bytes.Equal(ct1, ct2) {
		t.Error("two encryptions of same plaintext produced identical ciphertext (IV reuse)")
	}
}

// --- Тесты ГОСТ-примитивов ---

func TestGOSTCTRIncrement(t *testing.T) {
	counter := make([]byte, blockSize)
	counter[blockSize-1] = 0xFE

	gostCTRIncrement(counter)
	if counter[blockSize-1] != 0xFF {
		t.Errorf("increment 0xFE: got 0x%02x", counter[blockSize-1])
	}

	gostCTRIncrement(counter)
	if counter[blockSize-1] != 0x00 || counter[blockSize-2] != 0x01 {
		t.Errorf("increment 0xFF overflow: got %x", counter[blockSize/2:])
	}

	// Upper half should remain unchanged
	for i := 0; i < blockSize/2; i++ {
		if counter[i] != 0 {
			t.Errorf("upper half byte %d changed to %d", i, counter[i])
		}
	}
}

func TestGOSTCTRIncrement_UpperHalfPreserved(t *testing.T) {
	counter := []byte{0xAA, 0xBB, 0xCC, 0xDD, 0xEE, 0xFF, 0x11, 0x22,
		0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF}

	gostCTRIncrement(counter)

	for i := 0; i < 8; i++ {
		expected := []byte{0xAA, 0xBB, 0xCC, 0xDD, 0xEE, 0xFF, 0x11, 0x22}
		if counter[i] != expected[i] {
			t.Errorf("upper half byte %d: got 0x%02x, want 0x%02x", i, counter[i], expected[i])
		}
	}

	for i := 8; i < 16; i++ {
		if counter[i] != 0x00 {
			t.Errorf("lower half after overflow byte %d: got 0x%02x, want 0x00", i, counter[i])
		}
	}
}

func TestCMACSubkeys(t *testing.T) {
	key, _ := hex.DecodeString("8899aabbccddeeff0011223344556677fedcba98765432100123456789abcdef")
	block, err := kuznyechik.NewCipher(key)
	if err != nil {
		t.Fatal(err)
	}

	k1, k2 := cmacGenerateSubkeys(block)

	if len(k1) != blockSize || len(k2) != blockSize {
		t.Fatalf("subkey lengths: k1=%d, k2=%d, want %d", len(k1), len(k2), blockSize)
	}

	if bytes.Equal(k1, k2) {
		t.Error("K1 and K2 should be different")
	}

	if bytes.Equal(k1, make([]byte, blockSize)) {
		t.Error("K1 is all zeros")
	}
}

func TestShiftLeftOne(t *testing.T) {
	tests := []struct {
		input    []byte
		expected []byte
	}{
		{[]byte{0x00, 0x01}, []byte{0x00, 0x02}},
		{[]byte{0x80, 0x00}, []byte{0x00, 0x00}},
		{[]byte{0x40, 0x00}, []byte{0x80, 0x00}},
		{[]byte{0x01, 0x80}, []byte{0x03, 0x00}},
	}

	for _, tt := range tests {
		dst := make([]byte, len(tt.input))
		shiftLeftOne(dst, tt.input)
		if !bytes.Equal(dst, tt.expected) {
			t.Errorf("shiftLeft(%x) = %x, want %x", tt.input, dst, tt.expected)
		}
	}
}

func BenchmarkAEAD_Encrypt_64B(b *testing.B) {
	benchmarkEncrypt(b, 64)
}

func BenchmarkAEAD_Encrypt_1KB(b *testing.B) {
	benchmarkEncrypt(b, 1024)
}

func BenchmarkAEAD_Encrypt_64KB(b *testing.B) {
	benchmarkEncrypt(b, 64*1024)
}

func BenchmarkAEAD_Decrypt_1KB(b *testing.B) {
	key := make([]byte, KuznyechikKeySize)
	rand.Read(key)
	aead, _ := NewKuznyechikAEAD(key)
	plaintext := make([]byte, 1024)
	rand.Read(plaintext)
	ciphertext, _ := aead.Encrypt(plaintext)

	b.SetBytes(int64(len(ciphertext)))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		aead.Decrypt(ciphertext)
	}
}

func benchmarkEncrypt(b *testing.B, size int) {
	key := make([]byte, KuznyechikKeySize)
	rand.Read(key)
	aead, _ := NewKuznyechikAEAD(key)
	plaintext := make([]byte, size)
	rand.Read(plaintext)

	b.SetBytes(int64(size))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		aead.Encrypt(plaintext)
	}
}
