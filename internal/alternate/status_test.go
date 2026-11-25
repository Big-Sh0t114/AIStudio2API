package alternate

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestAntigravityStatusReportsCredentialWithoutLeakingTokens(t *testing.T) {
	path := filepath.Join(t.TempDir(), "oauth.json")
	access := "secret-access-token"
	refresh := "secret-refresh-token"
	data, err := json.Marshal(agCredentials{
		AccessToken: access, RefreshToken: refresh, Expiry: time.Now().Add(time.Hour),
		ManagedBy: "aistudio2api",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	provider := NewAntigravity(Config{
		OAuthFile: path, ClientID: "client-id", ClientSecret: "client-secret", ProjectID: "project-id",
	})
	status := provider.Status()
	if !status.OAuthFileConfigured || !status.ClientIDConfigured || !status.ClientSecretConfigured ||
		!status.ProjectIDConfigured || !status.CredentialFileExists || !status.Authenticated {
		t.Fatalf("unexpected status: %+v", status)
	}
	encoded, err := json.Marshal(status)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), access) || strings.Contains(string(encoded), refresh) ||
		strings.Contains(string(encoded), "client-secret") {
		t.Fatalf("status leaked credential material: %s", encoded)
	}
}

func TestAntigravityStatusReportsMissingCredentials(t *testing.T) {
	provider := NewAntigravity(Config{OAuthFile: filepath.Join(t.TempDir(), "missing.json"), ClientID: "client-id"})
	status := provider.Status()
	if !status.OAuthFileConfigured || !status.ClientIDConfigured || status.CredentialFileExists || status.Authenticated {
		t.Fatalf("unexpected missing-credential status: %+v", status)
	}
}
