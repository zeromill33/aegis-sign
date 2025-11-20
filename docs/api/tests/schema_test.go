package tests

import (
	"os"
	"path/filepath"
	"testing"

	"gopkg.in/yaml.v3"
)

func loadOpenAPI(t *testing.T) map[string]any {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "openapi.yaml"))
	if err != nil {
		t.Fatalf("read openapi: %v", err)
	}
	var doc map[string]any
	if err := yaml.Unmarshal(data, &doc); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	return doc
}

func TestSignRequestDigestSchema(t *testing.T) {
	doc := loadOpenAPI(t)
	components := doc["components"].(map[string]any)
	schemas := components["schemas"].(map[string]any)
	signRequest := schemas["SignRequest"].(map[string]any)
	props := signRequest["properties"].(map[string]any)
	digest := props["digest"].(map[string]any)
	variants, ok := digest["oneOf"].([]any)
	if !ok || len(variants) != 2 {
		t.Fatalf("digest.oneOf expects 2 variants, got %v", len(variants))
	}

	hexDigest := schemas["HexDigest"].(map[string]any)
	if hexDigest["pattern"] == nil {
		t.Fatal("HexDigest must include pattern")
	}

	base64Digest := schemas["Base64Digest"].(map[string]any)
	if base64Digest["minLength"] == nil || base64Digest["maxLength"] == nil {
		t.Fatal("Base64Digest must bound length")
	}
}

func TestHeaderParametersExist(t *testing.T) {
	doc := loadOpenAPI(t)
	components := doc["components"].(map[string]any)
	params := components["parameters"].(map[string]any)
	if _, ok := params["RequestId"]; !ok {
		t.Fatal("RequestId header missing")
	}
	if _, ok := params["TenantId"]; !ok {
		t.Fatal("TenantId header missing")
	}
}

func TestInvalidKeyResponseDocumented(t *testing.T) {
	doc := loadOpenAPI(t)
	components := doc["components"].(map[string]any)
	responses := components["responses"].(map[string]any)
	if _, ok := responses["InvalidKey"]; !ok {
		t.Fatal("InvalidKey response missing")
	}
	paths := doc["paths"].(map[string]any)
	sign := paths["/sign"].(map[string]any)["post"].(map[string]any)
	signResponses := sign["responses"].(map[string]any)
	if _, ok := signResponses["404"]; !ok {
		t.Fatal("/sign must document 404 InvalidKey response")
	}
}
