package protocol

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"regexp"
	"unicode/utf8"

	"phantomprompt/internal/mask"
)

const (
	Demo      = "demo"
	OpenAI    = "openai-chat"
	Anthropic = "anthropic-messages"
)

var (
	ErrFormat     = errors.New("unsupported message format")
	ErrAttachment = errors.New("attachments are not supported")
	ErrExposed    = errors.New("sensitive value outside a protected field")
)
var modelName = regexp.MustCompile(`^[a-zA-Z0-9._:-]{1,128}$`)

type DemoRequest struct {
	Text   string `json:"text"`
	Stream bool   `json:"stream,omitempty"`
}
type DemoResponse struct {
	Text     string `json:"text"`
	Received string `json:"received"`
}
type textBlock struct {
	Type string `json:"type"`
	Text string `json:"text"`
}
type message struct {
	Role    string          `json:"role"`
	Content json.RawMessage `json:"content"`
}
type chatRequest struct {
	Model               string          `json:"model"`
	Messages            []message       `json:"messages"`
	Stream              bool            `json:"stream,omitempty"`
	MaxTokens           uint32          `json:"max_tokens,omitempty"`
	MaxCompletionTokens uint32          `json:"max_completion_tokens,omitempty"`
	Temperature         *float64        `json:"temperature,omitempty"`
	TopP                *float64        `json:"top_p,omitempty"`
	System              json.RawMessage `json:"system,omitempty"`
}

func Decode(data []byte, value any) error {
	if !utf8.Valid(data) {
		return ErrFormat
	}
	d := json.NewDecoder(bytes.NewReader(data))
	d.DisallowUnknownFields()
	if err := d.Decode(value); err != nil {
		return ErrFormat
	}
	var extra any
	if err := d.Decode(&extra); err != io.EOF {
		return ErrFormat
	}
	return nil
}

func MaskRequest(kind string, data []byte, session *mask.Session) ([]byte, mask.Counts, bool, error) {
	if kind == Demo {
		var req DemoRequest
		if err := Decode(data, &req); err != nil || req.Text == "" {
			return nil, mask.Counts{}, false, ErrFormat
		}
		texts, counts, err := session.Mask([]string{req.Text})
		if err != nil {
			return nil, counts, false, err
		}
		req.Text = texts[0]
		out, err := json.Marshal(req)
		return out, counts, req.Stream, err
	}
	if kind != OpenAI && kind != Anthropic {
		return nil, mask.Counts{}, false, ErrFormat
	}
	var req chatRequest
	if Decode(data, &req) != nil || !modelName.MatchString(req.Model) || len(mask.Detect(req.Model)) != 0 || len(req.Messages) == 0 || len(req.Messages) > 256 {
		return nil, mask.Counts{}, false, ErrFormat
	}
	if req.MaxTokens > 1000000 || req.MaxCompletionTokens > 1000000 || req.Temperature != nil && (*req.Temperature < 0 || *req.Temperature > 2) || req.TopP != nil && (*req.TopP < 0 || *req.TopP > 1) {
		return nil, mask.Counts{}, false, ErrFormat
	}
	if kind == OpenAI && len(req.System) != 0 || kind == Anthropic && (req.MaxTokens == 0 || req.MaxCompletionTokens != 0) {
		return nil, mask.Counts{}, false, ErrFormat
	}
	var texts []string
	type field struct {
		target       *json.RawMessage
		blocks       []textBlock
		scalar       bool
		start, count int
	}
	var fields []field
	add := func(target *json.RawMessage) error {
		var scalar string
		if json.Unmarshal(*target, &scalar) == nil && string(*target) != "null" {
			fields = append(fields, field{target: target, scalar: true, start: len(texts), count: 1})
			texts = append(texts, scalar)
			return nil
		}
		var blocks []textBlock
		if Decode(*target, &blocks) != nil || len(blocks) == 0 || len(blocks) > 256 {
			return ErrFormat
		}
		f := field{target: target, blocks: blocks, start: len(texts), count: len(blocks)}
		for _, b := range blocks {
			if b.Type != "text" {
				return ErrFormat
			}
			texts = append(texts, b.Text)
		}
		fields = append(fields, f)
		return nil
	}
	if len(req.System) != 0 {
		if err := add(&req.System); err != nil {
			return nil, mask.Counts{}, false, err
		}
	}
	for i := range req.Messages {
		m := &req.Messages[i]
		if m.Role != "user" && m.Role != "assistant" && !(kind == OpenAI && (m.Role == "system" || m.Role == "developer")) {
			return nil, mask.Counts{}, false, ErrFormat
		}
		if err := add(&m.Content); err != nil {
			return nil, mask.Counts{}, false, err
		}
	}
	masked, counts, err := session.Mask(texts)
	if err != nil {
		return nil, counts, false, err
	}
	for _, f := range fields {
		if f.scalar {
			*f.target, err = json.Marshal(masked[f.start])
		} else {
			for j := 0; j < f.count; j++ {
				f.blocks[j].Text = masked[f.start+j]
			}
			*f.target, err = json.Marshal(f.blocks)
		}
		if err != nil {
			return nil, counts, false, err
		}
	}
	out, err := json.Marshal(req)
	return out, counts, req.Stream, err
}
