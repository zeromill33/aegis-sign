package tests

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestProtoIncludesAuditContext(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "proto", "signer.proto"))
	if err != nil {
		t.Fatalf("read proto: %v", err)
	}
	content := string(data)
	checks := []string{
		"message AuditContext",
		"enum DigestEncoding",
		"enum ApiErrorCode",
		"ErrorStatus",
		"API_ERROR_CODE_INVALID_KEY",
	}
	for _, token := range checks {
		if !strings.Contains(content, token) {
			t.Fatalf("proto missing %s", token)
		}
	}
}
