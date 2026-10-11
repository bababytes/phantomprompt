package protocol

import (
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"

	"phantomprompt/internal/mask"
)

type hangUpAfter struct {
	marker string
	seen   bool
	data   strings.Builder
}

func (w *hangUpAfter) Write(p []byte) (int, error) {
	if w.seen {
		return 0, errors.New("client closed the connection")
	}
	w.data.Write(p)
	w.seen = strings.Contains(w.data.String(), w.marker)
	return len(p), nil
}

// Clients such as Codex hang up as soon as the final event arrives; that is a complete answer.
func TestStreamsReportCompletionBeforeTheClientHangsUp(t *testing.T) {
	for _, tc := range []struct {
		name, marker, stream string
		restore              func(*strings.Reader, *hangUpAfter, *mask.Session) (StreamStats, error)
	}{
		{"anthropic", "message_stop", sse(
			[2]string{"message_start", `{"type":"message_start","message":{"usage":{"input_tokens":3}}}`},
			[2]string{"message_stop", `{"type":"message_stop"}`},
			[2]string{"ping", `{"type":"ping"}`},
		), func(r *strings.Reader, w *hangUpAfter, s *mask.Session) (StreamStats, error) {
			return RestoreAgentStream(r, w, s, 1<<20)
		}},
		{"responses", "response.completed", sse(
			[2]string{"response.output_text.delta", `{"type":"response.output_text.delta","item_id":"m","output_index":0,"content_index":0,"delta":"hi"}`},
			[2]string{"response.completed", `{"type":"response.completed","response":{"usage":{"input_tokens":3}}}`},
			[2]string{"keepalive", `{"type":"keepalive"}`},
		), func(r *strings.Reader, w *hangUpAfter, s *mask.Session) (StreamStats, error) {
			return RestoreOpenAIResponsesStream(r, w, s, 1<<20)
		}},
		{"chat", "[DONE]", sse(
			[2]string{"", `{"choices":[{"index":0,"delta":{"content":"hi"},"finish_reason":"stop"}]}`},
			[2]string{"", `[DONE]`},
			[2]string{"", `{"choices":[]}`},
		), func(r *strings.Reader, w *hangUpAfter, s *mask.Session) (StreamStats, error) {
			return RestoreOpenAIChatStream(r, w, s, 1<<20)
		}},
	} {
		stats, err := tc.restore(strings.NewReader(tc.stream), &hangUpAfter{marker: tc.marker}, agentSession())
		if err == nil || !stats.Completed {
			t.Fatalf("%s: err %v completed %v", tc.name, err, stats.Completed)
		}
		cut := tc.stream[:strings.Index(tc.stream, tc.marker)-10]
		if stats, _ := tc.restore(strings.NewReader(cut), &hangUpAfter{marker: "never"}, agentSession()); stats.Completed {
			t.Fatalf("%s: a stream cut before its final event was reported complete", tc.name)
		}
	}
}

type restorer func(io.Reader, io.Writer, *mask.Session, int) (StreamStats, error)

func completeStreams() map[string]struct {
	stream  string
	restore restorer
} {
	return map[string]struct {
		stream  string
		restore restorer
	}{
		"anthropic": {sse(
			[2]string{"message_start", `{"type":"message_start","message":{"usage":{"input_tokens":3}}}`},
			[2]string{"content_block_start", `{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`},
			[2]string{"content_block_delta", `{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"VIN ` + vin + ` ok"}}`},
			[2]string{"content_block_stop", `{"type":"content_block_stop","index":0}`},
			[2]string{"message_stop", `{"type":"message_stop"}`},
		), RestoreAgentStream},
		"responses": {sse(
			[2]string{"response.output_text.delta", `{"type":"response.output_text.delta","item_id":"m","output_index":0,"content_index":0,"delta":"VIN ` + vin + ` ok"}`},
			[2]string{"response.completed", `{"type":"response.completed","response":{"usage":{"input_tokens":3}}}`},
		), RestoreOpenAIResponsesStream},
		"chat": {sse(
			[2]string{"", `{"choices":[{"index":0,"delta":{"content":"VIN ` + vin + ` ok"},"finish_reason":"stop"}]}`},
			[2]string{"", `[DONE]`},
		), RestoreOpenAIChatStream},
	}
}

