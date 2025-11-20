package validator

import "testing"

func TestValidateDigest(t *testing.T) {
	hexDigest := "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	if err := ValidateDigest(hexDigest, DigestEncodingHex); err != nil {
		t.Fatalf("hex digest should be valid: %v", err)
	}
	decodedHex, err := DecodeDigest(hexDigest, DigestEncodingHex)
	if err != nil {
		t.Fatalf("decode hex failed: %v", err)
	}
	if len(decodedHex) != 32 {
		t.Fatalf("hex decode len=%d, want 32", len(decodedHex))
	}

	base64Digest := "AAECAwQFBgcICQoLDA0ODxAREhMUFRYXGBkaGxwdHh8="
	if err := ValidateDigest(base64Digest, DigestEncodingBase64); err != nil {
		t.Fatalf("base64 digest should be valid: %v", err)
	}
	if _, err := DecodeDigest(base64Digest, DigestEncodingBase64); err != nil {
		t.Fatalf("decode base64 failed: %v", err)
	}

	if err := ValidateDigest("zzz", DigestEncodingHex); err == nil {
		t.Fatal("expected error for invalid hex")
	}

	if err := ValidateDigest(hexDigest+"00", DigestEncodingHex); err == nil {
		t.Fatal("expected error for non 32 byte hex")
	}

	if _, err := NormalizeEncoding("hex"); err != nil {
		t.Fatalf("normalize hex failed: %v", err)
	}
	if _, err := NormalizeEncoding("base64"); err != nil {
		t.Fatalf("normalize base64 failed: %v", err)
	}
	if _, err := NormalizeEncoding("HEX"); err != nil {
		t.Fatalf("normalize uppercase failed: %v", err)
	}
	if _, err := NormalizeEncoding("unknown"); err == nil {
		t.Fatal("expected error for unknown encoding")
	}
}
