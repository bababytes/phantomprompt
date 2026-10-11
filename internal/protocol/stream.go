package protocol

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"phantomprompt/internal/mask"
)

type Event struct{ Name, Data string }

func WriteEvent(w io.Writer, e Event) error {
	if e.Name != "" {
		if _, err := fmt.Fprintf(w, "event: %s\n", e.Name); err != nil {
			return err
		}
	}
	for _, line := range strings.Split(e.Data, "\n") {
		if _, err := fmt.Fprintf(w, "data: %s\n", line); err != nil {
			return err
		}
	}
	_, err := io.WriteString(w, "\n")
	return err
}

type StreamDecoder struct {
	kind    string
	session *mask.Session
	streams map[string]*mask.Stream
	done    bool
}

func NewStreamDecoder(kind string, s *mask.Session) *StreamDecoder {
	return &StreamDecoder{kind: kind, session: s, streams: make(map[string]*mask.Stream)}
}

func (d *StreamDecoder) stream(key string) (*mask.Stream, error) {
	if s := d.streams[key]; s != nil {
		return s, nil
	}
	if len(d.streams) >= 64 {
		return nil, ErrFormat
	}
	s := mask.NewStream(d.session)
	d.streams[key] = s
	return s, nil
}

func (d *StreamDecoder) Transform(e Event) ([]Event, error) {
	if d.done {
		return nil, ErrFormat
	}
	if d.kind == OpenAI && e.Data == "[DONE]" {
		if len(d.streams) != 0 {
			return nil, ErrFormat
		}
		d.done = true
		return []Event{e}, nil
	}
	obj, err := object([]byte(e.Data))
	if err != nil {
		return nil, err
	}
	var before []Event
	switch d.kind {
	case Demo:
		s, err := d.stream("text")
		if err != nil {
			return nil, err
		}
		switch e.Name {
		case "receipt":
			var v struct {
				Text string `json:"text"`
			}
			if Decode([]byte(e.Data), &v) != nil {
				return nil, ErrFormat
			}
			for _, m := range mask.Detect(v.Text) {
				if d.session.Enabled(m.Kind) {
					return nil, ErrFormat
				}
			}
		case "delta":
			if err := transformText(obj, "text", func(t string) string { return s.Push(t, false) }); err != nil {
				return nil, err
			}
		case "done":
			if tail := s.Push("", true); tail != "" {
				data, _ := json.Marshal(map[string]string{"text": tail})
				before = append(before, Event{"delta", string(data)})
			}
			d.done = true
		default:
			return nil, ErrFormat
		}
	case OpenAI:
		var choices []map[string]json.RawMessage
		if json.Unmarshal(obj["choices"], &choices) != nil || len(choices) > 32 {
			return nil, ErrFormat
		}
		for _, c := range choices {
			var index int
			if json.Unmarshal(c["index"], &index) != nil || index < 0 || index > 31 {
				return nil, ErrFormat
			}
			m, err := object(c["delta"])
			if err != nil {
				return nil, err
			}
			for key := range m {
				if key != "content" && key != "refusal" && key != "role" {
					return nil, ErrFormat
				}
			}
			var finish *string
			if v, ok := c["finish_reason"]; ok {
				if json.Unmarshal(v, &finish) != nil {
					return nil, ErrFormat
				}
			}
			if finish != nil && *finish != "stop" && *finish != "length" && *finish != "content_filter" {
				return nil, ErrFormat
			}
			for _, field := range []string{"content", "refusal"} {
				key := fmt.Sprintf("%d:%s", index, field)
				_, present := m[field]
				if !present && d.streams[key] == nil {
					continue
				}
				s, err := d.stream(key)
				if err != nil {
					return nil, err
				}
				text := ""
				if raw := m[field]; raw != nil && string(raw) != "null" {
					if json.Unmarshal(raw, &text) != nil {
						return nil, ErrFormat
					}
				}
				m[field], _ = json.Marshal(s.Push(text, finish != nil))
				if finish != nil {
					delete(d.streams, key)
				}
			}
			c["delta"], err = json.Marshal(m)
			if err != nil {
				return nil, err
			}
		}
		obj["choices"], err = json.Marshal(choices)
		if err != nil {
			return nil, err
		}
	case Anthropic:
		var typ string
		if json.Unmarshal(obj["type"], &typ) != nil || typ != e.Name {
			return nil, ErrFormat
		}
		switch typ {
		case "message_start":
			m, err := object(obj["message"])
			if err != nil {
				return nil, err
			}
			var content []json.RawMessage
			if json.Unmarshal(m["content"], &content) != nil || len(content) != 0 {
				return nil, ErrFormat
			}
		case "ping", "message_delta":
		case "message_stop":
			if len(d.streams) != 0 {
				return nil, ErrFormat
			}
			d.done = true
		case "content_block_start", "content_block_delta", "content_block_stop":
			var index int
			if json.Unmarshal(obj["index"], &index) != nil || index < 0 || index > 31 {
				return nil, ErrFormat
			}
			key := fmt.Sprint(index)
			if typ == "content_block_start" {
				if d.streams[key] != nil {
					return nil, ErrFormat
				}
			} else if d.streams[key] == nil {
				return nil, ErrFormat
			}
			s, err := d.stream(key)
			if err != nil {
				return nil, err
			}
			if typ == "content_block_stop" {
				if tail := s.Push("", true); tail != "" {
					data, _ := json.Marshal(map[string]any{"type": "content_block_delta", "index": index, "delta": map[string]string{"type": "text_delta", "text": tail}})
					before = append(before, Event{"content_block_delta", string(data)})
				}
				delete(d.streams, key)
			} else {
				field, expected := "delta", "text_delta"
				if typ == "content_block_start" {
					field, expected = "content_block", "text"
				}
				m, err := object(obj[field])
				if err != nil {
					return nil, err
				}
				var blockType string
				if json.Unmarshal(m["type"], &blockType) != nil || blockType != expected {
					return nil, ErrFormat
				}
				if err := transformText(m, "text", func(t string) string { return s.Push(t, false) }); err != nil {
					return nil, err
				}
				obj[field], err = json.Marshal(m)
				if err != nil {
					return nil, err
				}
			}
		default:
			return nil, ErrFormat
		}
	default:
		return nil, ErrFormat
	}
	encoded, err := json.Marshal(obj)
	if err != nil {
		return nil, err
	}
	return append(before, Event{e.Name, string(encoded)}), nil
}

