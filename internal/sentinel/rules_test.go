package sentinel

import (
	"strings"
	"testing"
)

// Synthetic, fabricated secrets only.
const (
	fakeBody24 = "FAKEfake0123FAKEfake4567"
	// fakeHighEntropy has 32 distinct characters, so it clears entropyThreshold.
	fakeHighEntropy = "Q7vX2mK9pL4rT8wZ1nB6cH3jF5dG0sYa"
	fakeJWT         = "eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiJmYWtlIn0.ZmFrZS1zaWduYXR1cmU"
	fakeGitHub      = "ghp_FAKEfakeFAKEfakeFAKEfakeFAKEfake0000"
)

func TestDetectSecretsNewPatterns(t *testing.T) {
	tests := []struct {
		name, in, want string
	}{
		{"sk", "key sk-test" + fakeBody24, "openai-style-key"},
		{"sk-proj", "sk-proj-" + fakeBody24, "openai-style-key"},
		{"sk-ant", "sk-ant-api03-" + fakeBody24, "openai-style-key"},
		{"jwt", "Bearer " + fakeJWT, "jwt"},
		{"short sk", "sk-abc", ""},
		{"embedded in word", "task-" + fakeBody24, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var got string
			for _, m := range DetectSecrets(tt.in) {
				if m.KnownPattern && (m.Category == "openai-style-key" || m.Category == "jwt") {
					got = m.Category
				}
			}
			if got != tt.want {
				t.Fatalf("DetectSecrets(%q) known category = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestRedactPreview(t *testing.T) {
	const sha40 = "3f786850e387550fdab836ed7e6dc881de23001b"
	const uuid = "123e4567-e89b-12d3-a456-426614174000"
	hex64 := strings.Repeat("9f86d081884c7d65", 4)
	tests := []struct {
		name, in, want string
	}{
		{"export assignment", "export FOO_TOKEN=" + fakeHighEntropy, "export FOO_TOKEN=<redacted>"},
		{"yaml assignment", "api_key: " + fakeHighEntropy, "api_key: <redacted>"},
		{"quoted assignment", `PASSWORD="` + fakeHighEntropy + `"`, `PASSWORD="<redacted>"`},
		{"bearer jwt", "curl -H 'Authorization: Bearer " + fakeJWT + "'", "curl -H 'Authorization: Bearer <redacted>'"},
		{"github token", "fatal: bad token " + fakeGitHub, "fatal: bad token <redacted>"},
		{"sk key", "401: invalid key sk-test" + fakeBody24, "401: invalid key <redacted>"},
		{"sha", "git checkout " + sha40, "git checkout " + sha40},
		{"uuid", "id " + uuid, "id " + uuid},
		{"digest", "sha256:" + hex64, "sha256:" + hex64},
		{"free-standing entropy", "value " + fakeHighEntropy, "value " + fakeHighEntropy},
		{"short value", "TOKEN=short", "TOKEN=short"},
		{"low entropy", "API_KEY=aaaaaaaaaaaaaaaaaaaa", "API_KEY=aaaaaaaaaaaaaaaaaaaa"},
		{"already masked", "api_key=<redacted>", "api_key=<redacted>"},
		{"plain", "go test ./...", "go test ./..."},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := RedactPreview(tt.in); got != tt.want {
				t.Fatalf("RedactPreview(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}
