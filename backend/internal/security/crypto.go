package security

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
)

const (
	appSecretKeyEnv         = "APP_SECRET_KEY"
	appSecretKeysEnv        = "APP_SECRET_KEYS"
	appSecretKeyVersionEnv  = "APP_SECRET_KEY_VERSION"
	defaultCryptoKeyVersion = "v1"
	messageCryptoAlgorithm  = "AES-256-GCM"
	encryptedFileMagic      = "TORAFILEENC1"
	encryptedFileNonceBytes = 12
	encryptedFileTagBytes   = 16
	maxKeyVersionBytes      = 255
	fileEnvelopeVersionPad  = 64
)

var (
	cachedCryptoKeyRing     cryptoKeyRing
	cachedCryptoKeyRingErr  error
	cachedCryptoKeyRingOnce sync.Once

	ErrFilePayloadNotEncrypted = errors.New("file payload is not encrypted")
	ErrFilePayloadMalformed    = errors.New("encrypted file payload is malformed")
)

type cryptoKeyRing struct {
	activeVersion string
	keys          map[string][]byte
}

func EncryptMessage(plainText string) (string, error) {
	keyRing, err := loadCryptoKeyRing()
	if err != nil {
		return "", err
	}
	key, ok := keyRing.keys[keyRing.activeVersion]
	if !ok || len(key) == 0 {
		return "", fmt.Errorf("active crypto key version %q not configured", keyRing.activeVersion)
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
	return fmt.Sprintf("%s:%s", keyRing.activeVersion, base64.StdEncoding.EncodeToString(payload)), nil
}

func MessageEncryptionAlgorithm() string {
	return messageCryptoAlgorithm
}

func ActiveMessageEncryptionKeyVersion() (string, error) {
	keyRing, err := loadCryptoKeyRing()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(keyRing.activeVersion), nil
}

func EncryptFilePayload(plain []byte) ([]byte, error) {
	keyRing, err := loadCryptoKeyRing()
	if err != nil {
		return nil, err
	}
	keyVersion := strings.TrimSpace(keyRing.activeVersion)
	if keyVersion == "" {
		return nil, fmt.Errorf("active crypto key version is not configured")
	}
	keyVersionBytes := []byte(keyVersion)
	if len(keyVersionBytes) == 0 || len(keyVersionBytes) > maxKeyVersionBytes {
		return nil, fmt.Errorf("invalid active crypto key version length")
	}
	key, ok := keyRing.keys[keyVersion]
	if !ok || len(key) == 0 {
		return nil, fmt.Errorf("active crypto key version %q not configured", keyVersion)
	}

	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("create aes cipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("create aes-gcm: %w", err)
	}

	nonceSize := gcm.NonceSize()
	if nonceSize != encryptedFileNonceBytes {
		return nil, fmt.Errorf("unexpected nonce size: %d", nonceSize)
	}

	nonce := make([]byte, nonceSize)
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, fmt.Errorf("generate nonce: %w", err)
	}

	ciphertext := gcm.Seal(nil, nonce, plain, nil)
	encoded := make([]byte, 0, len(encryptedFileMagic)+1+len(keyVersionBytes)+nonceSize+len(ciphertext))
	encoded = append(encoded, []byte(encryptedFileMagic)...)
	encoded = append(encoded, byte(len(keyVersionBytes)))
	encoded = append(encoded, keyVersionBytes...)
	encoded = append(encoded, nonce...)
	encoded = append(encoded, ciphertext...)
	return encoded, nil
}

func IsEncryptedFilePayload(payload []byte) bool {
	return len(payload) > len(encryptedFileMagic)+1 && string(payload[:len(encryptedFileMagic)]) == encryptedFileMagic
}

func DecryptFilePayload(payload []byte) ([]byte, error) {
	if !IsEncryptedFilePayload(payload) {
		return nil, ErrFilePayloadNotEncrypted
	}

	cursor := len(encryptedFileMagic)
	if len(payload) <= cursor {
		return nil, ErrFilePayloadMalformed
	}
	versionLen := int(payload[cursor])
	cursor++
	if versionLen <= 0 || versionLen > maxKeyVersionBytes {
		return nil, ErrFilePayloadMalformed
	}
	if len(payload) < cursor+versionLen+encryptedFileNonceBytes+encryptedFileTagBytes {
		return nil, ErrFilePayloadMalformed
	}

	version := strings.TrimSpace(string(payload[cursor : cursor+versionLen]))
	cursor += versionLen
	if version == "" {
		return nil, ErrFilePayloadMalformed
	}

	keyRing, err := loadCryptoKeyRing()
	if err != nil {
		return nil, err
	}
	key, ok := keyRing.keys[version]
	if !ok || len(key) == 0 {
		return nil, fmt.Errorf("unknown crypto key version %q", version)
	}

	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("create aes cipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("create aes-gcm: %w", err)
	}

	nonceSize := gcm.NonceSize()
	if nonceSize != encryptedFileNonceBytes {
		return nil, fmt.Errorf("unexpected nonce size: %d", nonceSize)
	}
	if len(payload) < cursor+nonceSize+encryptedFileTagBytes {
		return nil, ErrFilePayloadMalformed
	}

	nonce := payload[cursor : cursor+nonceSize]
	ciphertext := payload[cursor+nonceSize:]
	plaintext, err := gcm.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return nil, fmt.Errorf("decrypt file payload: %w", err)
	}
	return plaintext, nil
}

