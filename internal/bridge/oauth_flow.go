package bridge

import (
    "context"
    "crypto/rand"
    "crypto/sha256"
    "crypto/subtle"
    "encoding/base64"
    "encoding/json"
    "errors"
    "fmt"
    "html"
    "io"
    "net"
    "net/http"
    "net/url"
    "os"
    "path/filepath"
    "strings"
    "time"
)

const (
    oauthCallbackURL = "http://localhost:51121/oauth-callback"
    oauthAuthURL = "https://accounts.google.com/o/oauth2/v2/auth"
    oauthTokenURL = "https://oauth2.googleapis.com/token"
    oauthFlowTimeout = 5 * time.Minute
)

// StartOAuth creates a one-time local PKCE login flow. Only an authenticated
// administration endpoint may invoke it. The callback is bound to loopback;
// neither the code nor the resulting Google tokens are exposed through an API.
func (p *Provider) StartOAuth() (string, error) {
    if strings.TrimSpace(p.cfg.OAuthFile)=="" || strings.TrimSpace(p.cfg.OAuthClientID)=="" {
        return "", &APIError{Status: 400,Code:"oauth_not_configured",
            Message:"ANTIGRAVITY_OAUTH_FILE and ANTIGRAVITY_OAUTH_CLIENT_ID are required for OAuth login"}
    }
    if err:=checkCredentialOwnership(p.cfg.OAuthFile);err!=nil {return "",err}

    p.flowMu.Lock()
    if p.flowActive {
        p.flowMu.Unlock()
        return "", &APIError{Status:409,Code:"oauth_in_progress",Message:"An Antigravity OAuth login is already in progress"}
    }
    p.flowActive=true
    p.flowMu.Unlock()
    releaseFlow:=func(){
        p.flowMu.Lock()
        p.flowActive=false
        p.flowMu.Unlock()
    }
    state,err:=oauthRandom(24)
    if err!=nil {releaseFlow();return "",err}
    verifier,err:=oauthRandom(48)
    if err!=nil {releaseFlow();return "",err}
    hash:=sha256.Sum256([]byte(verifier))
    challenge:=base64.RawURLEncoding.EncodeToString(hash[:])

    listener,err:=net.Listen("tcp4","127.0.0.1:51121")
    if err!=nil {
        releaseFlow()
        return "",&APIError{Status:409,Code:"oauth_callback_busy",Message:"OAuth callback port 51121 is in use"}
    }
    ctx,cancel:=context.WithTimeout(context.Background(),oauthFlowTimeout)
    mux:=http.NewServeMux()
    server:=&http.Server{Handler:mux,ReadHeaderTimeout:10*time.Second,IdleTimeout:10*time.Second}
    finish:=make(chan struct{},1)

    mux.HandleFunc("/oauth-callback",func(w http.ResponseWriter,r *http.Request) {
        w.Header().Set("Cache-Control","no-store")
        w.Header().Set("X-Content-Type-Options","nosniff")
        w.Header().Set("Referrer-Policy","no-referrer")
        w.Header().Set("Content-Security-Policy","default-src 'none'; style-src 'unsafe-inline'")
        if r.Method!=http.MethodGet {
            oauthHTML(w,http.StatusMethodNotAllowed,"Method not allowed")
            return
        }
        if r.Host!="localhost:51121" && r.Host!="127.0.0.1:51121" {
            oauthHTML(w,http.StatusBadRequest,"Unrecognized callback host")
            return
        }
        givenState:=r.URL.Query().Get("state")
        if subtle.ConstantTimeCompare([]byte(givenState),[]byte(state))!=1 {
            oauthHTML(w,http.StatusBadRequest,"OAuth state mismatch")
            return
        }
        if denied:=r.URL.Query().Get("error");denied!="" {
            oauthHTML(w,http.StatusBadRequest,"OAuth permission was not granted")
            select {case finish<-struct{}{}:default:}
            return
        }
        code:=r.URL.Query().Get("code")
        if code==""||len(code)>8192 {
            oauthHTML(w,http.StatusBadRequest,"Missing or invalid authorization code")
            return
        }
        // Use a bounded context independent of the browser's socket lifetime.
        requestCtx,stop:=context.WithTimeout(ctx,30*time.Second)
        defer stop()
        credential,err:=p.exchangeOAuthCode(requestCtx,code,verifier)
        if err!=nil {
            oauthHTML(w,http.StatusBadGateway,"Token exchange failed. Check the OAuth client configuration.")
            select {case finish<-struct{}{}:default:}
            return
        }
        if credential.RefreshToken=="" {
            oauthHTML(w,http.StatusBadGateway,"Google did not return a refresh token. Revoke the grant and retry.")
            select {case finish<-struct{}{}:default:}
            return
        }
        credential.ManagedBy="aistudio2api"
        credential.ClientID=p.cfg.OAuthClientID
        credential.ProjectID=p.cfg.ProjectID
        if credential.ProjectID=="" {
            // Project discovery may legitimately fail until the account is
            // provisioned. Saving valid tokens is still useful for a retry.
            if project,err:=p.discoverProject(requestCtx,credential.AccessToken);err==nil {
                credential.ProjectID=project
            }
        }
        if err:=saveOAuthCredential(p.cfg.OAuthFile,credential);err!=nil {
            oauthHTML(w,http.StatusInternalServerError,"Could not save OAuth credential file")
            select {case finish<-struct{}{}:default:}
            return
        }
        p.oauth.Lock()
        p.oauth.access,p.oauth.refresh,p.oauth.expiry,p.oauth.project="","","",time.Time{}
        p.oauth.Unlock()
        oauthHTML(w,http.StatusOK,"Authentication successful. You may close this page.")
        select {case finish<-struct{}{}:default:}
    })
    go func() {
        defer releaseFlow()
        defer cancel()
        go func(){
            select {
            case <-finish:
            case <-ctx.Done():
            }
            stopCtx,stop:=context.WithTimeout(context.Background(),3*time.Second)
            _=server.Shutdown(stopCtx)
            stop()
        }()
        _=server.Serve(listener)
    }()

    params:=url.Values{}
    params.Set("client_id",p.cfg.OAuthClientID)
    params.Set("redirect_uri",oauthCallbackURL)
    params.Set("response_type","code")
    params.Set("scope",strings.Join([]string{
        "https://www.googleapis.com/auth/cloud-platform",
        "https://www.googleapis.com/auth/userinfo.email",
        "https://www.googleapis.com/auth/cclog",
        "https://www.googleapis.com/auth/experimentsandconfigs",
    }," "))
    params.Set("access_type","offline")
    params.Set("prompt","consent")
    params.Set("state",state)
    params.Set("code_challenge",challenge)
    params.Set("code_challenge_method","S256")
    return oauthAuthURL+"?"+params.Encode(),nil
}

