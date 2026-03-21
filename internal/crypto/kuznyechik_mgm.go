// Package crypto реализует AEAD на базе ГОСТ Р 34.12-2015 (Кузнечик) и ГОСТ Р 34.13-2015 (CTR + CMAC).
//
// Схема: Encrypt-then-MAC.
//   - Шифрование: Кузнечик-CTR (ГОСТ Р 34.13-2015, раздел 5.5) с инкрементом нижних 64 бит счётчика.
//   - Аутентификация: CMAC на базе Кузнечика (ГОСТ Р 34.13-2015, раздел 5.6).
//   - Вывод ключей: из мастер-ключа (256 бит) через SHA-256 с доменным разделением выводятся
//     отдельные ключи шифрования и аутентификации.
//
// Формат выходных данных:
//
//	version(1) || iv(16) || ciphertext || cmac_tag(16)
//
// Где version = 0x01.
package crypto

import (
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"errors"
	"fmt"
	"io"

	"github.com/kubebao/kubebao/internal/kuznyechik"
)

const (
	KuznyechikKeySize = 32
	blockSize         = 16
	ivSize            = 16
	cmacTagSize       = 16
	versionSize       = 1
	Overhead          = versionSize + ivSize + cmacTagSize // 33 bytes

	currentVersion byte = 0x01
)

var (
	ErrInvalidKeySize     = errors.New("kuznyechik: ключ должен быть 32 байта (256 бит)")
	ErrInvalidCiphertext  = errors.New("kuznyechik: шифротекст слишком короткий или повреждён")
	ErrAuthFailed         = errors.New("kuznyechik: аутентификация не пройдена (CMAC mismatch)")
	ErrUnsupportedVersion = errors.New("kuznyechik: неподдерживаемая версия формата шифротекста")
)

// rb128 — полином приведения для GF(2^128): x^128 + x^7 + x^2 + x + 1.
// Используется при генерации подключей CMAC (ГОСТ Р 34.13-2015, раздел 5.6).
var rb128 = [blockSize]byte{0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0x87}

// KuznyechikAEAD — AEAD-схема на основе ГОСТ-алгоритмов.
// Шифрование: Кузнечик-CTR (ГОСТ Р 34.13-2015).
// Аутентификация: CMAC на базе Кузнечика (ГОСТ Р 34.13-2015).
type KuznyechikAEAD struct {
	encBlock cipher.Block
	macBlock cipher.Block
}

// NewKuznyechikAEAD создаёт AEAD с мастер-ключом 256 бит.
// Из мастер-ключа выводятся два независимых 256-битных ключа:
// один для шифрования (CTR), другой для аутентификации (CMAC).
func NewKuznyechikAEAD(masterKey []byte) (*KuznyechikAEAD, error) {
	if len(masterKey) != KuznyechikKeySize {
		return nil, ErrInvalidKeySize
	}

	encKey := deriveSubkey(masterKey, "kubebao-kuznyechik-enc")
	macKey := deriveSubkey(masterKey, "kubebao-kuznyechik-mac")
	defer zeroSlice(encKey)
	defer zeroSlice(macKey)

	encBlock, err := kuznyechik.NewCipher(encKey)
	if err != nil {
		return nil, fmt.Errorf("create encryption cipher: %w", err)
	}

	macBlock, err := kuznyechik.NewCipher(macKey)
	if err != nil {
		return nil, fmt.Errorf("create mac cipher: %w", err)
	}

	return &KuznyechikAEAD{
		encBlock: encBlock,
		macBlock: macBlock,
	}, nil
}

// Encrypt шифрует plaintext и возвращает:
//
//	version(1) || iv(16) || ciphertext || cmac_tag(16)
func (k *KuznyechikAEAD) Encrypt(plaintext []byte) ([]byte, error) {
	iv := make([]byte, ivSize)
	if _, err := io.ReadFull(rand.Reader, iv); err != nil {
		return nil, fmt.Errorf("generate IV: %w", err)
	}

	ct := make([]byte, len(plaintext))
	gostCTR(k.encBlock, ct, plaintext, iv)

	tag := gostCMAC(k.macBlock, iv, ct)

	out := make([]byte, 0, Overhead+len(ct))
	out = append(out, currentVersion)
	out = append(out, iv...)
	out = append(out, ct...)
	out = append(out, tag...)

	return out, nil
}

// Decrypt проверяет CMAC и дешифрует данные.
func (k *KuznyechikAEAD) Decrypt(data []byte) ([]byte, error) {
	if len(data) < Overhead {
		return nil, ErrInvalidCiphertext
	}

	version := data[0]
	if version != currentVersion {
		return nil, fmt.Errorf("%w: got 0x%02x, want 0x%02x", ErrUnsupportedVersion, version, currentVersion)
	}

	iv := data[versionSize : versionSize+ivSize]
	ct := data[versionSize+ivSize : len(data)-cmacTagSize]
	tag := data[len(data)-cmacTagSize:]

	expectedTag := gostCMAC(k.macBlock, iv, ct)
	if subtle.ConstantTimeCompare(tag, expectedTag) != 1 {
		return nil, ErrAuthFailed
	}

	plaintext := make([]byte, len(ct))
	gostCTR(k.encBlock, plaintext, ct, iv)

	return plaintext, nil
}

// --- ГОСТ Р 34.13-2015, раздел 5.5: Режим гаммирования (CTR) ---

