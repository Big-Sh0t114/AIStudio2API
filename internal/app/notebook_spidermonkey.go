package app

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
    "strings"

	"github.com/Mag1cFall/AIStudio2API/internal/aistudio"
	"github.com/Mag1cFall/AIStudio2API/internal/notebook"
)

// spiderNotebookPage exposes Notebook's Go HTTP transport through the same
// lightweight session interface as the Camoufox browser adapter. SpiderMonkey
// is a WAA JavaScript VM, not a tab-capable browser, so it must not attempt
// to run Notebook's browser-side JavaScript.
type spiderNotebookPage struct {
	worker *aistudio.NativeWorker
}

func (p *spiderNotebookPage) Evaluate(context.Context, string) (json.RawMessage, error) {
	return nil, errors.New("SpiderMonkey Notebook uses direct Go HTTP, not browser JavaScript")
}

func (p *spiderNotebookPage) Navigate(_ context.Context, target string) error {
	if target != "https://notebook.google.com/" {
		return errors.New("SpiderMonkey Notebook navigation is restricted")
	}
	// The Notebook adapter invalidates its own bootstrap metadata when
	// refreshing. No browser navigation is necessary for the Go HTTP runtime.
	return nil
}

func (p *spiderNotebookPage) Close() error { return nil }

func (p *spiderNotebookPage) Fetch(ctx context.Context, method, target string, headers http.Header, body []byte) (notebook.HTTPResponse, error) {
	var status int
    var result []byte
    var err error
    if strings.HasPrefix(target, "https://gemini.google.com/") {
        status, result, err = p.worker.GeminiWebHTTP(ctx, method, target, headers, body)
        if errors.Is(err, aistudio.ErrGeminiWebAuthentication) {
            return notebook.HTTPResponse{}, &notebook.APIError{
                Status: 401, Code: "auth_required",
                Message: "Gemini Web authentication expired for the linked Playground account",
            }
        }
    } else {
        status, result, err = p.worker.NotebookHTTP(ctx, method, target, headers, body)
    }
	if errors.Is(err, aistudio.ErrNotebookAuthentication) {
		return notebook.HTTPResponse{}, &notebook.APIError{
			Status: 401, Code: "auth_required",
			Message: "Google could not complete NotebookLM sign-in with this Playground session after attempting cookie renewal; refresh the Google account authentication or use standalone Notebook Chrome login",
		}
	}
	if errors.Is(err, aistudio.ErrNotebookRedirect) {
		return notebook.HTTPResponse{}, &notebook.APIError{
			Status: 502, Code: "notebook_redirect",
			Message: err.Error(),
		}
	}
	if err != nil {
		return notebook.HTTPResponse{}, err
	}
	return notebook.HTTPResponse{Status: status, Body: result}, nil
}

// The existing notebookSharedPage retains the Playground worker's lifecycle
// lease. Only this wrapper exposes the optional HTTPPage interface, so
// Camoufox pages remain routed to browser-side RPC evaluation unchanged.
type notebookSharedHTTPPage struct{ *notebookSharedPage }

func (p *notebookSharedHTTPPage) Fetch(ctx context.Context, method, target string, headers http.Header, body []byte) (notebook.HTTPResponse, error) {
	return p.SharedPage.(notebook.HTTPPage).Fetch(ctx, method, target, headers, body)
}
