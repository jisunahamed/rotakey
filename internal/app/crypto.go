package app

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"

	"golang.org/x/crypto/argon2"
)

type vault struct {
	aead cipher.AEAD
}

func newVault(key []byte) (*vault, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &vault{aead: aead}, nil
}

func (v *vault) Encrypt(plain []byte) ([]byte, error) {
	nonce := make([]byte, v.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	out := make([]byte, 1, 1+len(nonce)+len(plain)+v.aead.Overhead())
	out[0] = 1
	out = append(out, nonce...)
	out = v.aead.Seal(out, nonce, plain, nil)
	return out, nil
}

func (v *vault) Decrypt(ciphertext []byte) ([]byte, error) {
	nonceSize := v.aead.NonceSize()
	if len(ciphertext) < 1+nonceSize || ciphertext[0] != 1 {
		return nil, errors.New("invalid encrypted value")
	}
	nonce := ciphertext[1 : 1+nonceSize]
	return v.aead.Open(nil, nonce, ciphertext[1+nonceSize:], nil)
}

func randomToken(prefix string, bytesCount int) (string, error) {
	raw := make([]byte, bytesCount)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return prefix + base64.RawURLEncoding.EncodeToString(raw), nil
}

func newID(prefix string) (string, error) {
	return randomToken(prefix+"_", 18)
}

func hashAPIKey(value string) []byte {
	sum := sha256.Sum256([]byte(value))
	return sum[:]
}

func secureEqual(left, right []byte) bool {
	return len(left) == len(right) && subtle.ConstantTimeCompare(left, right) == 1
}

func hashPassword(password string) (string, error) {
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	const (
		memory      = 64 * 1024
		iterations  = 3
		parallelism = 2
		keyLength   = 32
	)
	hash := argon2.IDKey([]byte(password), salt, iterations, memory, parallelism, keyLength)
	return fmt.Sprintf("$argon2id$v=19$m=%d,t=%d,p=%d$%s$%s",
		memory,
		iterations,
		parallelism,
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(hash),
	), nil
}

func verifyPassword(encoded, password string) bool {
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[1] != "argon2id" || parts[2] != "v=19" {
		return false
	}
	var memory uint32
	var iterations uint32
	var parallelism uint8
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &memory, &iterations, &parallelism); err != nil {
		return false
	}
	salt, err := base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil {
		return false
	}
	expected, err := base64.RawStdEncoding.DecodeString(parts[5])
	if err != nil {
		return false
	}
	actual := argon2.IDKey([]byte(password), salt, iterations, memory, parallelism, uint32(len(expected)))
	return secureEqual(actual, expected)
}

func secretSuffix(value string) string {
	if len(value) <= 4 {
		return strings.Repeat("•", len(value))
	}
	return value[len(value)-4:]
}
