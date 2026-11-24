package aistudio

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"
)

type notebookTestStreamReader struct {
	read           int
	callbackCalled *bool
}

func (r *notebookTestStreamReader) Read(p []byte) (int, error) {
	r.read++
	if r.read == 1 {
		return copy(p, "first"), nil
	}
	if !*r.callbackCalled {
		return 0, errors.New("response buffered before callback")
	}
	if r.read == 2 {
		return copy(p, " second"), nil
	}
	return 0, io.EOF
}

func TestNotebookHTTPReadsIncrementally(t *testing.T) {
	called := false
	var out strings.Builder
	r := &notebookTestStreamReader{callbackCalled: &called}
	err := readNotebookHTTPStream(context.Background(), r, func(chunk []byte) error { called = true; out.Write(chunk); return nil })
	if err != nil || out.String() != "first second" {
		t.Fatalf("stream: %q %v", out.String(), err)
	}
}
