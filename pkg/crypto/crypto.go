// Package crypto cung cấp AES-256-GCM symmetric encryption để bảo vệ
// Apple ID password trước khi lưu vào MongoDB.
//
// QUAN TRỌNG về bảo mật:
//   - Key 32 bytes lấy từ env ENCRYPTION_KEY (base64-encoded).
//   - Nếu missing → bot sẽ FAIL TO START (không cho chạy với key default).
//   - Mỗi lần encrypt sinh nonce ngẫu nhiên 12 bytes → output thay đổi liên tục.
//   - Chống tampering bằng GCM auth tag (16 bytes).
//
// Format output (base64): [nonce(12) | ciphertext | tag(16)]
package crypto

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
)

var (
	// ErrKeyMissing returned khi ENCRYPTION_KEY env var không được set.
	ErrKeyMissing = errors.New("ENCRYPTION_KEY env var is required (32 bytes, base64); generate one with: openssl rand -base64 32")
	// ErrInvalidKey returned khi key sai length (phải là 32 bytes sau decode).
	ErrInvalidKey = errors.New("ENCRYPTION_KEY must be 32 bytes (base64-encoded)")
)

// Cipher giữ AES-GCM block đã init, dùng để encrypt/decrypt nhiều lần.
type Cipher struct {
	aead cipher.AEAD
}

// New tạo Cipher mới từ base64 key (32 bytes sau khi decode).
func New(base64Key string) (*Cipher, error) {
	if base64Key == "" {
		return nil, ErrKeyMissing
	}
	key, err := base64.StdEncoding.DecodeString(base64Key)
	if err != nil {
		return nil, fmt.Errorf("decode key: %w", err)
	}
	if len(key) != 32 {
		return nil, ErrInvalidKey
	}

	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("aes.NewCipher: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("cipher.NewGCM: %w", err)
	}
	return &Cipher{aead: aead}, nil
}

// Encrypt mã hoá plaintext, trả về base64 string sẵn sàng lưu DB.
// Mỗi lần gọi sẽ sinh nonce ngẫu nhiên → cùng input sẽ ra output khác nhau.
func (c *Cipher) Encrypt(plaintext string) (string, error) {
	if plaintext == "" {
		return "", nil
	}
	nonce := make([]byte, c.aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", fmt.Errorf("rand nonce: %w", err)
	}
	sealed := c.aead.Seal(nonce, nonce, []byte(plaintext), nil)
	return base64.StdEncoding.EncodeToString(sealed), nil
}

// Decrypt giải mã base64 ciphertext về plaintext gốc.
// Trả error nếu data đã bị sửa đổi (GCM auth tag failed).
func (c *Cipher) Decrypt(b64Cipher string) (string, error) {
	if b64Cipher == "" {
		return "", nil
	}
	data, err := base64.StdEncoding.DecodeString(b64Cipher)
	if err != nil {
		return "", fmt.Errorf("decode cipher: %w", err)
	}
	nonceSize := c.aead.NonceSize()
	if len(data) < nonceSize {
		return "", errors.New("ciphertext too short")
	}
	nonce, sealed := data[:nonceSize], data[nonceSize:]
	plaintext, err := c.aead.Open(nil, nonce, sealed, nil)
	if err != nil {
		return "", fmt.Errorf("decrypt: %w", err)
	}
	return string(plaintext), nil
}

// GenerateKey sinh key 32 bytes ngẫu nhiên dưới dạng base64.
// Dùng để gen key lần đầu cho setup.
func GenerateKey() (string, error) {
	key := make([]byte, 32)
	if _, err := io.ReadFull(rand.Reader, key); err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(key), nil
}

// IsEncrypted heuristic check — xem chuỗi có vẻ là ciphertext (base64) chưa.
// Dùng cho migration: phân biệt password plaintext cũ với ciphertext mới.
// Plaintext password thường có ký tự đặc biệt, ciphertext base64 thì không.
func IsEncrypted(s string) bool {
	if s == "" || len(s) < 28 {
		return false
	}
	// Base64 chỉ chứa [A-Za-z0-9+/=]; nếu có ký tự khác → plaintext cũ.
	for _, c := range s {
		if !((c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') || c == '+' || c == '/' || c == '=') {
			return false
		}
	}
	// Phải decode được base64
	if _, err := base64.StdEncoding.DecodeString(s); err != nil {
		return false
	}
	return true
}
