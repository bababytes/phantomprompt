package protocol

import (
	"bytes"
	"encoding/json"
	"io"
	"strings"
	"testing"

	"phantomprompt/internal/mask"
)

const vin = "TEST0000000000001"

func session() *mask.Session { return mask.NewSession(100, 16384, mask.DefaultPolicy()) }

func TestRequestAdapters(t *testing.T) {
	for _, tc := range []struct{ kind, input string }{
		{Demo, `{"text":"` + vin + `"}`},
		{OpenAI, `{"model":"test-model","messages":[{"role":"user","content":"` + vin + `"}],"stream":true}`},
		{Anthropic, `{"model":"test-model","max_tokens":100,"system":"` + vin + `","messages":[{"role":"user","content":[{"type":"text","text":"` + vin + `"}]}]}`},
	} {
		t.Run(tc.kind, func(t *testing.T) {
			out, counts, _, err := MaskRequest(tc.kind, []byte(tc.input), session())
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(string(out), vin) || counts.VIN == 0 {
				t.Fatal("original VIN escaped adapter")
			}
			if !json.Valid(out) {
				t.Fatal("invalid output JSON")
			}
		})
	}
}

func TestRejectUnsupportedBeforeMasking(t *testing.T) {
	for _, tc := range []struct{ kind, input string }{
		{Demo, `{"text":"hello","image":"secret"}`},
		{Demo, `{"text":123}`},
		{Demo, `{"text":"hello"} {"text":"extra"}`},
		{OpenAI, `{"model":"x","messages":[{"role":"user","content":"hello"}],"tools":[]}`},
		{OpenAI, `{"model":"x","messages":[{"role":"user","content":[{"type":"image_url","image_url":"secret"}]}]}`},
		{Anthropic, `{"model":"x","max_tokens":100,"messages":[{"role":"user","content":[{"type":"tool_result","text":"secret"}]}]}`},
		{OpenAI, `{"model":"x","messages":[{"role":"user","content":null}]}`},
	} {
		if _, _, _, err := MaskRequest(tc.kind, []byte(tc.input), session()); err == nil {
			t.Errorf("unsupported request accepted for %s", tc.kind)
		}
	}
}

func TestJSONEscapingAndExactNumbers(t *testing.T) {
	s := session()
	input := `{"text":"\u0054EST0000000000001\n\"quoted\""}`
	out, _, _, err := MaskRequest(Demo, []byte(input), s)
	if err != nil {
		t.Fatal(err)
	}
	var request DemoRequest
	json.Unmarshal(out, &request)
	if request.Text == "TEST0000000000001\n\"quoted\"" {
		t.Fatal("escaped VIN was not masked")
	}
	response := `{"id":"test","created":9007199254740993,"choices":[{"index":0,"message":{"role":"assistant","content":` + string(mustJSON(t, request.Text)) + `},"finish_reason":"stop"}]}`
	result, err := RestoreResponse(OpenAI, []byte(response), s)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(result, []byte("9007199254740993")) {
		t.Fatal("large integer precision lost")
	}
	if !bytes.Contains(result, []byte(vin)) {
		t.Fatal("VIN not restored")
	}
}

func mustJSON(t *testing.T, value any) []byte {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func TestDemoSSEAcrossEveryByte(t *testing.T) {
	s := session()
	masked, _, _ := s.Mask([]string{vin})
	var wire bytes.Buffer
	WriteEvent(&wire, Event{"receipt", string(mustJSON(t, map[string]string{"text": masked[0]}))})
	for _, r := range masked[0] {
		WriteEvent(&wire, Event{"delta", string(mustJSON(t, map[string]string{"text": string(r)}))})
	}
	WriteEvent(&wire, Event{"done", `{}`})
	var out bytes.Buffer
	if err := TransformSSE(Demo, &oneByteReader{reader: bytes.NewReader(wire.Bytes())}, &out, s, 4096); err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(out.Bytes(), []byte(vin)) {
		t.Fatal("split token not restored")
	}
	if !bytes.Contains(out.Bytes(), []byte(masked[0])) {
		t.Fatal("actual upstream receipt was not preserved")
	}
}

type oneByteReader struct{ reader io.Reader }

func (r *oneByteReader) Read(b []byte) (int, error) { return r.reader.Read(b[:min(1, len(b))]) }

func TestOpenAIStreamingFinishesPendingTokens(t *testing.T) {
	s := session()
	masked, _, _ := s.Mask([]string{vin})
	d := NewStreamDecoder(OpenAI, s)
	var text strings.Builder
	for i, part := range []string{masked[0][:12], masked[0][12:]} {
		finish := any(nil)
		if i == 1 {
			finish = "stop"
		}
		wire := mustJSON(t, map[string]any{"choices": []any{map[string]any{"index": 0, "delta": map[string]string{"content": part}, "finish_reason": finish}}})
		out, err := d.Transform(Event{Data: string(wire)})
		if err != nil {
			t.Fatal(err)
		}
		var decoded struct {
			Choices []struct {
				Delta struct {
					Content string `json:"content"`
				} `json:"delta"`
			} `json:"choices"`
		}
		json.Unmarshal([]byte(out[0].Data), &decoded)
		text.WriteString(decoded.Choices[0].Delta.Content)
	}
	if text.String() != vin {
		t.Fatal("incorrect streaming content")
	}
	if _, err := d.Transform(Event{Data: "[DONE]"}); err != nil {
		t.Fatal(err)
	}
}

func TestAnthropicStreamingAndToolRejection(t *testing.T) {
	s := session()
	masked, _, _ := s.Mask([]string{vin})
	d := NewStreamDecoder(Anthropic, s)
	start := `{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`
	if _, err := d.Transform(Event{"content_block_start", start}); err != nil {
		t.Fatal(err)
	}
	var text strings.Builder
	for _, part := range []string{masked[0][:8], masked[0][8:]} {
		wire := mustJSON(t, map[string]any{"type": "content_block_delta", "index": 0, "delta": map[string]string{"type": "text_delta", "text": part}})
		out, err := d.Transform(Event{"content_block_delta", string(wire)})
		if err != nil {
			t.Fatal(err)
		}
		var decoded struct {
			Delta struct {
				Text string `json:"text"`
			} `json:"delta"`
		}
		json.Unmarshal([]byte(out[0].Data), &decoded)
		text.WriteString(decoded.Delta.Text)
	}
	if text.String() != vin {
		t.Fatal("Anthropic token not restored")
	}
	if _, err := d.Transform(Event{"content_block_stop", `{"type":"content_block_stop","index":0}`}); err != nil {
		t.Fatal(err)
	}
	if _, err := d.Transform(Event{"message_stop", `{"type":"message_stop"}`}); err != nil {
		t.Fatal(err)
	}
	d = NewStreamDecoder(Anthropic, s)
	if _, err := d.Transform(Event{"content_block_start", `{"type":"content_block_start","index":0,"content_block":{"type":"tool_use"}}`}); err == nil {
		t.Fatal("tool use accepted")
	}
}

func TestSSEErrorsAndLimits(t *testing.T) {
	for _, input := range []string{"event: delta\ndata: {}\n\n", "data: " + strings.Repeat("x", 300) + "\n\n"} {
		if err := TransformSSE(Demo, strings.NewReader(input), io.Discard, session(), 128); err == nil {
			t.Fatal("invalid or unfinished stream accepted")
		}
	}
}
