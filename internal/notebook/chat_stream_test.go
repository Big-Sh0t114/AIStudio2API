package notebook

import (
	"encoding/json"
	"strings"
	"testing"
)

func notebookTestFrame(text string, marker any, final bool) []byte {
	row := []any{text, nil, nil, nil, []any{nil, nil, nil, nil, marker}}
	inner, _ := json.Marshal([]any{row, nil, nil, nil, final})
	outer, _ := json.Marshal([]any{[]any{"wrb.fr", "", string(inner), nil, nil, nil}})
	return append(outer, '\n')
}

func TestNotebookStreamEmitsCumulativeDeltasBeforeCompletion(t *testing.T) {
	var deltas []string
	s := notebookChatStream{emit: func(text string) error { deltas = append(deltas, text); return nil }}
	first := notebookTestFrame("你好", 1, false)
	split := strings.Index(string(first), "你") + 1
	for _, chunk := range [][]byte{[]byte(")]}'\n24\n"), first[:split], first[split:]} {
		if err := s.feed(chunk); err != nil {
			t.Fatal(err)
		}
	}
	if len(deltas) != 1 || deltas[0] != "你好" {
		t.Fatalf("no early delta: %#v", deltas)
	}
	for _, frame := range [][]byte{notebookTestFrame("secret thinking", 2, false), notebookTestFrame("你好世界", 1, false), notebookTestFrame("你好世界！", 1, true)} {
		if err := s.feed(frame); err != nil {
			t.Fatal(err)
		}
	}
	out, err := s.finish()
	if err != nil || out.Text != "你好世界！" || strings.Join(deltas, "") != out.Text || len(deltas) != 3 {
		t.Fatalf("wrong cumulative output: %#v %v %#v", out, err, deltas)
	}
}

func TestNotebookStreamRejectsRevisedFinal(t *testing.T) {
	s := notebookChatStream{emit: func(string) error { return nil }}
	if err := s.feed(notebookTestFrame("first", 1, false)); err != nil {
		t.Fatal(err)
	}
	if err := s.feed(notebookTestFrame("different", 1, true)); err == nil {
		t.Fatal("revision silently accepted")
	}
}

func TestNotebookStreamLegacyAndTruncation(t *testing.T) {
	var output string
	s := notebookChatStream{emit: func(text string) error { output += text; return nil }}
	if err := s.feed(notebookTestFrame("legacy partial", nil, false)); err != nil {
		t.Fatal(err)
	}
	if output != "" {
		t.Fatal("unmarked partial was exposed")
	}
	if err := s.feed(notebookTestFrame("legacy final", nil, true)); err != nil {
		t.Fatal(err)
	}
	out, err := s.finish()
	if err != nil || out.Text != "legacy final" || output != out.Text {
		t.Fatalf("legacy output: %v %v %q", out, err, output)
	}
	s = notebookChatStream{emit: func(string) error { return nil }}
	if err := s.feed(notebookTestFrame("partial", 1, false)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.finish(); err == nil {
		t.Fatal("truncated stream reported as success")
	}
}
