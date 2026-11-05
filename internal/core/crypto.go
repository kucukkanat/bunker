package core

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strings"
)

var secretKey []byte

// LoadSecretKey loads BUNKER_SECRET_KEY or a generated key file in dataDir.
func LoadSecretKey(dataDir string) error {
	if v := Env("SECRET_KEY", ""); v != "" {
		sum := sha256.Sum256([]byte(v))
		secretKey = sum[:]
		return nil
	}
	p := filepath.Join(dataDir, "secret.key")
	b, err := os.ReadFile(p)
	if err == nil && len(b) >= 32 {
		k, err := hex.DecodeString(strings.TrimSpace(string(b)))
		if err == nil && len(k) == 32 {
			secretKey = k
			return nil
		}
	}
	k := make([]byte, 32)
	if _, err := rand.Read(k); err != nil {
		return err
	}
	if err := os.WriteFile(p, []byte(hex.EncodeToString(k)), 0o600); err != nil {
		return err
	}
	secretKey = k
	return nil
}

// Encrypt seals plaintext with AES-256-GCM.
func Encrypt(plain string) (string, error) {
	block, err := aes.NewCipher(secretKey)
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}
	return "v1:" + base64.RawStdEncoding.EncodeToString(gcm.Seal(nonce, nonce, []byte(plain), nil)), nil
}

// Decrypt opens a value from Encrypt.
func Decrypt(sealed string) (string, error) {
	if !strings.HasPrefix(sealed, "v1:") {
		return "", errors.New("unknown format")
	}
	raw, err := base64.RawStdEncoding.DecodeString(sealed[3:])
	if err != nil {
		return "", err
	}
	block, err := aes.NewCipher(secretKey)
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	if len(raw) < gcm.NonceSize() {
		return "", errors.New("short")
	}
	out, err := gcm.Open(nil, raw[:gcm.NonceSize()], raw[gcm.NonceSize():], nil)
	return string(out), err
}

// RandomToken returns n random bytes, base64url encoded.
func RandomToken(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}

// HashToken returns the sha256 hex of a token (for storage).
func HashToken(t string) string {
	s := sha256.Sum256([]byte(t))
	return hex.EncodeToString(s[:])
}

// SecretSetting returns a decrypted secret setting (settings stores "enc:"-prefixed values).
func (a *App) SecretSetting(key string) string {
	v := a.Setting(key)
	if strings.HasPrefix(v, "enc:") {
		p, err := Decrypt(v[4:])
		if err != nil {
			return ""
		}
		return p
	}
	return v
}

// SetSecretSetting encrypts and stores a secret setting.
func (a *App) SetSecretSetting(key, value string) error {
	if value == "" {
		return a.SetSetting(context.Background(), key, "")
	}
	e, err := Encrypt(value)
	if err != nil {
		return err
	}
	return a.SetSetting(context.Background(), key, "enc:"+e)
}

// MAC returns a truncated HMAC-SHA256 of data under the server secret,
// base64url encoded. Used for signed URLs (content tickets).
func MAC(data string) string {
	m := hmac.New(sha256.New, secretKey)
	m.Write([]byte("bunker-mac:" + data))
	return base64.RawURLEncoding.EncodeToString(m.Sum(nil)[:18])
}

// MACEqual compares MACs in constant time.
func MACEqual(a, b string) bool { return hmac.Equal([]byte(a), []byte(b)) }