func EncryptedFilePayloadMaxBytes(plainMaxBytes int64) int64 {
	if plainMaxBytes <= 0 {
		return plainMaxBytes
	}
	overhead := int64(len(encryptedFileMagic) + 1 + fileEnvelopeVersionPad + encryptedFileNonceBytes + encryptedFileTagBytes)
	return plainMaxBytes + overhead
}

func DecryptMessage(encryptedBase64 string) (string, error) {
	keyRing, err := loadCryptoKeyRing()
	if err != nil {
		return "", err
	}

	version, payloadBase64, hasVersion := strings.Cut(strings.TrimSpace(encryptedBase64), ":")
	if hasVersion {
		key, ok := keyRing.keys[strings.TrimSpace(version)]
		if !ok || len(key) == 0 {
			return "", fmt.Errorf("unknown crypto key version %q", strings.TrimSpace(version))
		}
		return decryptMessagePayload(payloadBase64, key)
	}

	if activeKey, ok := keyRing.keys[keyRing.activeVersion]; ok && len(activeKey) > 0 {
		if plaintext, decryptErr := decryptMessagePayload(encryptedBase64, activeKey); decryptErr == nil {
			return plaintext, nil
		}
	}
	for versionID, key := range keyRing.keys {
		if versionID == keyRing.activeVersion || len(key) == 0 {
			continue
		}
		if plaintext, decryptErr := decryptMessagePayload(encryptedBase64, key); decryptErr == nil {
			return plaintext, nil
		}
	}
	return "", fmt.Errorf("decrypt payload: no configured key could decrypt payload")
}

func decryptMessagePayload(payloadBase64 string, key []byte) (string, error) {
	payload, err := base64.StdEncoding.DecodeString(strings.TrimSpace(payloadBase64))
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

func loadCryptoKeyRing() (cryptoKeyRing, error) {
	cachedCryptoKeyRingOnce.Do(func() {
		rawKey := os.Getenv(appSecretKeyEnv)
		if len(rawKey) != 32 {
			cachedCryptoKeyRingErr = fmt.Errorf("%s must be exactly 32 bytes", appSecretKeyEnv)
			return
		}
		keys := map[string][]byte{
			defaultCryptoKeyVersion: []byte(rawKey),
		}

		rawVersionedKeys := strings.TrimSpace(os.Getenv(appSecretKeysEnv))
		if rawVersionedKeys != "" {
			entries := strings.Split(rawVersionedKeys, ",")
			for _, entry := range entries {
				versionedKey := strings.TrimSpace(entry)
				if versionedKey == "" {
					continue
				}
				parts := strings.SplitN(versionedKey, ":", 2)
				if len(parts) != 2 {
					cachedCryptoKeyRingErr = fmt.Errorf("invalid %s entry %q", appSecretKeysEnv, versionedKey)
					return
				}
				versionID := strings.TrimSpace(parts[0])
				keyValue := strings.TrimSpace(parts[1])
				if versionID == "" {
					cachedCryptoKeyRingErr = fmt.Errorf("empty key version in %s entry %q", appSecretKeysEnv, versionedKey)
					return
				}
				if len(keyValue) != 32 {
					cachedCryptoKeyRingErr = fmt.Errorf(
						"%s key for version %q must be exactly 32 bytes",
						appSecretKeysEnv,
						versionID,
					)
					return
				}
				keys[versionID] = []byte(keyValue)
			}
		}

		activeVersion := strings.TrimSpace(os.Getenv(appSecretKeyVersionEnv))
		if activeVersion == "" {
			activeVersion = defaultCryptoKeyVersion
		}
		if _, exists := keys[activeVersion]; !exists {
			cachedCryptoKeyRingErr = fmt.Errorf(
				"active key version %q not found in configured keys",
				activeVersion,
			)
			return
		}

		cachedCryptoKeyRing = cryptoKeyRing{
			activeVersion: activeVersion,
			keys:          keys,
		}
	})

	if cachedCryptoKeyRingErr != nil {
		return cryptoKeyRing{}, cachedCryptoKeyRingErr
	}
	return cachedCryptoKeyRing, nil
}
