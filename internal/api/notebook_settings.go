package api

import (
	"encoding/json"
	"io"
	"net/http"

	"github.com/Mag1cFall/AIStudio2API/internal/notebook"
)

// Notebook account configuration belongs to the authenticated control mux,
// never the public OpenAI-compatible API routes.
func (s *server) handleNotebookSettings(w http.ResponseWriter, r *http.Request) {
	if s.config.Notebook == nil {
		writeOpenAIError(w, 503, "not_configured", "Notebook provider disabled")
		return
	}
	switch r.Method {
	case http.MethodGet:
		// No account credentials or Google tokens are included in settings.
	case http.MethodPut:
		var input struct {
			Concurrency *int `json:"concurrency"`
		}
		if err := json.NewDecoder(io.LimitReader(r.Body, 32<<10)).Decode(&input); err != nil {
			writeNotebookError(w, &notebook.APIError{Status: 400, Code: "invalid_request", Message: "Invalid Notebook settings JSON"})
			return
		}
		if input.Concurrency == nil {
			writeNotebookError(w, &notebook.APIError{Status: 400, Code: "invalid_request", Message: "Missing concurrency"})
			return
		}
		if err := s.config.Notebook.SetConcurrency(*input.Concurrency); err != nil {
			writeNotebookError(w, err)
			return
		}
	default:
		writeOpenAIError(w, 405, "invalid_method", "Unsupported Notebook settings action")
		return
	}
	writeJSON(w, 200, map[string]any{
		"concurrency":           s.config.Notebook.Concurrency(),
		"minimum":               notebook.MinConcurrency,
		"maximum":               notebook.MaxConcurrency,
		"queue_limit":           10,
		"queue_timeout_seconds": 30,
	})
}
