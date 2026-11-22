package api

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/Mag1cFall/AIStudio2API/internal/notebook"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"time"
)

func notebookModelInBody(r *http.Request) bool {
	if r.Body == nil {
		return false
	}
	original := r.Body
	prefix, _ := io.ReadAll(io.LimitReader(original, 64<<10))
	r.Body = struct {
		io.Reader
		io.Closer
	}{Reader: io.MultiReader(bytes.NewReader(prefix), original), Closer: original}
	var meta struct {
		Model string `json:"model"`
	}
	if json.Unmarshal(prefix, &meta) == nil {
		return strings.HasPrefix(meta.Model, "notebook:")
	}
	return bytes.Contains(bytes.ReplaceAll(prefix, []byte(" "), nil), []byte(`"model":"notebook:`))
}
func notebookTextContent(raw json.RawMessage) (string, error) {
	var text string
	if json.Unmarshal(raw, &text) == nil {
		return text, nil
	}
	var parts []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if json.Unmarshal(raw, &parts) != nil || len(parts) == 0 {
		return "", fmt.Errorf("Notebook supports text-only message content")
	}
	var lines []string
	for _, part := range parts {
		if part.Type != "text" || part.Text == "" {
			return "", fmt.Errorf("Notebook does not support non-text content parts")
		}
		lines = append(lines, part.Text)
	}
	return strings.Join(lines, "\n"), nil
}
func (s *server) notebookAuthorized(r *http.Request, model string) bool {
	if s.config.Notebook == nil {
		return false
	}
	email, ok := s.config.Notebook.EmailForModel(model)
	if !ok {
		return false
	}
	if s.config.NotebookAuthorized == nil {
		return true
	}
	var apiKey string
	keys := apiKeys(s.config.APIKey)
	for _, candidate := range apiKeyCandidates(r) {
		for _, configured := range keys {
			if candidate == configured {
				apiKey = candidate
				break
			}
		}
		if apiKey != "" {
			break
		}
	}
	return s.config.NotebookAuthorized(apiKey, email)
}
func (s *server) handleNotebookOrChat(w http.ResponseWriter, r *http.Request) {
	if s.config.Notebook == nil || !notebookModelInBody(r) {
		s.handleChatCompletions(w, r)
		return
	}
	var input struct {
		Model    string `json:"model"`
		Stream   bool   `json:"stream"`
		Messages []struct {
			Role    string          `json:"role"`
			Content json.RawMessage `json:"content"`
		} `json:"messages"`
		Tools []json.RawMessage `json:"tools"`
		N     *int              `json:"n"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 8<<20)).Decode(&input); err != nil {
		writeOpenAIError(w, 400, "invalid_request", err.Error())
		return
	}
	if !s.notebookAuthorized(r, input.Model) {
		writeOpenAIError(w, 403, "permission_denied", "Notebook account is not accessible with this API key, or login needs refreshing")
		return
	}
	if input.Stream {
		writeOpenAIError(w, 400, "unsupported_feature", "Notebook backend supports only stream=false")
		return
	}
	if len(input.Tools) > 0 {
		writeOpenAIError(w, 400, "unsupported_feature", "Notebook does not support OpenAI function tools")
		return
	}
	if input.N != nil && *input.N != 1 {
		writeOpenAIError(w, 400, "unsupported_feature", "Notebook supports only n=1")
		return
	}
	messages := make([]notebook.Message, 0, len(input.Messages))
	for _, msg := range input.Messages {
		content, err := notebookTextContent(msg.Content)
		if err != nil {
			writeOpenAIError(w, 400, "unsupported_content", err.Error())
			return
		}
		messages = append(messages, notebook.Message{Role: msg.Role, Content: content})
	}
	ans, err := s.config.Notebook.Chat(r.Context(), input.Model, messages)
	if err != nil {
		writeNotebookError(w, err)
		return
	}
	content := ans.Text
	if len(ans.Citations) > 0 {
		content += "\n\nSources:"
		for _, c := range ans.Citations {
			name := c.SourceTitle
			if name == "" {
				name = c.SourceID
			}
			content += fmt.Sprintf("\n[%d] %s", c.Number, name)
		}
	}
	writeJSON(w, 200, map[string]any{
		"id": newID("chatcmpl"), "object": "chat.completion", "created": time.Now().Unix(), "model": input.Model,
		"choices":   []any{map[string]any{"index": 0, "message": map[string]any{"role": "assistant", "content": content}, "finish_reason": "stop"}},
		"citations": ans.Citations,
	})
}
func writeNotebookError(w http.ResponseWriter, err error) {
	status := 502
	code := "upstream_error"
	var api *notebook.APIError
	if errors.As(err, &api) {
		if api.Status > 0 {
			status = api.Status
		}
		if api.Code != "" {
			code = api.Code
		}
		if status == 429 && api.RetryAt.After(time.Now()) {
			w.Header().Set("Retry-After", strconv.Itoa(max(1, int(time.Until(api.RetryAt).Seconds()))))
		}
	}
	writeOpenAIError(w, status, code, err.Error())
}
func (s *server) handleNotebookOrModels(w http.ResponseWriter, r *http.Request) {
	if s.config.Notebook == nil {
		s.handleOpenAIModels(w, r)
		return
	}
	rec := httptest.NewRecorder()
	s.handleOpenAIModels(rec, r)
	var body struct {
		Object string           `json:"object"`
		Data   []map[string]any `json:"data"`
	}
	if rec.Code >= 200 && rec.Code < 300 {
		_ = json.Unmarshal(rec.Body.Bytes(), &body)
	}
	if body.Data == nil {
		body.Data = []map[string]any{}
	}
	for _, m := range s.config.Notebook.Models() {
		if !s.notebookAuthorized(r, m.ID) {
			continue
		}
		body.Data = append(body.Data, map[string]any{"id": m.ID, "name": m.Name, "object": "model", "owned_by": "notebooklm", "supported_generation_methods": []string{"generateContent"}})
	}
	if len(body.Data) == 0 && rec.Code >= 400 {
		w.WriteHeader(rec.Code)
		_, _ = w.Write(rec.Body.Bytes())
		return
	}
	writeJSON(w, 200, map[string]any{"object": "list", "data": body.Data})
}
func (s *server) handleNotebookOrModel(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("model")
	if s.config.Notebook != nil && strings.HasPrefix(id, "notebook:") {
		for _, m := range s.config.Notebook.Models() {
			if m.ID == id && s.notebookAuthorized(r, id) {
				writeJSON(w, 200, map[string]any{"id": m.ID, "name": m.Name, "object": "model", "owned_by": "notebooklm"})
				return
			}
		}
		writeOpenAIError(w, 404, "model_not_found", "Notebook not registered or account needs refresh")
		return
	}
	s.handleOpenAIModel(w, r)
}
func (s *server) handleNotebookAccounts(w http.ResponseWriter, r *http.Request) {
	if s.config.Notebook == nil {
		writeOpenAIError(w, 503, "not_configured", "Notebook provider disabled")
		return
	}
	switch r.Method {
	case http.MethodGet:
		writeJSON(w, 200, map[string]any{"accounts": s.config.Notebook.Accounts()})
	case http.MethodPost:
		var input struct {
			Label           string `json:"label"`
			SourceAccountID string `json:"source_account_id"`
		}
		if err := json.NewDecoder(io.LimitReader(r.Body, 32<<10)).Decode(&input); err != nil && err != io.EOF {
			writeNotebookError(w, &notebook.APIError{Status: 400, Code: "invalid_request", Message: err.Error()})
			return
		}
		account, created, err := s.config.Notebook.CreateWithSourceResult(r.Context(), input.Label, input.SourceAccountID)
		if err != nil {
			writeNotebookError(w, err)
			return
		}
		if created {
			writeJSON(w, 201, map[string]any{"account": account, "created": true})
		} else {
			writeJSON(w, 200, map[string]any{"account": account, "created": false})
		}
	}
}
func (s *server) handleNotebookAccountAction(w http.ResponseWriter, r *http.Request) {
	if s.config.Notebook == nil {
		writeOpenAIError(w, 503, "not_configured", "Notebook provider disabled")
		return
	}
	id := r.PathValue("account")
	switch {
	case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/login"):
		if err := s.config.Notebook.Login(r.Context(), id); err != nil {
			writeNotebookError(w, err)
			return
		}
		writeJSON(w, 200, map[string]any{"ok": true, "message": "Complete sign-in in the Chrome window, then click Refresh"})
	case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/refresh"):
		account, err := s.config.Notebook.Refresh(r.Context(), id)
		if err != nil {
			writeNotebookError(w, err)
			return
		}
		writeJSON(w, 200, map[string]any{"account": account})
	case r.Method == http.MethodDelete:
		if err := s.config.Notebook.Remove(id); err != nil {
			writeNotebookError(w, err)
			return
		}
		writeJSON(w, 200, map[string]any{"ok": true})
	default:
		writeOpenAIError(w, 405, "invalid_method", "Unsupported Notebook action")
	}
}

func (s *server) handleNotebookSources(w http.ResponseWriter, r *http.Request) {
	if s.config.Notebook == nil {
		writeOpenAIError(w, 503, "not_configured", "Notebook provider disabled")
		return
	}
	sources, err := s.config.Notebook.Sources(r.Context())
	if err != nil {
		writeNotebookError(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"sources": sources})
}
