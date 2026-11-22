package notebook

import (
 "path/filepath"
 "strings"
 "testing"
)

func TestChromeProfileUsesAbsolutePath(t *testing.T) {
 b := &browser{profile: filepath.Join("runtime", "notebook", "profiles", "test-account")}
 expected, err := filepath.Abs(b.profile)
 if err != nil { t.Fatal(err) }
 if err := b.resolveProfile(); err != nil { t.Fatal(err) }
 if !filepath.IsAbs(b.profile) { t.Fatalf("profile is not absolute: %q", b.profile) }
 if b.profile != filepath.Clean(expected) { t.Fatalf("profile = %q, want %q", b.profile, expected) }
 if err := b.resolveProfile(); err != nil { t.Fatal(err) }
 if b.profile != filepath.Clean(expected) { t.Fatalf("profile changed after second resolution: %q", b.profile) }
}

func TestChromeProfileRejectsEmptyPath(t *testing.T) {
 b := &browser{profile: "  "}
 if err := b.resolveProfile(); err == nil || !strings.Contains(err.Error(), "empty") {
  t.Fatalf("expected empty profile error, got %v", err)
 }
}
