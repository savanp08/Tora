package security

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"io"
	"os"
	"sync"
)

const appSecretKeyEnv = "APP_SECRET_KEY"

var (
	cachedCryptoKey     []byte
	cachedCryptoKeyErr  error
	cachedCryptoKeyOnce sync.Once
)

func EncryptMessage(plainText string) (string, error) {
	key, err := loadCryptoKey()
	if err != nil {
		return "", err
	}

	block, err := aes.NewCipher(key)
	if err != nil {
		return "", fmt.Errorf("create aes cipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", fmt.Errorf("create aes-gcm: %w", err)
	}

	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", fmt.Errorf("generate nonce: %w", err)
	}

	ciphertext := gcm.Seal(nil, nonce, []byte(plainText), nil)
	payload := append(nonce, ciphertext...)
	return base64.StdEncoding.EncodeToString(payload), nil
}

func DecryptMessage(encryptedBase64 string) (string, error) {
	key, err := loadCryptoKey()
	if err != nil {
		return "", err
	}

	payload, err := base64.StdEncoding.DecodeString(encryptedBase64)
	if err != nil {
		return "", fmt.Errorf("decode encrypted payload: %w", err)
	}

	block, err := aes.NewCipher(key)
	if err != nil {
		return "", fmt.Errorf("create aes cipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", fmt.Errorf("create aes-gcm: %w", err)
	}

	nonceSize := gcm.NonceSize()
	if len(payload) <= nonceSize {
		return "", fmt.Errorf("invalid encrypted payload length")
	}

	nonce := payload[:nonceSize]
	ciphertext := payload[nonceSize:]
	plaintext, err := gcm.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return "", fmt.Errorf("decrypt payload: %w", err)
	}
	return string(plaintext), nil
}

func loadCryptoKey() ([]byte, error) {
	cachedCryptoKeyOnce.Do(func() {
		rawKey := os.Getenv(appSecretKeyEnv)
		if len(rawKey) != 32 {
			cachedCryptoKeyErr = fmt.Errorf("%s must be exactly 32 bytes", appSecretKeyEnv)
			return
		}
		cachedCryptoKey = []byte(rawKey)
	})

	if cachedCryptoKeyErr != nil {
		return nil, cachedCryptoKeyErr
	}
	return cachedCryptoKey, nil
}
