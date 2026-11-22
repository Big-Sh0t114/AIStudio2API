package notebook

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
)

const notebookOrigin = "https://notebook.google.com"
const notebookBootstrapTTL = 2 * time.Minute

// notebookBootstrap is strictly in-memory and must never be persisted or
// returned to the UI. It contains Google's transient page CSRF material.
type notebookBootstrap struct {
	Email      string
	AT         string
	BL         string
	SID        string
	Lang       string
	ValidUntil time.Time
}

var wizAssignment = regexp.MustCompile(`(?:window\.)?WIZ_global_data\s*=\s*`)
var htmlLanguage = regexp.MustCompile(`(?i)<html[^>]*\blang\s*=\s*["']([^"']+)["']`)

func parseNotebookBootstrap(body []byte) (*notebookBootstrap, error) {
	matches := wizAssignment.FindIndex(body)
	if matches == nil {
		return nil, errors.New("Notebook HTML has no WIZ_global_data bootstrap")
	}
	var globals map[string]json.RawMessage
	if err := json.NewDecoder(bytes.NewReader(body[matches[1]:])).Decode(&globals); err != nil {
		return nil, fmt.Errorf("parse Notebook WIZ_global_data: %w", err)
	}
	get := func(key string) string {
		var value string
		_ = json.Unmarshal(globals[key], &value)
		return value
	}
	meta := &notebookBootstrap{
		Email: get("oPEP7c"), AT: get("SNlM0e"), BL: get("cfb2h"),
		SID: get("FdrFJe"), Lang: "en", ValidUntil: time.Now().Add(notebookBootstrapTTL),
	}
	if match := htmlLanguage.FindSubmatch(body); len(match) == 2 {
		meta.Lang = string(match[1])
	}
	if meta.AT == "" || meta.Email == "" {
		return nil, &APIError{Status: 401, Code: "auth_required", Message: "NotebookLM is not signed in with the linked Playground account"}
	}
	return meta, nil
}

// bootstrap fetches the actual Notebook page using the SpiderMonkey worker's
// authenticated Google cookie jar and the same configured network proxy.
// No second browser or isolated Chrome profile is created.
func (b *browser) bootstrap(ctx context.Context, page HTTPPage) (*notebookBootstrap, error) {
	if b.httpBootstrap != nil && time.Now().Before(b.httpBootstrap.ValidUntil) {
		return b.httpBootstrap, nil
	}
	header := make(http.Header)
	header.Set("Accept", "text/html,application/xhtml+xml")
	response, err := page.Fetch(ctx, http.MethodGet, notebookOrigin+"/", header, nil)
	if err != nil {
		return nil, err
	}
	if response.Status == http.StatusUnauthorized || response.Status == http.StatusForbidden {
		return nil, &APIError{Status: 401, Code: "auth_required", Message: "NotebookLM sign-in is required for this Playground account"}
	}
	if response.Status != http.StatusOK {
		return nil, &APIError{Status: 502, Code: "upstream_error", Message: fmt.Sprintf("Notebook bootstrap HTTP %d", response.Status)}
	}
	meta, err := parseNotebookBootstrap(response.Body)
	if err != nil {
		return nil, err
	}
	b.httpBootstrap = meta
	return meta, nil
}

func (b *browser) loginInfo(ctx context.Context) (json.RawMessage, error) {
	if page, ok := b.shared.(HTTPPage); ok {
		meta, err := b.bootstrap(ctx, page)
		if err != nil {
			return nil, err
		}
		return json.Marshal(map[string]any{"email": meta.Email, "signedIn": true, "url": notebookOrigin + "/"})
	}
	return b.evaluate(ctx, `(() => {const w=window.WIZ_global_data||{};return {email:w.oPEP7c||'',signedIn:!!(w.SNlM0e&&w.oPEP7c),url:location.href}})()`)
}

func (b *browser) executeHTTP(ctx context.Context, page HTTPPage, rpcID string, params any, sourcePath, path string) (browserResponse, error) {
	meta, err := b.bootstrap(ctx, page)
	if err != nil {
		return browserResponse{}, err
	}
	query := url.Values{
		"bl": {meta.BL}, "f.sid": {meta.SID}, "hl": {meta.Lang},
		"_reqid": {fmt.Sprintf("%d", time.Now().UnixNano()%900000+100000)},
		"rt":     {"c"},
	}
	if rpcID != "" {
		query.Set("rpcids", rpcID)
		if sourcePath == "" {
			sourcePath = "/"
		}
		query.Set("source-path", sourcePath)
	}
	encodedParams, err := json.Marshal(params)
	if err != nil {
		return browserResponse{}, err
	}
	var wire []byte
	if rpcID != "" {
		wire, err = json.Marshal([]any{[]any{[]any{rpcID, string(encodedParams), nil, "generic"}}})
	} else {
		wire, err = json.Marshal([]any{nil, string(encodedParams)})
	}
	if err != nil {
		return browserResponse{}, err
	}
	data := url.Values{"f.req": {string(wire)}, "at": {meta.AT}}.Encode()
	if path == "" {
		path = "/_/LabsTailwindUi/data/batchexecute"
	}
	// All paths are constants controlled by the native Notebook provider.
	if !strings.HasPrefix(path, "/_/LabsTailwindUi/data/") {
		return browserResponse{}, errors.New("unrecognized Notebook RPC endpoint")
	}
	header := make(http.Header)
	header.Set("Content-Type", "application/x-www-form-urlencoded;charset=UTF-8")
	header.Set("X-Same-Domain", "1")
	header.Set("Accept", "*/*")
	response, err := page.Fetch(ctx, http.MethodPost, notebookOrigin+path+"?"+query.Encode(), header, []byte(data))
	if err != nil {
		return browserResponse{}, err
	}
	if response.Status == http.StatusForbidden || response.Status == http.StatusUnauthorized {
		// Force fresh CSRF bootstrap on retry. The RPC layer already limits
		// retries and returns a structured error if the new token also fails.
		b.httpBootstrap = nil
	}
	return browserResponse{Status: response.Status, Text: string(response.Body)}, nil
}
