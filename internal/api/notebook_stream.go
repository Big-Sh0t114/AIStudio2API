package api

import (
	"fmt"
	"net/http"
	"time"

	"github.com/Mag1cFall/AIStudio2API/internal/notebook"
)

func notebookCitationSuffix(citations []notebook.Citation) string {
	if len(citations) == 0 {
		return ""
	}
	content := "\n\nSources:"
	for _, c := range citations {
		name := c.SourceTitle
		if name == "" {
			name = c.SourceID
		}
		content += fmt.Sprintf("\n[%d] %s", c.Number, name)
	}
	return content
}

func (s *server) streamNotebookChat(w http.ResponseWriter, r *http.Request, model string, messages []notebook.Message, includeUsage bool) {
	id, created := newID("chatcmpl"), time.Now().Unix()
	var stream *streamKeepalive
	defer func() {
		if stream != nil {
			stream.stop()
		}
	}()
	emit := func(delta string) error {
		if stream == nil {
			w.Header().Set("X-Token-Usage-Estimated", "true")
			var err error
			stream, err = openStream(w, sseHeartbeat)
			if err != nil {
				return err
			}
			if err := writeChatChunk(stream, id, created, model, 0, map[string]any{"role": "assistant", "content": ""}, nil, includeUsage); err != nil {
				return err
			}
		}
		return writeChatChunk(stream, id, created, model, 0, map[string]any{"content": delta}, nil, includeUsage)
	}
	ans, err := s.config.Notebook.StreamChat(r.Context(), model, messages, emit)
	if err != nil {
		SetAccessLogError(r.Context(), err)
		if r.Context().Err() != nil {
			return
		}
		if stream == nil {
			writeNotebookError(w, err)
		} else {
			writeOpenAISSEError(stream, err)
		}
		return
	}
	suffix := notebookCitationSuffix(ans.Citations)
	if suffix != "" {
		if err := emit(suffix); err != nil {
			SetAccessLogError(r.Context(), err)
			return
		}
	}
	usage := notebookTextUsage(messages, ans.Text+suffix)
	SetAccessLogGenerationResult(r.Context(), usage, 0)
	SetAccessLogFinishReason(r.Context(), "stop")
	finish := "stop"
	if err := writeChatChunk(stream, id, created, model, 0, map[string]any{}, &finish, includeUsage); err != nil {
		return
	}
	if includeUsage {
		chunk := map[string]any{
			"id": id, "object": "chat.completion.chunk", "created": created, "model": model, "choices": []any{},
			"usage": map[string]any{
				"prompt_tokens": usage.InputTokens, "completion_tokens": usage.OutputTokens, "total_tokens": usage.TotalTokens,
				"estimated": true, "estimation_method": "text_only_heuristic",
			},
		}
		if err := writeSSE(stream, "", officialOrder("chat", chunk)); err != nil {
			return
		}
	}
	_ = writeSSEText(stream, "[DONE]")
}
