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

// Google's SetOSID cookie refresh is a cross-host roundtrip. It must use
// domain-specific cookies on EACH hop and merge account Set-Cookie before
// returning to Notebook, without a browser or forwarding a raw Cookie header.
func TestNotebookGoogleAccountCookieRenewalRoundTrip(t *testing.T) {
	var hosts []string
	worker := notebookRedirectWorker(notebookRedirectRoundTrip(func(r *http.Request) (*http.Response, error) {
		hosts = append(hosts, r.URL.Host)
		cookie := r.Header.Get("Cookie")
		if !strings.Contains(cookie, "SID=test-cookie") {
			t.Fatalf("shared Google cookie was not selected for %s", r.URL.Host)
		}
		switch len(hosts) {
		case 1:
			if r.URL.Host != "notebook.google.com" {
				t.Fatalf("unexpected first host %s", r.URL.Host)
			}
			return notebookHTTPResponse(r, 302, "https://accounts.google.com/ServiceLogin?continue=https%3A%2F%2Fnotebook.google.com%2F", ""), nil
		case 2:
			if r.URL.Host != "accounts.google.com" {
				t.Fatalf("unexpected Google account host %s", r.URL.Host)
			}
			resp := notebookHTTPResponse(r, 302, "/SetOSID?continue=https%3A%2F%2Fnotebook.google.com%2F", "")
			resp.Header.Add("Set-Cookie", "SIDCC=rotated; Domain=.google.com; Path=/; Secure; HttpOnly")
			return resp, nil
		case 3:
			if r.URL.Host != "accounts.google.com" || !strings.Contains(cookie, "SIDCC=rotated") {
				t.Fatalf("account-scoped cookie rotation did not propagate to account host")
			}
			return notebookHTTPResponse(r, 302, "https://notebook.google.com/", ""), nil
		case 4:
			if r.URL.Host != "notebook.google.com" || !strings.Contains(cookie, "SIDCC=rotated") {
				t.Fatalf("account Set-Cookie not applied when returning to Notebook")
			}
			return notebookHTTPResponse(r, 200, "", "authenticated notebook page"), nil
		default:
			t.Fatalf("unexpected redirect hop %d", len(hosts))
			return nil, nil
		}
	}))
	status, body, err := worker.NotebookHTTP(context.Background(), "GET", "https://notebook.google.com/", nil, nil)
	if err != nil || status != 200 || string(body) != "authenticated notebook page" {
		t.Fatalf("cookie renewal: status=%d body=%q err=%v", status, body, err)
	}
	if len(hosts) != 4 {
		t.Fatalf("expected 4 Google-only hops, got %v", hosts)
	}
}

func TestNotebookGoogleAccountCookieScopesAreNotFlattened(t *testing.T) {
	var calls int
	worker := notebookRedirectWorker(notebookRedirectRoundTrip(func(r *http.Request) (*http.Response, error) {
		calls++
		cookie := r.Header.Get("Cookie")
		if r.URL.Host == "notebook.google.com" {
			if strings.Contains(cookie, "ACCOUNTS_ONLY=") {
				t.Fatal("accounts.google.com cookie leaked to Notebook")
			}
			if calls == 1 {
				return notebookHTTPResponse(r, 302, "https://accounts.google.com/ServiceLogin", ""), nil
			}
			return notebookHTTPResponse(r, 200, "", "ok"), nil
		}
		if r.URL.Host != "accounts.google.com" {
			t.Fatalf("unsafe destination %s", r.URL.Host)
		}
		if strings.Contains(cookie, "NOTEBOOK_ONLY=") || !strings.Contains(cookie, "ACCOUNTS_ONLY=accounts") {
			t.Fatalf("incorrect cookie scoping on Google accounts host")
		}
		return notebookHTTPResponse(r, 302, "https://notebook.google.com/", ""), nil
	}))
	runtime := worker.current().(*goWAARuntime)
	runtime.session.mu.Lock()
	runtime.session.state.Cookies = append(runtime.session.state.Cookies,
		StateCookie{Name: "NOTEBOOK_ONLY", Value: "notebook", Domain: "notebook.google.com", Path: "/", Secure: true, Expires: -1},
		StateCookie{Name: "ACCOUNTS_ONLY", Value: "accounts", Domain: "accounts.google.com", Path: "/", Secure: true, Expires: -1},
	)
	runtime.session.mu.Unlock()
	status, _, err := worker.NotebookHTTP(context.Background(), "GET", "https://notebook.google.com/", nil, nil)
	if err != nil || status != 200 || calls != 3 {
		t.Fatalf("incorrect cross-origin cookie isolation: status=%d calls=%d err=%v", status, calls, err)
	}
}

func TestNotebookGoogleLoginPageStillRequiresAuthentication(t *testing.T) {
	calls := 0
	worker := notebookRedirectWorker(notebookRedirectRoundTrip(func(r *http.Request) (*http.Response, error) {
		calls++
		if r.URL.Host == "notebook.google.com" {
			return notebookHTTPResponse(r, 302, "https://accounts.google.com/ServiceLogin?continue=https://notebook.google.com/", ""), nil
		}
		if r.URL.Host != "accounts.google.com" {
			t.Fatalf("redirect escaped Google hosts: %s", r.URL.Host)
		}
		return notebookHTTPResponse(r, 200, "", "<html>Google sign in</html>"), nil
	}))
	_, _, err := worker.NotebookHTTP(context.Background(), "GET", "https://notebook.google.com/", nil, nil)
	if !errors.Is(err, ErrNotebookAuthentication) || calls != 2 {
		t.Fatalf("Google login challenge not classified: calls=%d err=%v", calls, err)
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

func TestNotebookRedirectAllowsOnlyTrustedGoogleHostnames(t *testing.T) {
	current, _ := url.Parse("https://notebook.google.com/")
	for _, target := range []string{
		"https://accounts.google.com/ServiceLogin",
		"https://notebook.google.com/u/0/",
	} {
		next, err := notebookRedirectTarget(current, target)
		if err != nil || next.String() != target {
			t.Fatalf("Google redirect %q rejected: target=%v err=%v", target, next, err)
		}
	}
	for _, target := range []string{
		"https://accounts.google.com.evil.example/ServiceLogin",
		"https://accounts.google.com:443/ServiceLogin",
		"https://www.google.com/url?redirect=https://evil.example",
	} {
		if _, err := notebookRedirectTarget(current, target); !errors.Is(err, ErrNotebookRedirect) {
			t.Fatalf("unsafe redirect %q accepted: %v", target, err)
		}
	}
}
