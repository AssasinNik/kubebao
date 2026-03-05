/*
Copyright 2024 KubeBao Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific License governing permissions and
limitations under the License.
*/

// Package crypto provides GOST R 34.12-2015 (Kuznyechik) encryption.
// Uses Encrypt-then-MAC: Kuznyechik-CTR + HMAC-SHA256 for authenticated encryption.
package crypto

import (
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"errors"
	"fmt"
	"io"

	"github.com/starius/kuznyechik"
)

const (
	// KuznyechikKeySize is the key size for Kuznyechik (256 bits)
	KuznyechikKeySize = 32
	// BlockSize is Kuznyechik block size (128 bits)
	BlockSize = 16
	// NonceSize is the size of the nonce for CTR mode
	NonceSize = BlockSize
	// AuthTagSize is HMAC-SHA256 output size
	AuthTagSize = 32
	// HeaderSize is nonce + auth tag
	HeaderSize = NonceSize + AuthTagSize
)

var (
	ErrInvalidKeySize  = errors.New("kuznyechik: key must be 32 bytes")
	ErrInvalidCiphertext = errors.New("kuznyechik: ciphertext too short")
	ErrAuthFailed      = errors.New("kuznyechik: authentication failed")
)

// KuznyechikAEAD provides authenticated encryption using Kuznyechik in CTR mode
// with HMAC-SHA256 for authentication (Encrypt-then-MAC).
// Format: nonce(16) || ciphertext || hmac(nonce||ciphertext)
type KuznyechikAEAD struct {
	encKey []byte // 32 bytes for encryption
	macKey []byte // 32 bytes for HMAC (derived from same KEK)
}

// NewKuznyechikAEAD creates a new AEAD instance.
// Key must be 32 bytes (256 bits) for Kuznyechik.
// The key is split: first 16 bytes used for MAC key derivation, rest for encryption.
func NewKuznyechikAEAD(key []byte) (*KuznyechikAEAD, error) {
	if len(key) != KuznyechikKeySize {
		return nil, ErrInvalidKeySize
	}

	// Derive MAC key from encryption key using SHA256
	// This ensures we don't use the same key material for both purposes
	macKey := sha256.Sum256(append(key, []byte("mac")...))

	return &KuznyechikAEAD{
		encKey: key,
		macKey: macKey[:],
	}, nil
}

// ctrXORKeyStream implements CTR mode manually because kuznyechik.Encrypt
// requires exactly 16-byte dst/src, which standard cipher.CTR may not satisfy.
func ctrXORKeyStream(block cipher.Block, dst, src, iv []byte) {
	counter := make([]byte, BlockSize)
	copy(counter, iv)
	blockBuf := make([]byte, BlockSize)

	for len(src) > 0 {
		block.Encrypt(blockBuf, counter)
		n := BlockSize
		if len(src) < n {
			n = len(src)
		}
		for i := 0; i < n; i++ {
			dst[i] = src[i] ^ blockBuf[i]
		}
		dst = dst[n:]
		src = src[n:]

		// Increment counter (big-endian, 128-bit)
		for i := BlockSize - 1; i >= 0; i-- {
			counter[i]++
			if counter[i] != 0 {
				break
			}
		}
	}
}

// Encrypt encrypts plaintext and returns ciphertext with format:
// nonce(16) || ciphertext || hmac_tag(32)
func (k *KuznyechikAEAD) Encrypt(plaintext []byte) ([]byte, error) {
	block, err := kuznyechik.NewCipher(k.encKey)
	if err != nil {
		return nil, fmt.Errorf("create cipher: %w", err)
	}

	nonce := make([]byte, NonceSize)
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, fmt.Errorf("read random: %w", err)
	}

	// CTR mode (manual implementation for kuznyechik compatibility)
	ciphertext := make([]byte, len(plaintext))
	ctrXORKeyStream(block, ciphertext, plaintext, nonce)

	// Compute HMAC over nonce || ciphertext (Encrypt-then-MAC)
	mac := hmac.New(sha256.New, k.macKey)
	mac.Write(nonce)
	mac.Write(ciphertext)
	tag := mac.Sum(nil)

	// Output: nonce || ciphertext || tag
	result := make([]byte, 0, NonceSize+len(ciphertext)+AuthTagSize)
	result = append(result, nonce...)
	result = append(result, ciphertext...)
	result = append(result, tag...)

	return result, nil
}

// Decrypt decrypts ciphertext and verifies the authentication tag.
func (k *KuznyechikAEAD) Decrypt(ciphertext []byte) ([]byte, error) {
	if len(ciphertext) < HeaderSize {
		return nil, ErrInvalidCiphertext
	}

	nonce := ciphertext[:NonceSize]
	ct := ciphertext[NonceSize : len(ciphertext)-AuthTagSize]
	tag := ciphertext[len(ciphertext)-AuthTagSize:]

	// Verify HMAC
	mac := hmac.New(sha256.New, k.macKey)
	mac.Write(nonce)
	mac.Write(ct)
	expectedTag := mac.Sum(nil)

	if subtle.ConstantTimeCompare(tag, expectedTag) != 1 {
		return nil, ErrAuthFailed
	}

	// Decrypt (manual CTR)
	block, err := kuznyechik.NewCipher(k.encKey)
	if err != nil {
		return nil, fmt.Errorf("create cipher: %w", err)
	}

	plaintext := make([]byte, len(ct))
	ctrXORKeyStream(block, plaintext, ct, nonce)

	return plaintext, nil
}
