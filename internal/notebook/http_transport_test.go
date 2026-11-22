package notebook

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

type fakeNotebookHTTP struct {
	getCount  int
	postCount int
	status    int
	lastURL   string
	lastBody  string
}

func (f *fakeNotebookHTTP) Fetch(_ context.Context, method, target string, _ http.Header, body []byte) (HTTPResponse, error) {
	if method == http.MethodGet {
		f.getCount++
		return HTTPResponse{Status: 200, Body: []byte(`<!doctype html><html lang="en"><script>window.WIZ_global_data = {"SNlM0e":"csrf-test","oPEP7c":"test@example.com","cfb2h":"bl-test","FdrFJe":"sid-test"};</script>`)}, nil
	}
	f.postCount++
	f.lastURL = target
	f.lastBody = string(body)
	if f.status > 0 {
		return HTTPResponse{Status: f.status, Body: []byte("error")}, nil
	}
	return HTTPResponse{Status: 200, Body: []byte(`)]}'` + "\n" + `[["wrb.fr","wXbhsf","[[1]]",null,null,null]]`)}, nil
}

func TestParseNotebookBootstrap(t *testing.T) {
	sample := []byte(`<html lang="ja"><script>window.WIZ_global_data = {"SNlM0e":"hello\\u003d","oPEP7c":"test@example.com","cfb2h":"x","FdrFJe":"y"};</script>`)
	m, err := parseNotebookBootstrap(sample)
	if err != nil {
		t.Fatal(err)
	}
	if m.AT != "hello\\u003d" && m.AT != "hello=" {
		t.Fatalf("csrf token not parsed: %q", m.AT)
	}
	if m.Email != "test@example.com" || m.Lang != "ja" {
		t.Fatalf("WIZ metadata incorrect: %+v", m)
	}
	if _, err := parseNotebookBootstrap([]byte("login required")); err == nil {
		t.Fatal("accepted missing WIZ globals")
	}
	if _, err := parseNotebookBootstrap([]byte(`<script>window.WIZ_global_data={"SNlM0e":"csrf"};</script>`)); err == nil {
		t.Fatal("accepted signed-out globals")
	}
}

func TestSpiderMonkeyBootstrapAndRPCEncoding(t *testing.T) {
	f := &fakeNotebookHTTP{}
	b := &browser{shared: &fakeSharedHTTPPage{}}
	m, err := b.bootstrap(context.Background(), f)
	if err != nil {
		t.Fatal(err)
	}
	if m.Email != "test@example.com" {
		t.Fatalf("login email %q", m.Email)
	}
	login, err := b.loginInfo(context.Background())
	if err == nil {
		var v map[string]any
		_ = json.Unmarshal(login, &v)
		if v["signedIn"] != true {
			t.Fatal("login not marked signed in")
		}
	} else {
		t.Fatal(err)
	}
	if f.getCount != 1 {
		t.Fatalf("duplicate HTTP bootstrap fetch: %d", f.getCount)
	}
	r, err := b.executeHTTP(context.Background(), f, "wXbhsf", []any{"hello", nil}, "/", "")
	if err != nil || r.Status != 200 {
		t.Fatalf("rpc %v %+v", err, r)
	}
	q, err := url.Parse(f.lastURL)
	if err != nil {
		t.Fatal(err)
	}
	if q.Host != "notebook.google.com" || q.Query().Get("rpcids") != "wXbhsf" || q.Query().Get("bl") != "bl-test" || q.Query().Get("f.sid") != "sid-test" {
		t.Fatalf("incorrect Notebook RPC url: %s", f.lastURL)
	}
	v, err := url.ParseQuery(f.lastBody)
	if err != nil {
		t.Fatal(err)
	}
	if v.Get("at") != "csrf-test" || !strings.Contains(v.Get("f.req"), "wXbhsf") {
		t.Fatalf("incorrect csrf or payload: %s", f.lastBody)
	}
	if f.getCount != 1 || f.postCount != 1 {
		t.Fatalf("unexpected calls GET=%d POST=%d", f.getCount, f.postCount)
	}
	// A 403 is retried only after fresh Notebook bootstrap.
	f.status = 403
	_, err = b.executeHTTP(context.Background(), f, "wXbhsf", []any{}, "/", "")
	if err != nil {
		t.Fatal(err)
	}
	if b.httpBootstrap != nil {
		t.Fatal("403 did not invalidate cached csrf")
	}
	f.status = 0
	_, err = b.executeHTTP(context.Background(), f, "wXbhsf", []any{}, "/", "")
	if err != nil || f.getCount != 2 {
		t.Fatalf("bootstrap was not refreshed: %v GET=%d", err, f.getCount)
	}
}

type fakeSharedHTTPPage struct{}

func (*fakeSharedHTTPPage) Evaluate(context.Context, string) (json.RawMessage, error) {
	return nil, errors.New("evaluate should not be used")
}
func (*fakeSharedHTTPPage) Navigate(context.Context, string) error { return nil }
func (*fakeSharedHTTPPage) Close() error                           { return nil }
func (*fakeSharedHTTPPage) Fetch(ctx context.Context, method, target string, h http.Header, b []byte) (HTTPResponse, error) {
	return (&fakeNotebookHTTP{}).Fetch(ctx, method, target, h, b)
}
