package aistudio

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

type notebookRedirectRoundTrip func(*http.Request) (*http.Response, error)

func (fn notebookRedirectRoundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return fn(r) }

func notebookRedirectWorker(transport http.RoundTripper) *NativeWorker {
	runtime := &goWAARuntime{
		client: &http.Client{
			Transport:     transport,
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		},
		session: &sessionCookies{state: StorageState{
			Cookies: []StateCookie{{Name: "SID", Value: "test-cookie", Domain: ".google.com", Path: "/", Secure: true, Expires: -1}},
		}},
		userAgent:      "test-notebook-agent",
		acceptLanguage: "en",
	}
	return &NativeWorker{runtime: runtime}
}

func notebookHTTPResponse(request *http.Request, status int, location, body string) *http.Response {
	headers := make(http.Header)
	if location != "" {
		headers.Set("Location", location)
	}
	return &http.Response{
		StatusCode: status, Header: headers, Body: io.NopCloser(strings.NewReader(body)),
		Request: request,
	}
}

func TestNotebookRedirectSameOriginAndCookieReuse(t *testing.T) {
	var requests []string
	worker := notebookRedirectWorker(notebookRedirectRoundTrip(func(r *http.Request) (*http.Response, error) {
		requests = append(requests, r.URL.String())
		if r.Header.Get("Cookie") != "SID=test-cookie" {
			t.Fatalf("Google session cookies missing on hop %d: %q", len(requests), r.Header.Get("Cookie"))
		}
		if r.Header.Get("User-Agent") != "test-notebook-agent" {
			t.Fatalf("missing worker user agent")
		}
		switch r.URL.Path {
		case "/":
			return notebookHTTPResponse(r, 302, "/u/0/", ""), nil
		case "/u/0/":
			if r.URL.RawQuery == "" {
				return notebookHTTPResponse(r, 302, "https://notebook.google.com/u/0/?hl=en", ""), nil
			}
			return notebookHTTPResponse(r, 200, "", "authenticated notebook page"), nil
		default:
			return notebookHTTPResponse(r, 200, "", "authenticated notebook page"), nil
		}
	}))
	status, body, err := worker.NotebookHTTP(context.Background(), "GET", "https://notebook.google.com/", nil, nil)
	if err != nil || status != 200 || string(body) != "authenticated notebook page" {
		t.Fatalf("request: status=%d body=%q err=%v", status, body, err)
	}
	if len(requests) != 3 {
		t.Fatalf("expected 3 origin-bound requests, got %v", requests)
	}
}

func TestNotebookRedirectToGoogleLoginIsAuthError(t *testing.T) {
	calls := 0
	worker := notebookRedirectWorker(notebookRedirectRoundTrip(func(r *http.Request) (*http.Response, error) {
		calls++
		if r.URL.Host != "notebook.google.com" {
			t.Fatalf("redirect leaked to %s", r.URL.Host)
		}
		return notebookHTTPResponse(r, 302, "https://accounts.google.com/ServiceLogin?continue=https://notebook.google.com/", ""), nil
	}))
	_, _, err := worker.NotebookHTTP(context.Background(), "GET", "https://notebook.google.com/", nil, nil)
	if !errors.Is(err, ErrNotebookAuthentication) {
		t.Fatalf("login redirect not classified: %v", err)
	}
	if calls != 1 {
		t.Fatalf("cross-origin HTTP request attempted: %d", calls)
	}
}

func TestNotebookRedirectRefusesOtherHostAndInvalidSchemes(t *testing.T) {
	current, _ := url.Parse("https://notebook.google.com/")
	for _, target := range []string{
		"https://evil.example/collect", "http://notebook.google.com/",
		"//evil.example/redirect", "javascript:alert(1)", "", "https://user:pass@notebook.google.com/",
	} {
		t.Run(target, func(t *testing.T) {
			_, err := notebookRedirectTarget(current, target)
			if !errors.Is(err, ErrNotebookRedirect) {
				t.Fatalf("redirect %q was not rejected: %v", target, err)
			}
		})
	}
}

func TestNotebookRedirectStopsAfterMaxHops(t *testing.T) {
	calls := 0
	worker := notebookRedirectWorker(notebookRedirectRoundTrip(func(r *http.Request) (*http.Response, error) {
		calls++
		return notebookHTTPResponse(r, 302, "/", ""), nil
	}))
	_, _, err := worker.NotebookHTTP(context.Background(), "GET", "https://notebook.google.com/", nil, nil)
	if !errors.Is(err, ErrNotebookRedirect) || calls != notebookHTTPMaxRedirects+1 {
		t.Fatalf("expected bounded redirects, calls=%d err=%v", calls, err)
	}
}

func TestNotebookRPCDoesNotFollowRedirect(t *testing.T) {
	calls := 0
	worker := notebookRedirectWorker(notebookRedirectRoundTrip(func(r *http.Request) (*http.Response, error) {
		calls++
		if r.Method != http.MethodPost {
			t.Fatalf("unexpected method %s", r.Method)
		}
		return notebookHTTPResponse(r, 302, "/u/0/", ""), nil
	}))
	_, _, err := worker.NotebookHTTP(context.Background(), "POST", "https://notebook.google.com/_/LabsTailwindUi/data/batchexecute", nil, []byte("f.req=..."))
	if !errors.Is(err, ErrNotebookRedirect) || calls != 1 {
		t.Fatalf("POST redirect must stop: calls=%d err=%v", calls, err)
	}
}
