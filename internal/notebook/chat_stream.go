package notebook

import (
	"bytes"
	"encoding/json"
	"strings"
)

const notebookStreamMaxBytes = 32 << 20

type notebookChatStream struct {
	pending     []byte
	total       int
	emit        func(string) error
	titles      map[string]string
	sent        string
	marked      []any
	final       []any
	legacy      []any
	legacyFinal []any
	sawFinal    bool
}

func notebookStreamError(code, message string) error {
	return &APIError{Status: 502, Code: code, Message: message}
}

func (s *notebookChatStream) feed(data []byte) error {
	s.total += len(data)
	if s.total > notebookStreamMaxBytes {
		return notebookStreamError("upstream_response_too_large", "Notebook stream exceeds size limit")
	}
	s.pending = append(s.pending, data...)
	for {
		pos := bytes.IndexByte(s.pending, '\n')
		if pos < 0 {
			return nil
		}
		line := s.pending[:pos]
		s.pending = s.pending[pos+1:]
		if err := s.line(line); err != nil {
			return err
		}
		if len(s.pending) == 0 {
			s.pending = nil
			return nil
		}
	}
}

func (s *notebookChatStream) line(line []byte) error {
	for _, frame := range parseFrames(string(line)) {
		switch str(at(frame, 0)) {
		case "er":
			return parseErr([]any{at(frame, 2)}, "chat")
		case "wrb.fr":
		default:
			continue
		}
		if at(frame, 2) == nil {
			if status := at(frame, 5); status != nil {
				return parseErr(status, "chat")
			}
			continue
		}
		var v any
		if err := json.Unmarshal([]byte(str(at(frame, 2))), &v); err != nil {
			continue
		}
		block := array(v)
		final := at(block, 4) == true
		if final {
			s.sawFinal = true
		}
		answer := array(at(block, 0))
		text := str(at(answer, 0))
		if text == "" {
			continue
		}
		// AnswerResponse.responseDoc at [4] contains an answer marker
		// at [4][4]: 1 is displayable answer, 2 is thought-only content.
		marker := at(answer, 4, 4)
		if number(marker) == 2 {
			continue
		}
		if number(marker) == 1 {
			if final {
				s.final = block
			}
			if s.marked == nil || len(text) > len(str(at(s.marked, 0, 0))) {
				s.marked = block
			}
			if strings.HasPrefix(text, s.sent) {
				if err := s.send(text); err != nil {
					return err
				}
			} else if final {
				return notebookStreamError("notebook_stream_revised", "Notebook final answer revised previously emitted text")
			}
			continue
		}
		// Unmarked legacy responses are withheld until the final frame.
		// Never emit partial text from an unknown marker.
		if marker != nil {
			continue
		}
		if final {
			s.legacyFinal = block
		}
		if s.legacy == nil || len(text) > len(str(at(s.legacy, 0, 0))) {
			s.legacy = block
		}
	}
	return nil
}

func (s *notebookChatStream) send(text string) error {
	if !strings.HasPrefix(text, s.sent) {
		return notebookStreamError("notebook_stream_revised", "Notebook final answer revised previously emitted text")
	}
	delta := text[len(s.sent):]
	if delta != "" {
		if err := s.emit(delta); err != nil {
			return err
		}
		s.sent = text
	}
	return nil
}

func (s *notebookChatStream) finish() (Answer, error) {
	if len(s.pending) > 0 {
		if err := s.line(s.pending); err != nil {
			return Answer{}, err
		}
		s.pending = nil
	}
	if !s.sawFinal {
		return Answer{}, notebookStreamError("incomplete_stream", "Notebook stream ended without a final response")
	}
	chosen := s.final
	if chosen == nil {
		chosen = s.marked
	}
	if chosen == nil {
		chosen = s.legacyFinal
	}
	if chosen == nil {
		chosen = s.legacy
	}
	ans := at(chosen, 0)
	reply := str(at(ans, 0))
	if strings.TrimSpace(reply) == "" {
		return Answer{}, notebookStreamError("empty_response", "Notebook returned no answer")
	}
	if err := s.send(reply); err != nil {
		return Answer{}, err
	}
	out := Answer{Text: reply, Citations: []Citation{}}
	for i, c := range array(at(ans, 4, 3)) {
		sid := str(at(c, 1, 5, 0, 0, 0))
		out.Citations = append(out.Citations, Citation{Number: i + 1, SourceID: sid, SourceTitle: s.titles[sid]})
	}
	return out, nil
}
