package validator

import (
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
)

// DigestEncoding 描述 digest 字符串的编码。
type DigestEncoding string

const (
	DigestEncodingHex    DigestEncoding = "hex"
	DigestEncodingBase64 DigestEncoding = "base64"
)

// NormalizeEncoding 将用户输入转换为内部常量。
func NormalizeEncoding(raw string) (DigestEncoding, error) {
	switch strings.ToLower(raw) {
	case "", string(DigestEncodingHex):
		return DigestEncodingHex, nil
	case string(DigestEncodingBase64):
		return DigestEncodingBase64, nil
	default:
		return "", fmt.Errorf("unsupported encoding %q", raw)
	}
}

var errDigestNot32Bytes = errors.New("digest must decode to 32 bytes")

// DecodeDigest 将 digest 解码为二进制并验证长度。
func DecodeDigest(digest string, enc DigestEncoding) ([]byte, error) {
	switch enc {
	case DigestEncodingHex:
		decoded, err := hex.DecodeString(digest)
		if err != nil {
			return nil, fmt.Errorf("invalid hex digest: %w", err)
		}
		if len(decoded) != 32 {
			return nil, errDigestNot32Bytes
		}
		return decoded, nil
	case DigestEncodingBase64:
		decoded, err := base64.StdEncoding.DecodeString(digest)
		if err != nil {
			return nil, fmt.Errorf("invalid base64 digest: %w", err)
		}
		if len(decoded) != 32 {
			return nil, errDigestNot32Bytes
		}
		return decoded, nil
	default:
		return nil, fmt.Errorf("unknown encoding %q", enc)
	}
}

// ValidateDigest 确保 digest 经解码后为 32 字节。
func ValidateDigest(digest string, enc DigestEncoding) error {
	_, err := DecodeDigest(digest, enc)
	return err
}
