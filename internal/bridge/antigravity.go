package bridge

import (
    "bytes"
    "context"
    "crypto/rand"
    "encoding/hex"
    "encoding/json"
    "fmt"
    "io"
    "net/http"
    "net/url"
    "os"
    "strings"
    "sync"
    "time"
)

const antigravityEndpoint = "https://daily-cloudcode-pa.googleapis.com"

type tokenCache struct {
    sync.Mutex
    access, refresh, project string
    expiry time.Time
}

type authFile struct {
    AccessToken string `json:"access_token"`
    RefreshToken string `json:"refresh_token"`
    ExpiryDate int64 `json:"expiry_date"`
    Expired string `json:"expired"`
    ProjectID string `json:"project_id"`
    ClientID string `json:"client_id"`
    ClientSecret string `json:"client_secret"`
    Token *authFile `json:"token"`
}

func (a authFile) flatten() authFile {
    if a.Token == nil { return a }
    inner := *a.Token
    if inner.ProjectID == "" { inner.ProjectID = a.ProjectID }
    if inner.ClientID == "" { inner.ClientID = a.ClientID }
    if inner.ClientSecret == "" { inner.ClientSecret = a.ClientSecret }
    return inner
}

func (a authFile) expiryTime() time.Time {
    if a.ExpiryDate > 1_000_000_000_000 {
        return time.UnixMilli(a.ExpiryDate)
    }
    if a.ExpiryDate > 0 { return time.Unix(a.ExpiryDate, 0) }
    if a.Expired != "" {
        if t, err := time.Parse(time.RFC3339Nano, a.Expired); err == nil { return t }
    }
    return time.Time{}
}

func randomID() string {
    var b [16]byte
    if _, err := rand.Read(b[:]); err != nil { return fmt.Sprint(time.Now().UnixNano()) }
    return hex.EncodeToString(b[:])
}

// authToken reads an existing Antigravity/CLIProxyAPI OAuth credential file.
// Only the in-memory access token is refreshed; the original file is untouched
// because another process may own it. The caller is responsible for protecting
// the file using OS file permissions.
func (p *Provider) authToken(ctx context.Context) (string, string, error) {
    if p.cfg.OAuthFile == "" {
        return "", "", &APIError{503, "auth_not_configured", "ANTIGRAVITY_OAUTH_FILE is not configured"}
    }
    raw, err := os.ReadFile(p.cfg.OAuthFile)
    if err != nil {
        return "", "", &APIError{503, "auth_not_configured", "Antigravity OAuth credential file cannot be read"}
    }
    var file authFile
    if err := json.Unmarshal(raw, &file); err != nil {
        return "", "", &APIError{400, "invalid_credentials", "Antigravity credential file is not valid JSON"}
    }
    file = file.flatten()
    if file.AccessToken == "" && file.RefreshToken == "" {
        return "", "", &APIError{401, "auth_required", "Antigravity credential file has no OAuth tokens"}
    }
    p.oauth.Lock()
    defer p.oauth.Unlock()
    access, expiry := file.AccessToken, file.expiryTime()
    if p.oauth.refresh == file.RefreshToken && p.oauth.access != "" && p.oauth.expiry.After(time.Now().Add(2*time.Minute)) {
        access, expiry = p.oauth.access, p.oauth.expiry
    }
    if !expiry.After(time.Now().Add(2*time.Minute)) {
        if file.RefreshToken == "" {
            return "", "", &APIError{401, "auth_expired", "Antigravity OAuth token expired and has no refresh token"}
        }
        id, secret := p.cfg.OAuthClientID, p.cfg.OAuthClientSecret
        if id == "" { id = file.ClientID }
        if secret == "" { secret = file.ClientSecret }
        if id == "" {
            return "", "", &APIError{503, "oauth_client_required", "Set ANTIGRAVITY_OAUTH_CLIENT_ID to refresh the OAuth token"}
        }
        form := url.Values{
            "client_id": {id}, "refresh_token": {file.RefreshToken}, "grant_type": {"refresh_token"},
        }
        if secret != "" { form.Set("client_secret", secret) }
        req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://oauth2.googleapis.com/token", strings.NewReader(form.Encode()))
        if err != nil { return "", "", err }
        req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
        resp, err := p.http.Do(req)
        if err != nil { return "", "", fmt.Errorf("Antigravity OAuth refresh request failed: %w", err) }
        defer resp.Body.Close()
        if resp.StatusCode != http.StatusOK {
            return "", "", &APIError{401, "auth_refresh_failed", fmt.Sprintf("Antigravity OAuth refresh rejected (HTTP %d)", resp.StatusCode)}
        }
        var refreshed struct {
            Access string `json:"access_token"`
            ExpiresIn int `json:"expires_in"`
        }
        if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&refreshed); err != nil || refreshed.Access == "" || refreshed.ExpiresIn <= 0 {
            return "", "", &APIError{502, "oauth_refresh_invalid", "Antigravity OAuth refresh returned invalid credentials"}
        }
        access, expiry = refreshed.Access, time.Now().Add(time.Duration(refreshed.ExpiresIn)*time.Second)
        p.oauth.access, p.oauth.expiry, p.oauth.refresh = access, expiry, file.RefreshToken
    }
    project := p.cfg.ProjectID
    if project == "" { project = file.ProjectID }
    if project == "" && p.oauth.project != "" { project = p.oauth.project }
    if project == "" {
        project, err = p.discoverProject(ctx, access)
        if err != nil { return "", "", err }
        p.oauth.project = project
    }
    return access, project, nil
}

