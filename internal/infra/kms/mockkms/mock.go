package mockkms

import (
	"context"
	"errors"

	kmspkg "github.com/aegis-sign/wallet/internal/infra/kms"
)

// StaticProvider 返回固定 PlainKey，用于演练/单测。
type StaticProvider struct {
	plain []byte
}

// NewStaticProvider 构造固定 Provider。
func NewStaticProvider(plain []byte) *StaticProvider {
	cp := append([]byte(nil), plain...)
	return &StaticProvider{plain: cp}
}

// Decrypt 返回预置明文。
func (p *StaticProvider) Decrypt(context.Context, kmspkg.DecryptRequest) ([]byte, error) {
	if len(p.plain) == 0 {
		return nil, errors.New("mock key empty")
	}
	return append([]byte(nil), p.plain...), nil
}

// GenerateDataKey 返回预置明文。
func (p *StaticProvider) GenerateDataKey(context.Context, kmspkg.GenerateDataKeyRequest) ([]byte, error) {
	if len(p.plain) == 0 {
		return nil, errors.New("mock key empty")
	}
	return append([]byte(nil), p.plain...), nil
}

// StaticAttestor 返回固定 attestation。
type StaticAttestor struct {
	document []byte
}

// NewStaticAttestor 构造 attestor。
func NewStaticAttestor(doc []byte) *StaticAttestor {
	cp := doc
	if cp == nil {
		cp = []byte("mock-attestation")
	}
	return &StaticAttestor{document: append([]byte(nil), cp...)}
}

// Document 返回固定文档。
func (s *StaticAttestor) Document(context.Context) ([]byte, error) {
	return append([]byte(nil), s.document...), nil
}

// Verify 在 mock 模式下不做任何校验。
func (s *StaticAttestor) Verify([]byte) error { return nil }