func oauthRandom(n int) (string,error) {
    b:=make([]byte,n)
    if _,err:=rand.Read(b);err!=nil{return "",err}
    return base64.RawURLEncoding.EncodeToString(b),nil
}

func (p *Provider) exchangeOAuthCode(ctx context.Context, code,verifier string) (authFile,error) {
    form:=url.Values{
        "code":{code},
        "client_id":{p.cfg.OAuthClientID},
        "code_verifier":{verifier},
        "redirect_uri":{oauthCallbackURL},
        "grant_type":{"authorization_code"},
    }
    if p.cfg.OAuthClientSecret!="" {form.Set("client_secret",p.cfg.OAuthClientSecret)}
    req,err:=http.NewRequestWithContext(ctx,http.MethodPost,oauthTokenURL,strings.NewReader(form.Encode()))
    if err!=nil{return authFile{},err}
    req.Header.Set("Content-Type","application/x-www-form-urlencoded")
    resp,err:=p.http.Do(req)
    if err!=nil{return authFile{},err}
    defer resp.Body.Close()
    if resp.StatusCode!=http.StatusOK {
        _,_=io.Copy(io.Discard,io.LimitReader(resp.Body,4096))
        return authFile{},fmt.Errorf("Google token endpoint rejected authorization code (HTTP %d)",resp.StatusCode)
    }
    var result struct {
        Access string `json:"access_token"`
        Refresh string `json:"refresh_token"`
        ExpiresIn int `json:"expires_in"`
        TokenType string `json:"token_type"`
    }
    if err:=json.NewDecoder(io.LimitReader(resp.Body,1<<20)).Decode(&result);err!=nil{return authFile{},err}
    if result.Access==""||result.ExpiresIn<=0 {return authFile{},errors.New("OAuth token response was incomplete")}
    return authFile{
        AccessToken:result.Access,
        RefreshToken:result.Refresh,
        ExpiryDate:time.Now().Add(time.Duration(result.ExpiresIn)*time.Second).UnixMilli(),
        TokenType:result.TokenType,
    },nil
}

// Never replace a file issued by CLIProxyAPI/Antigravity or another program.
// This endpoint may only create a new file or rotate one previously created
// by this gateway. Symlinks are rejected to prevent unexpected overwrites.
func checkCredentialOwnership(path string) error {
    info,err:=os.Lstat(path)
    if os.IsNotExist(err) {return nil}
    if err!=nil{return err}
    if !info.Mode().IsRegular() {
        return &APIError{Status:409,Code:"oauth_file_conflict",Message:"OAuth credential path must be a regular file"}
    }
    data,err:=os.ReadFile(path)
    if err!=nil{return err}
    var previous authFile
    if json.Unmarshal(data,&previous)!=nil || previous.ManagedBy!="aistudio2api" {
        return &APIError{Status:409,Code:"oauth_file_owned_elsewhere",
            Message:"OAuth credential file already exists and is not managed by this gateway; configure a new file path"}
    }
    return nil
}

func saveOAuthCredential(path string,file authFile) error {
    if err:=checkCredentialOwnership(path);err!=nil{return err}
    dir:=filepath.Dir(path)
    if err:=os.MkdirAll(dir,0700);err!=nil{return err}
    raw,err:=json.MarshalIndent(file,"","  ")
    if err!=nil{return err}
    tmp,err:=os.CreateTemp(dir,".antigravity-oauth-*.tmp")
    if err!=nil{return err}
    defer os.Remove(tmp.Name())
    defer tmp.Close()
    if err:=tmp.Chmod(0600);err!=nil{return err}
    if _,err:=tmp.Write(raw);err!=nil{return err}
    if err:=tmp.Sync();err!=nil{return err}
    if err:=tmp.Close();err!=nil{return err}
    return os.Rename(tmp.Name(),path)
}
func oauthHTML(w http.ResponseWriter,status int,message string) {
    w.Header().Set("Content-Type","text/html; charset=utf-8")
    w.WriteHeader(status)
    _,_=fmt.Fprintf(w,"<!doctype html><meta charset='utf-8'><title>Antigravity OAuth</title><body style='font-family:sans-serif;padding:40px'><h2>%s</h2></body>",html.EscapeString(message))
}
