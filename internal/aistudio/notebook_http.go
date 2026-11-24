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
const notebookHTTPMaxRedirects = 10

// ErrNotebookAuthentication indicates that the linked Playground cookies do
// not authenticate a NotebookLM request. A separate Chrome browser must NOT
// be silently launched in response to this error.
var ErrNotebookAuthentication = errors.New("NotebookLM requires Google authentication with the linked Playground account")
var ErrNotebookRedirect = errors.New("NotebookLM returned an unsupported redirect")

// notebookRedirectTarget accepts only HTTPS redirects between NotebookLM and
// Google's first-party account-login host. The shared SpiderMonkey HTTP client
// keeps automatic redirects disabled; each hop gets fresh host-scoped cookies
// from the existing session jar, never a forwarded Cookie header.
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
	switch next.Host {
	case "notebook.google.com", "accounts.google.com":
		return next, nil
	default:
		return nil, fmt.Errorf("%w: destination outside NotebookLM / Google account login (%s)", ErrNotebookRedirect, next.Hostname())
	}
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
	return worker.notebookHTTP(ctx, method, rawURL, headers, body, nil)
}

// NotebookHTTPStream keeps the same authentication, origin and redirect checks
// as ordinary Notebook HTTP while reading a successful response incrementally.
func (worker *NativeWorker) NotebookHTTPStream(ctx context.Context, method, rawURL string, headers http.Header, body []byte, onChunk func([]byte) error) (int, error) {
	if onChunk == nil {
		return 0, errors.New("Notebook HTTP streaming callback is required")
	}
	status, _, err := worker.notebookHTTP(ctx, method, rawURL, headers, body, onChunk)
	return status, err
}

func (worker *NativeWorker) notebookHTTP(ctx context.Context, method, rawURL string, headers http.Header, body []byte, onChunk func([]byte) error) (int, []byte, error) {
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
	// SpiderMonkey intentionally sets CheckRedirect=ErrUseLastResponse. A
	// Notebook bootstrap can legitimately pass through accounts.google.com
	// for a SetOSID/cookie-rotation handshake before returning to Notebook.
	// We follow only trusted GET hops, using runtime.do for each destination
	// so CookieHeader selects domain-specific cookies and Set-Cookie is merged.
	// The Playground WAA client and its redirect policy remain unchanged.
	for redirects := 0; ; redirects++ {
		response, err := runtime.do(ctx, method, rawURL, body, requestHeaders)
		if err != nil {
			return 0, nil, fmt.Errorf("Notebook HTTP request: %w", err)
		}
		if response.Request == nil || response.Request.URL == nil ||
			response.Request.URL.Scheme != "https" ||
			(response.Request.URL.Host != "notebook.google.com" && response.Request.URL.Host != "accounts.google.com") {
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
				if next.Host == "accounts.google.com" {
					return 0, nil, ErrNotebookAuthentication
				}
				return 0, nil, fmt.Errorf("%w: Notebook RPC returned HTTP %d", ErrNotebookRedirect, response.StatusCode)
			}
			if redirects >= notebookHTTPMaxRedirects {
				return 0, nil, fmt.Errorf("%w: exceeded %d trusted GET hops", ErrNotebookRedirect, notebookHTTPMaxRedirects)
			}
			rawURL = next.String()
			continue
		}
		if response.Request.URL.Host == "accounts.google.com" {
			// Google stopped on a sign-in form, challenge or error instead
			// of completing the cookie rotation and returning to Notebook.
			_ = response.Body.Close()
			return 0, nil, ErrNotebookAuthentication
		}
		if onChunk != nil {
			defer response.Body.Close()
			if response.StatusCode < 200 || response.StatusCode >= 300 {
				return response.StatusCode, nil, nil
			}
			return response.StatusCode, nil, readNotebookHTTPStream(ctx, response.Body, onChunk)
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

func readNotebookHTTPStream(ctx context.Context, body io.Reader, onChunk func([]byte) error) error {
	buffer := make([]byte, 16<<10)
	total := 0
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		n, err := body.Read(buffer)
		total += n
		if total > notebookHTTPMaxResponseBytes {
			return errors.New("Notebook HTTP response exceeds size limit")
		}
		if n > 0 {
			if callbackErr := onChunk(buffer[:n]); callbackErr != nil {
				return callbackErr
			}
		}
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return fmt.Errorf("read Notebook HTTP stream: %w", err)
		}
	}
}
