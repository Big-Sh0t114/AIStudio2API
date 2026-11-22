package aistudio

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

const notebookHTTPMaxResponseBytes = 32 << 20
const notebookHTTPMaxRedirects = 5

// ErrNotebookAuthentication indicates that the linked Playground cookies do
// not authenticate a NotebookLM request. A separate Chrome browser must NOT
// be silently launched in response to this error.
var ErrNotebookAuthentication = errors.New("NotebookLM requires Google authentication with the linked Playground account")
var ErrNotebookRedirect = errors.New("NotebookLM returned an unsupported redirect")

// notebookRedirectTarget resolves only same-origin redirects. It never sends
// Google login credentials to a different host: SpiderMonkey's shared HTTP
// client has automatic redirect following disabled deliberately.
func notebookRedirectTarget(current *url.URL, location string) (*url.URL, error) {
	if strings.TrimSpace(location) == "" {
		return nil, fmt.Errorf("%w: missing Location header", ErrNotebookRedirect)
	}
	next, err := current.Parse(location)
	if err != nil {
		return nil, fmt.Errorf("%w: invalid Location header", ErrNotebookRedirect)
	}
	if next.Scheme != "https" || next.User != nil || next.Fragment != "" {
		return nil, fmt.Errorf("%w: invalid redirect scheme or credentials", ErrNotebookRedirect)
	}
	if next.Host == "accounts.google.com" {
		return nil, ErrNotebookAuthentication
	}
	if next.Host != "notebook.google.com" {
		return nil, fmt.Errorf("%w: destination outside NotebookLM (%s)", ErrNotebookRedirect, next.Hostname())
	}
	return next, nil
}

// UsesSpiderMonkey reports whether the active worker is the native Go HTTP +
// SpiderMonkey WAA runtime, rather than a Camoufox browser.
func (worker *NativeWorker) UsesSpiderMonkey() bool {
	_, ok := worker.current().(*goWAARuntime)
	return ok
}

// NotebookHTTP makes a NotebookLM request using the same HTTP client, proxy,
// user-agent, and synchronized Google cookies as the existing SpiderMonkey
// Playground worker. There is no Chrome, Camoufox tab, or second HTTP session.
// The caller must hold its Playground worker lifecycle lease.
func (worker *NativeWorker) NotebookHTTP(ctx context.Context, method, rawURL string, headers http.Header, body []byte) (int, []byte, error) {
	runtime, ok := worker.current().(*goWAARuntime)
	if !ok {
		return 0, nil, errors.New("Notebook direct HTTP transport requires a SpiderMonkey Playground worker")
	}
	target, err := url.Parse(rawURL)
	if err != nil {
		return 0, nil, err
	}
	if target.Scheme != "https" || target.Host != "notebook.google.com" || target.User != nil || target.Fragment != "" {
		return 0, nil, errors.New("Notebook HTTP transport permits only https://notebook.google.com")
	}
	switch method {
	case http.MethodGet:
		if target.Path != "/" {
			return 0, nil, errors.New("Notebook HTTP bootstrap may only fetch the origin home page")
		}
	case http.MethodPost:
		if !strings.HasPrefix(target.Path, "/_/LabsTailwindUi/data/") {
			return 0, nil, errors.New("Notebook HTTP RPC path is not permitted")
		}
	default:
		return 0, nil, errors.New("Notebook HTTP transport only supports GET and POST")
	}
	requestHeaders := headers.Clone()
	if requestHeaders == nil {
		requestHeaders = make(http.Header)
	}
	// The SpiderMonkey transport otherwise sets compression headers suited to
	// Playground's WAA protocol; request identity here for direct RPC parsing.
	requestHeaders.Set("Accept-Encoding", "identity")
	if method == http.MethodPost {
		requestHeaders.Set("Origin", "https://notebook.google.com")
		requestHeaders.Set("Referer", "https://notebook.google.com/")
		requestHeaders.Set("Sec-Fetch-Site", "same-origin")
		requestHeaders.Set("Sec-Fetch-Mode", "cors")
		requestHeaders.Set("Sec-Fetch-Dest", "empty")
	}
	// SpiderMonkey intentionally sets CheckRedirect=ErrUseLastResponse.
	// Handle the small set of legitimate Notebook same-origin GET redirects
	// explicitly, so the protected Playground transport stays unchanged.
	for redirects := 0; ; redirects++ {
		response, err := runtime.do(ctx, method, rawURL, body, requestHeaders)
		if err != nil {
			return 0, nil, fmt.Errorf("Notebook HTTP request: %w", err)
		}
		if response.Request == nil || response.Request.URL == nil ||
			response.Request.URL.Scheme != "https" || response.Request.URL.Host != "notebook.google.com" {
			_ = response.Body.Close()
			return 0, nil, ErrNotebookRedirect
		}
		switch response.StatusCode {
		case http.StatusMovedPermanently, http.StatusFound, http.StatusSeeOther,
			http.StatusTemporaryRedirect, http.StatusPermanentRedirect:
			location := response.Header.Get("Location")
			_ = response.Body.Close()
			next, err := notebookRedirectTarget(response.Request.URL, location)
			if err != nil {
				return 0, nil, err
			}
			if method != http.MethodGet {
				return 0, nil, fmt.Errorf("%w: Notebook RPC returned HTTP %d", ErrNotebookRedirect, response.StatusCode)
			}
			if redirects >= notebookHTTPMaxRedirects {
				return 0, nil, fmt.Errorf("%w: exceeded %d same-origin hops", ErrNotebookRedirect, notebookHTTPMaxRedirects)
			}
			rawURL = next.String()
			continue
		}
		content, err := io.ReadAll(io.LimitReader(response.Body, notebookHTTPMaxResponseBytes+1))
		closeErr := response.Body.Close()
		if err != nil {
			return 0, nil, fmt.Errorf("read Notebook HTTP response: %w", err)
		}
		if closeErr != nil {
			return 0, nil, fmt.Errorf("close Notebook HTTP response: %w", closeErr)
		}
		if len(content) > notebookHTTPMaxResponseBytes {
			return 0, nil, errors.New("Notebook HTTP response exceeds size limit")
		}
		return response.StatusCode, content, nil
	}
}
