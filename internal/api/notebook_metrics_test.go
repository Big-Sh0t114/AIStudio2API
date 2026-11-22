package api

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/Mag1cFall/AIStudio2API/internal/notebook"
)

func TestNotebookTextOnlyUsageIsExplicitEstimate(t *testing.T) {
	msgs := []notebook.Message{
		{Role: "system", Content: "Answer briefly."},
		{Role: "user", Content: "请说明NotebookLM"},
	}
	usage := notebookTextUsage(msgs, "This is an answer.")
	if !usage.Estimated {
		t.Fatal("Notebook's unverified tokens were not marked as estimated")
	}
	if usage.InputTokens == 0 || usage.OutputTokens == 0 ||
		usage.TotalTokens != usage.InputTokens+usage.OutputTokens {
		t.Fatalf("invalid text-only token estimate: %+v", usage)
	}
	if usage.CachedTokens != 0 || usage.ReasoningTokens != 0 {
		t.Fatal("NotebookLM source and reasoning tokens must not be invented")
	}
	if notebookTextTokens("") != 0 {
		t.Fatal("empty string must have no counted text tokens")
	}
	if notebookTextTokens("中文") != 2 {
		t.Fatalf("Han text heuristic changed: %d", notebookTextTokens("中文"))
	}
}

func TestNotebookRequestAccessLogContainsModelAndEstimatedTokens(t *testing.T) {
	metadata := &accessLogMetadata{
		method: "POST", path: "/v1/chat/completions",
		startedAt: time.Now(), requestID: "request-test",
	}
	ctx := context.WithValue(context.Background(), accessLogContextKey{}, metadata)
	SetAccessLogTarget(ctx, "notebook:account-1:document-2", "notebook:account-1")
	SetAccessLogChannel(ctx, "notebook")
	SetAccessLogTextInput(ctx, 2, 12)
	usage := notebookTextUsage([]notebook.Message{{Role: "user", Content: "Hello"}}, "world")
	SetAccessLogGenerationResult(ctx, usage, 0)
	SetAccessLogFinishReason(ctx, "stop")
	access := metadata.finished(200, false)
	if !access.Generation || access.Model != "notebook:account-1:document-2" ||
		access.Account != "notebook:account-1" || access.Channel != "notebook" ||
		access.InputMessages != 2 || access.InputTextChars != 12 ||
		access.Usage == nil || !access.Usage.Estimated ||
		access.Usage.TotalTokens == 0 || access.FinishReason != "stop" {
		t.Fatalf("Notebook metadata not captured: %+v", access)
	}
}

func TestNotebookEstimatorIsNotMistakenForProviderReporting(t *testing.T) {
	prompt := strings.Repeat("A", 50)
	output := "简短答案"
	u := notebookTextUsage([]notebook.Message{{Role: "user", Content: prompt}}, output)
	if !u.Estimated || u.TotalTokens != notebookTextTokens(prompt)+notebookTextTokens(output) {
		t.Fatalf("estimate accounting differs from text heuristic: %+v", u)
	}
}