func (p *Provider) postCloud(ctx context.Context, token, action string, payload any) ([]byte, error) {
    body, err := json.Marshal(payload)
    if err != nil { return nil, err }
    req, err := http.NewRequestWithContext(ctx, http.MethodPost, antigravityEndpoint+"/v1internal:"+action, bytes.NewReader(body))
    if err != nil { return nil, err }
    req.Header.Set("Authorization", "Bearer "+token)
    req.Header.Set("Content-Type", "application/json")
    req.Header.Set("Accept", "application/json")
    req.Header.Set("User-Agent", "antigravity/cli/1.5.7 (aidev_client; auth_method=consumer)")
    resp, err := p.http.Do(req)
    if err != nil { return nil, fmt.Errorf("Antigravity upstream request failed: %w", err) }
    defer resp.Body.Close()
    limited, err := io.ReadAll(io.LimitReader(resp.Body, 16<<20+1))
    if err != nil { return nil, err }
    if len(limited)>16<<20 { return nil, &APIError{502,"upstream_too_large","Antigravity response exceeds 16 MiB"} }
    if resp.StatusCode < 200 || resp.StatusCode >= 300 {
        status := http.StatusBadGateway
        if resp.StatusCode == 401 || resp.StatusCode == 403 || resp.StatusCode == 429 { status = resp.StatusCode }
        return nil, &APIError{status, "upstream_error", fmt.Sprintf("Antigravity upstream returned HTTP %d",resp.StatusCode)}
    }
    return limited, nil
}

func (p *Provider) discoverProject(ctx context.Context, token string) (string, error) {
    result, err := p.postCloud(ctx, token, "loadCodeAssist", map[string]any{
        "metadata": map[string]string{"ideType": "ANTIGRAVITY", "platform": "PLATFORM_UNSPECIFIED", "pluginType": "GEMINI"},
    })
    if err != nil { return "", err }
    var r struct {
        Project string `json:"cloudaicompanionProject"`
    }
    if json.Unmarshal(result,&r)!=nil || r.Project=="" {
        return "", &APIError{502, "project_missing", "Antigravity project ID is unavailable; set ANTIGRAVITY_PROJECT_ID"}
    }
    return r.Project,nil
}

func (p *Provider) antigravity(ctx context.Context, model string, messages []Message) (string,error) {
    if !validModelName(model) { return "", &APIError{400,"invalid_model","Invalid Antigravity model ID"} }
    access, project, err := p.authToken(ctx)
    if err != nil { return "",err }
    contents := make([]any, 0, len(messages))
    var system []string
    for _, m := range messages {
        if strings.TrimSpace(m.Content)=="" { continue }
        role := strings.ToLower(m.Role)
        switch role {
        case "system","developer": system=append(system,m.Content)
        case "assistant": contents=append(contents, map[string]any{"role":"model","parts":[]any{map[string]string{"text":m.Content}}})
        case "user": contents=append(contents, map[string]any{"role":"user","parts":[]any{map[string]string{"text":m.Content}}})
        default: return "", &APIError{400,"unsupported_role","Antigravity supports only system, developer, user and assistant text messages"}
        }
    }
    if len(contents)==0 { return "", &APIError{400,"invalid_request","No user or assistant message provided"} }
    inner := map[string]any{"contents":contents,"sessionId":randomID()}
    if len(system)>0 {
        inner["systemInstruction"] = map[string]any{"role":"user","parts":[]any{map[string]string{"text":strings.Join(system,"\n\n")}}}
    }
    payload := map[string]any{
        "model":model,"project":project,"request":inner,
        "userAgent":"antigravity","requestType":"agent","requestId":"agent-"+randomID(),
    }
    raw,err:=p.postCloud(ctx,access,"generateContent",payload)
    if err!=nil{return "",err}
    var result struct {
        Response json.RawMessage `json:"response"`
        Candidates []struct {
            Content struct {Parts []struct{Text string `json:"text"`; Thought bool `json:"thought"`} `json:"parts"`} `json:"content"`
        } `json:"candidates"`
    }
    if err:=json.Unmarshal(raw,&result); err!=nil{return "",&APIError{502,"upstream_invalid","Antigravity response is not valid JSON"}}
    if len(result.Response)>0 {
        if err:=json.Unmarshal(result.Response,&result);err!=nil{return "",&APIError{502,"upstream_invalid","Antigravity nested response is invalid"}}
    }
    var out strings.Builder
    for _,candidate:=range result.Candidates {
        for _,part:=range candidate.Content.Parts {
            if !part.Thought && part.Text!="" {out.WriteString(part.Text)}
        }
        if out.Len()>0 {break}
    }
    if out.Len()==0 {return "",&APIError{502,"empty_response","Antigravity returned no textual candidate"}}
    return out.String(),nil
}