func TestStreamsEndedBeforeTheirFinalEventAreErrors(t *testing.T) {
	for name, tc := range completeStreams() {
		final := strings.LastIndex(strings.TrimRight(tc.stream, "\n"), "\n\n") + 2
		for cut := 0; cut < len(tc.stream); cut++ {
			stats, err := tc.restore(strings.NewReader(tc.stream[:cut]), io.Discard, agentSession(), 1<<20)
			complete := cut >= len(strings.TrimRight(tc.stream, "\n"))
			if complete && (err != nil || !stats.Completed) {
				t.Fatalf("%s: a final event without a trailing blank line was not accepted: err %v", name, err)
			}
			if !complete && (err == nil || stats.Completed) {
				t.Fatalf("%s: stream cut at %d of %d (final event at %d) reported err=%v completed=%v", name, cut, len(tc.stream), final, err, stats.Completed)
			}
		}
	}
}

func TestStreamsKeepAPendingTokenWhenCut(t *testing.T) {
	s := agentSession()
	masked, _, err := s.Mask([]string{vin})
	if err != nil {
		t.Fatal(err)
	}
	stream := sse(
		[2]string{"content_block_start", `{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`},
		[2]string{"content_block_delta", `{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"VIN ` + masked[0][:8] + `"}}`},
		[2]string{"content_block_delta", `{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"` + masked[0][8:] + `"}}`},
	)
	var out strings.Builder
	if _, err := RestoreAgentStream(strings.NewReader(stream), &out, s, 1<<20); err == nil || !strings.Contains(out.String(), vin) {
		t.Fatalf("a token completed just before the stream broke was lost: err %v", err)
	}
}

func TestSSEEventSizeIsLimitedAcrossLines(t *testing.T) {
	line := "data: " + strings.Repeat("x", 90) + "\n"
	for name, tc := range completeStreams() {
		for _, lines := range []int{10, 11, 12} {
			stream := "event: message\n" + strings.Repeat(line, lines) + "\n" + tc.stream
			_, err := tc.restore(strings.NewReader(stream), io.Discard, agentSession(), 1024)
			if tooLarge := lines*len(line) > 1024; tooLarge != errors.Is(err, ErrFormat) {
				t.Fatalf("%s: %d data lines of %d bytes, limit 1024: err %v", name, lines, len(line), err)
			}
		}
	}
}

func TestSSEHeartbeatsAndCRLF(t *testing.T) {
	for name, tc := range completeStreams() {
		stream := ": ping\r\n\r\n" + strings.ReplaceAll(tc.stream, "\n", "\r\n")
		var out strings.Builder
		stats, err := tc.restore(strings.NewReader(stream), &out, agentSession(), 1<<20)
		if err != nil || !stats.Completed || !strings.HasPrefix(out.String(), ":") || !strings.Contains(out.String(), vin) {
			t.Fatalf("%s: err %v completed %v output starts %q", name, err, stats.Completed, out.String()[:min(20, out.Len())])
		}
	}
}

func TestSSEOpenStatesAreLimited(t *testing.T) {
	var b strings.Builder
	for i := range 2000 {
		fmt.Fprintf(&b, "event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":%d,\"delta\":{\"type\":\"text_delta\",\"text\":\"[\"}}\n\n", i)
	}
	if _, err := RestoreAgentStream(strings.NewReader(b.String()), io.Discard, agentSession(), 1<<20); !errors.Is(err, ErrFormat) {
		t.Fatalf("2000 open content blocks: err %v", err)
	}
}

func TestMalformedSuccessfulRepliesAreErrors(t *testing.T) {
	s := agentSession()
	for _, body := range []string{`{"content":[`, `not json`, `[1,2]`, `{"a":1} {"b":2}`} {
		if _, _, err := RestoreAgentMessages([]byte(body), s); err == nil {
			t.Fatalf("anthropic accepted %q", body)
		}
		if _, _, err := RestoreOpenAI([]byte(body), s); err == nil && body != `[1,2]` {
			t.Fatalf("openai accepted %q", body)
		}
	}
	if out, _, err := RestoreAgentMessages([]byte(`{"input_tokens":12}`), s); err != nil || string(out) != `{"input_tokens":12}` {
		t.Fatalf("a token count reply was changed: %s %v", out, err)
	}
}