// gostCTR реализует режим CTR по ГОСТ Р 34.13-2015.
// Счётчик — 128-битный блок. Инкрементируются только нижние 64 бит (s = n/2 = 64).
// Верхние 64 бит фиксированы (из IV).
func gostCTR(block cipher.Block, dst, src, iv []byte) {
	var counter [blockSize]byte
	copy(counter[:], iv)
	var gamma [blockSize]byte

	for len(src) > 0 {
		block.Encrypt(gamma[:], counter[:])

		n := blockSize
		if len(src) < n {
			n = len(src)
		}
		xorBytes(dst[:n], src[:n], gamma[:n])

		dst = dst[n:]
		src = src[n:]

		gostCTRIncrement(counter[:])
	}
}

// gostCTRIncrement инкрементирует нижние 64 бит (байты 8..15) счётчика CTR.
// Соответствует incr_s() из ГОСТ Р 34.13-2015 при s = 64.
func gostCTRIncrement(counter []byte) {
	for i := blockSize - 1; i >= blockSize/2; i-- {
		counter[i]++
		if counter[i] != 0 {
			return
		}
	}
}

// --- ГОСТ Р 34.13-2015, раздел 5.6: Режим выработки имитовставки (CMAC) ---

// gostCMAC вычисляет CMAC (имитовставку) по ГОСТ Р 34.13-2015 от конкатенации частей.
// Возвращает 128-битный (16 байт) тег аутентификации.
func gostCMAC(block cipher.Block, parts ...[]byte) []byte {
	k1, k2 := cmacGenerateSubkeys(block)

	totalLen := 0
	for _, p := range parts {
		totalLen += len(p)
	}

	n := blockSize
	numBlocks := (totalLen + n - 1) / n
	if numBlocks == 0 {
		numBlocks = 1
	}
	lastBlockComplete := totalLen > 0 && totalLen%n == 0

	var x [blockSize]byte
	blockIdx := 0
	partIdx := 0
	partOff := 0
	var buf [blockSize]byte

	for blockIdx < numBlocks-1 {
		fillBlock(buf[:], parts, &partIdx, &partOff)
		xorBytes(x[:], x[:], buf[:])
		block.Encrypt(x[:], x[:])
		blockIdx++
	}

	// Последний блок
	var lastBlock [blockSize]byte
	remaining := totalLen - blockIdx*n
	if remaining > 0 {
		fillBlockPartial(lastBlock[:], parts, &partIdx, &partOff, remaining)
	}

	if lastBlockComplete {
		xorBytes(x[:], x[:], lastBlock[:])
		xorBytes(x[:], x[:], k1)
	} else {
		if remaining < n {
			lastBlock[remaining] = 0x80
		}
		xorBytes(x[:], x[:], lastBlock[:])
		xorBytes(x[:], x[:], k2)
	}

	block.Encrypt(x[:], x[:])

	result := make([]byte, blockSize)
	copy(result, x[:])
	return result
}

// cmacGenerateSubkeys генерирует подключи K1, K2 для CMAC по ГОСТ Р 34.13-2015.
func cmacGenerateSubkeys(block cipher.Block) (k1, k2 []byte) {
	var L [blockSize]byte
	var zero [blockSize]byte
	block.Encrypt(L[:], zero[:])

	k1 = make([]byte, blockSize)
	shiftLeftOne(k1, L[:])
	if L[0]&0x80 != 0 {
		xorBytes(k1, k1, rb128[:])
	}

	k2 = make([]byte, blockSize)
	shiftLeftOne(k2, k1)
	if k1[0]&0x80 != 0 {
		xorBytes(k2, k2, rb128[:])
	}

	return k1, k2
}

// --- Вспомогательные функции ---

// deriveSubkey выводит 32-байтный подключ из мастер-ключа с доменным разделением.
func deriveSubkey(masterKey []byte, domain string) []byte {
	h := sha256.New()
	h.Write([]byte(domain))
	h.Write([]byte{0x00})
	h.Write(masterKey)
	sum := h.Sum(nil)
	return sum
}

// shiftLeftOne сдвигает 128-битное значение влево на 1 бит.
func shiftLeftOne(dst, src []byte) {
	overflow := byte(0)
	for i := len(src) - 1; i >= 0; i-- {
		newOverflow := src[i] >> 7
		dst[i] = (src[i] << 1) | overflow
		overflow = newOverflow
	}
}

// xorBytes XOR-ит a и b, результат в dst. Длина определяется по dst.
func xorBytes(dst, a, b []byte) {
	for i := 0; i < len(dst) && i < len(a) && i < len(b); i++ {
		dst[i] = a[i] ^ b[i]
	}
}

// fillBlock заполняет buf полным блоком из потока частей.
func fillBlock(buf []byte, parts [][]byte, partIdx, partOff *int) {
	n := 0
	for n < blockSize && *partIdx < len(parts) {
		avail := len(parts[*partIdx]) - *partOff
		need := blockSize - n
		if avail <= need {
			copy(buf[n:], parts[*partIdx][*partOff:])
			n += avail
			*partIdx++
			*partOff = 0
		} else {
			copy(buf[n:], parts[*partIdx][*partOff:*partOff+need])
			n += need
			*partOff += need
		}
	}
}

// fillBlockPartial заполняет buf частичным блоком (remaining байт).
func fillBlockPartial(buf []byte, parts [][]byte, partIdx, partOff *int, remaining int) {
	n := 0
	for n < remaining && *partIdx < len(parts) {
		avail := len(parts[*partIdx]) - *partOff
		need := remaining - n
		if avail <= need {
			copy(buf[n:], parts[*partIdx][*partOff:])
			n += avail
			*partIdx++
			*partOff = 0
		} else {
			copy(buf[n:], parts[*partIdx][*partOff:*partOff+need])
			n += need
			*partOff += need
		}
	}
}

// zeroSlice обнуляет срез в памяти.
func zeroSlice(b []byte) {
	for i := range b {
		b[i] = 0
	}
}