func TransformSSE(kind string, input io.Reader, output io.Writer, session *mask.Session, maxEvent int) error {
	scanner := bufio.NewScanner(input)
	scanner.Buffer(make([]byte, 4096), maxEvent)
	decoder := NewStreamDecoder(kind, session)
	var event Event
	size := 0
	hasData := false
	for scanner.Scan() {
		line := scanner.Text()
		size += len(line) + 1
		if size > maxEvent {
			return ErrFormat
		}
		if line == "" {
			if hasData {
				out, err := decoder.Transform(event)
				if err != nil {
					return err
				}
				for _, e := range out {
					if err := WriteEvent(output, e); err != nil {
						return err
					}
				}
				if f, ok := output.(interface{ Flush() }); ok {
					f.Flush()
				}
				if decoder.done {
					return nil
				}
			}
			event = Event{}
			size = 0
			hasData = false
			continue
		}
		field, value, _ := strings.Cut(line, ":")
		value = strings.TrimPrefix(value, " ")
		switch field {
		case "event":
			event.Name = value
		case "data":
			if hasData {
				event.Data += "\n"
			}
			event.Data += value
			hasData = true
		case "":
			if !hasData {
				if _, err := io.WriteString(output, ": heartbeat\n\n"); err != nil {
					return err
				}
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return ErrFormat
	}
	return io.ErrUnexpectedEOF
}
