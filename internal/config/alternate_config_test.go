package config

import (
 "os"
 "path/filepath"
 "strings"
 "testing"
)

func TestAntigravitySettingsSurviveEnvSave(t *testing.T) {
 path:=filepath.Join(t.TempDir(),".env")
 cfg:=Default()
 cfg.AntigravityOAuthFile="runtime/antigravity/token.json"
 cfg.AntigravityClientID="desktop-id"
 cfg.AntigravityClientSecret="desktop-secret"
 cfg.AntigravityProjectID="project-for-test"
 if err:=cfg.Save(path);err!=nil{t.Fatal(err)}
 raw,err:=os.ReadFile(path);if err!=nil{t.Fatal(err)}
 contents:=string(raw)
 for _,entry:=range []string{
  "ANTIGRAVITY_OAUTH_FILE=runtime/antigravity/token.json",
  "ANTIGRAVITY_CLIENT_ID=desktop-id",
  "ANTIGRAVITY_CLIENT_SECRET=desktop-secret",
  "ANTIGRAVITY_PROJECT_ID=project-for-test",
 }{
  if !strings.Contains(contents,entry){t.Errorf("saved .env lacks %q",entry)}
 }
}
func TestAntigravityClientSecretRedactedFromInvalidSettings(t *testing.T) {
 entry:=invalidValue("ANTIGRAVITY_CLIENT_SECRET","should-not-leak",os.ErrInvalid)
 if entry.Value!=""{t.Fatal("OAuth client secret was not redacted")}
}
