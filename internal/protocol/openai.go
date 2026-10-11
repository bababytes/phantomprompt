package protocol

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	"phantomprompt/internal/mask"
)

func decodeRoot(data []byte) (map[string]any, error) {
	value, err := decodeExact(string(data))
	if err != nil {
		return nil, err
	}
	root, ok := value.(map[string]any)
	if !ok {
		return nil, ErrFormat
	}
	return root, nil
}

func (c *agentCollector) chatContent(holder map[string]any, key string) error {
	switch content := holder[key].(type) {
	case nil:
		return nil
	case string:
		return c.field(holder, key)
	case []any:
		for _, part := range content {
			block, ok := part.(map[string]any)
			if !ok {
				return ErrFormat
			}
			switch block["type"] {
			case "text":
				if err := c.field(block, "text"); err != nil {
					return err
				}
			case "refusal":
				if err := c.field(block, "refusal"); err != nil {
					return err
				}
			case "image_url", "file":
				c.attachment()
			case "input_audio":
				return ErrAttachment
			default:
				if err := c.unknownBlock(block); err != nil {
					return err
				}
			}
		}
		return nil
	}
	return ErrFormat
}

func (c *agentCollector) chatCalls(message map[string]any) error {
	calls, _ := message["tool_calls"].([]any)
	for _, item := range calls {
		call, ok := item.(map[string]any)
		function, _ := call["function"].(map[string]any)
		if !ok || function == nil {
			return ErrFormat
		}
		if err := c.jsonText(function, "arguments"); err != nil {
			return err
		}
	}
	if legacy, ok := message["function_call"].(map[string]any); ok {
		return c.jsonText(legacy, "arguments")
	}
	return nil
}

func MaskOpenAIChat(data []byte, session *mask.Session, destination string, public []string) ([]byte, mask.Counts, error) {
	root, err := decodeRoot(data)
	if err != nil {
		return nil, mask.Counts{}, err
	}
	messages, ok := root["messages"].([]any)
	if !ok {
		return nil, mask.Counts{}, ErrFormat
	}
	var c agentCollector
	for i, item := range messages {
		message, ok := item.(map[string]any)
		if !ok {
			return nil, mask.Counts{}, ErrFormat
		}
		if role, _ := message["role"].(string); !blockType.MatchString(role) {
			return nil, mask.Counts{}, ErrFormat
		}
		c.context, c.typed, c.latest = message["role"] == "system" || message["role"] == "developer", message["role"] == "user", i == len(messages)-1
		if err := c.chatContent(message, "content"); err != nil {
			return nil, mask.Counts{}, err
		}
		if _, ok := message["refusal"].(string); ok {
			c.field(message, "refusal")
		}
		if err := c.chatCalls(message); err != nil {
			return nil, mask.Counts{}, err
		}
	}
	if prediction, ok := root["prediction"].(map[string]any); ok {
		if err := c.chatContent(prediction, "content"); err != nil {
			return nil, mask.Counts{}, err
		}
	}
	counts, err := c.mask(session, destination, public)
	if err != nil {
		return nil, counts, err
	}
	counts.UnknownBlocks = c.unknown
	out, err := encodeJSON(root)
	return out, counts, err
}

func (c *agentCollector) responseParts(parts []any) error {
	for _, part := range parts {
		block, ok := part.(map[string]any)
		if !ok {
			return ErrFormat
		}
		switch block["type"] {
		case "input_text", "output_text", "text":
			if err := c.field(block, "text"); err != nil {
				return err
			}
		case "refusal":
			if err := c.field(block, "refusal"); err != nil {
				return err
			}
		case "input_image", "input_file":
			c.attachment()
		case "input_audio":
			return ErrAttachment
		default:
			if err := c.unknownBlock(block); err != nil {
				return err
			}
		}
	}
	return nil
}

func (c *agentCollector) responseValue(holder map[string]any, key string) error {
	switch value := holder[key].(type) {
	case string:
		return c.field(holder, key)
	case []any:
		return c.responseParts(value)
	}
	return ErrFormat
}

func (c *agentCollector) responseItem(value any) error {
	item, ok := value.(map[string]any)
	if !ok {
		return ErrFormat
	}
	kind, _ := item["type"].(string)
	if _, hasRole := item["role"]; kind == "" && hasRole {
		kind = "message"
	}
	c.context, c.typed = item["role"] == "system" || item["role"] == "developer", kind == "message" && item["role"] == "user"
	switch kind {
	case "message":
		return c.responseValue(item, "content")
	case "function_call":
		return c.jsonText(item, "arguments")
	case "function_call_output", "custom_tool_call_output":
		return c.responseValue(item, "output")
	case "custom_tool_call":
		return c.field(item, "input")
	case "reasoning", "item_reference", "additional_tools":
		return nil
	case "local_shell_call", "tool_search_call", "tool_search_output", "web_search_call", "image_generation_call", "agent_message", "compaction", "compaction_summary", "context_compaction", "configuration_update", "compaction_trigger":
		c.generic(item)
		return nil
	}
	return c.unknownBlock(item)
}

func MaskOpenAIResponses(data []byte, session *mask.Session, destination string, public []string) ([]byte, mask.Counts, error) {
	root, err := decodeRoot(data)
	if err != nil {
		return nil, mask.Counts{}, err
	}
	c := agentCollector{context: true}
	if root["instructions"] != nil {
		if err := c.field(root, "instructions"); err != nil {
			return nil, mask.Counts{}, err
		}
	}
	c.context = false
	switch input := root["input"].(type) {
	case string:
		c.typed = true
		c.field(root, "input")
	case []any:
		for i, item := range input {
			c.latest = i == len(input)-1
			if err := c.responseItem(item); err != nil {
				return nil, mask.Counts{}, err
			}
		}
	default:
		return nil, mask.Counts{}, ErrFormat
	}
	counts, err := c.mask(session, destination, public)
	if err != nil {
		return nil, counts, err
	}
	counts.UnknownBlocks = c.unknown
	out, err := encodeJSON(root)
	return out, counts, err
}

// restoreTree restores every string; arguments and input of a named tool call keep secret
// placeholders unless the tool edits files (see withheldFor).
func restoreTree(value any, key string, session *mask.Session, withhold, kept map[string]bool, withheld *int) any {
	switch v := value.(type) {
	case map[string]any:
		inner := withhold
		if name, named := v["name"]; named {
			inner = withheldFor(name)
		}
		for k, item := range v {
			if k == "arguments" || k == "input" {
				v[k] = restoreTree(item, k, session, inner, kept, withheld)
			} else {
				v[k] = restoreTree(item, k, session, withhold, kept, withheld)
			}
		}
	case []any:
		for i, item := range v {
			v[i] = restoreTree(item, key, session, withhold, kept, withheld)
		}
	case string:
		out, count := session.RestoreExcept(v, key == "arguments", withhold, kept)
		*withheld += count
		return out
	}
	return value
}

func RestoreOpenAI(data []byte, session *mask.Session) ([]byte, map[string]bool, error) {
	value, err := decodeExact(string(data))
	if err != nil {
		return nil, nil, err
	}
	var withheld int
	kept := make(map[string]bool)
	out, err := encodeJSON(restoreTree(value, "", session, nil, kept, &withheld))
	return out, kept, err
}

func number(value any) int {
	switch n := value.(type) {
	case json.Number:
		if parsed, err := n.Int64(); err == nil && parsed > 0 {
			return int(parsed)
		}
	case float64:
		if n > 0 {
			return int(n)
		}
	}
	return 0
}

func openAIUsage(value any) int {
	usage, _ := value.(map[string]any)
	if tokens := number(usage["input_tokens"]); tokens > 0 {
		return tokens
	}
	return number(usage["prompt_tokens"])
}

func OpenAIInputTokens(data []byte) int {
	root, err := decodeRoot(data)
	if err != nil {
		return 0
	}
	return openAIUsage(root["usage"])
}

type openAIStream struct {
	session     *mask.Session
	streams     map[string]*mask.Stream
	tools       map[string]any
	kept        map[string]bool
	inputTokens int
	sequence    int
	withheld    int
	completed   bool
}

func newOpenAIStream(session *mask.Session) *openAIStream {
	return &openAIStream{session: session, streams: make(map[string]*mask.Stream), tools: make(map[string]any), kept: make(map[string]bool)}
}

// push restores a streamed field; tool is the call's tool name, or nil for plain text.
func (s *openAIStream) push(key string, inJSON bool, text string, final bool, tool any) string {
	stream := s.streams[key]
	if stream == nil {
		if final {
			return ""
		}
		var withhold map[string]bool
		if tool != nil {
			withhold = withheldFor(tool)
		}
		stream = mask.NewWithholdingStream(s.session, inJSON, withhold, s.kept)
		s.streams[key] = stream
	}
	out := stream.Push(text, final)
	if final {
		s.withheld += stream.Withheld
		delete(s.streams, key)
	}
	return out
}

func (s *openAIStream) restore(value map[string]any, withhold map[string]bool) map[string]any {
	restored, _ := restoreTree(value, "", s.session, withhold, s.kept, &s.withheld).(map[string]any)
	return restored
}

// toolOf names the tool of a streamed call; a call whose tool is unknown is treated like a shell.
func (s *openAIStream) toolOf(key string) any {
	if name, ok := s.tools[key]; ok {
		return name
	}
	return ""
}

func (s *openAIStream) chatChoice(choice map[string]any) {
	index := fmt.Sprint(choice["index"])
	delta, _ := choice["delta"].(map[string]any)
	if delta == nil {
		delta = map[string]any{}
	}
	for _, field := range []string{"content", "refusal"} {
		if text, ok := delta[field].(string); ok {
			delta[field] = s.push(index+":"+field, false, text, false, nil)
		}
	}
	calls, _ := delta["tool_calls"].([]any)
	seen := map[string]map[string]any{}
	for _, item := range calls {
		call, _ := item.(map[string]any)
		function, _ := call["function"].(map[string]any)
		if function == nil {
			continue
		}
		key := index + ":call:" + fmt.Sprint(call["index"])
		seen[key] = function
		if name, ok := function["name"].(string); ok && name != "" {
			s.tools[key] = name
		}
		if text, ok := function["arguments"].(string); ok {
			function["arguments"] = s.push(key, true, text, false, s.toolOf(key))
		}
	}
	if choice["finish_reason"] == nil {
		return
	}
	for _, field := range []string{"content", "refusal"} {
		if tail := s.push(index+":"+field, false, "", true, nil); tail != "" {
			current, _ := delta[field].(string)
			delta[field] = current + tail
		}
	}
	for key := range s.streams {
		prefix := index + ":call:"
		if !strings.HasPrefix(key, prefix) {
			continue
		}
		tail := s.push(key, true, "", true, s.toolOf(key))
		if tail == "" {
			continue
		}
		if function := seen[key]; function != nil {
			current, _ := function["arguments"].(string)
			function["arguments"] = current + tail
			continue
		}
		calls = append(calls, map[string]any{"index": json.Number(strings.TrimPrefix(key, prefix)), "function": map[string]any{"arguments": tail}})
		delta["tool_calls"] = calls
	}
	choice["delta"] = delta
}

func (s *openAIStream) open() int { return len(s.streams) + len(s.tools) }

func (s *openAIStream) done() bool { return s.completed }

func (s *openAIStream) chat(event Event) ([]Event, bool, error) {
	if event.Data == "[DONE]" {
		s.completed = true
		return append(s.flushChat(), event), false, nil
	}
	value, ok := decodeJSONObject(event.Data)
	if !ok {
		return []Event{event}, false, nil
	}
	if tokens := openAIUsage(value["usage"]); tokens > 0 {
		s.inputTokens = tokens
	}
	choices, _ := value["choices"].([]any)
	for _, item := range choices {
		if choice, ok := item.(map[string]any); ok {
			s.chatChoice(choice)
		}
	}
	return encodeEvent(event, value), len(choices) > 0, nil
}

func streamKey(value map[string]any, kind string) string {
	return fmt.Sprintf("%s:%v:%v:%v:%v", kind, value["item_id"], value["output_index"], value["content_index"], value["summary_index"])
}

func (s *openAIStream) numbered(name string, value map[string]any) Event {
	if _, ok := value["sequence_number"]; ok {
		value["sequence_number"] = s.sequence
		s.sequence++
	}
	data, _ := encodeJSON(value)
	return Event{Name: name, Data: string(data)}
}

func (s *openAIStream) responses(event Event) ([]Event, bool, error) {
	value, ok := decodeJSONObject(event.Data)
	if !ok {
		return []Event{event}, false, nil
	}
	kind, _ := value["type"].(string)
	switch kind {
	case "response.completed", "response.incomplete", "response.failed":
		s.completed = true
	}
	if item, ok := value["item"].(map[string]any); ok {
		if name, named := item["name"]; named {
			s.tools[fmt.Sprint(item["id"])] = name
		}
	}
	var tool any
	var withhold map[string]bool
	if strings.Contains(kind, "arguments") || strings.Contains(kind, "_input") {
		tool = s.toolOf(fmt.Sprint(value["item_id"]))
		withhold = withheldFor(tool)
	}
	switch {
	case strings.HasSuffix(kind, ".delta"):
		if text, ok := value["delta"].(string); ok {
			base := strings.TrimSuffix(kind, ".delta")
			value["delta"] = s.push(streamKey(value, base), strings.Contains(base, "arguments"), text, false, tool)
		}
		return []Event{s.numbered(event.Name, value)}, true, nil
	case strings.HasSuffix(kind, ".done"):
		base := strings.TrimSuffix(kind, ".done")
		var before []Event
		if tail := s.push(streamKey(value, base), strings.Contains(base, "arguments"), "", true, tool); tail != "" {
			delta := map[string]any{"type": base + ".delta", "delta": tail, "sequence_number": 0}
			for _, field := range []string{"item_id", "output_index", "content_index", "summary_index"} {
				if v, ok := value[field]; ok {
					delta[field] = v
				}
			}
			before = append(before, s.numbered(base+".delta", delta))
		}
		return append(before, s.numbered(event.Name, s.restore(value, withhold))), true, nil
	}
	if response, ok := value["response"].(map[string]any); ok {
		if tokens := openAIUsage(response["usage"]); tokens > 0 {
			s.inputTokens = tokens
		}
	}
	return []Event{s.numbered(event.Name, s.restore(value, nil))}, true, nil
}

func (s *openAIStream) flushResponses() []Event {
	var events []Event
	for key := range s.streams {
		parts := strings.SplitN(key, ":", 3)
		var tool any
		if len(parts) == 3 && (strings.Contains(parts[0], "arguments") || strings.Contains(parts[0], "_input")) {
			tool = s.toolOf(parts[1])
		}
		tail := s.push(key, strings.Contains(parts[0], "arguments"), "", true, tool)
		if tail != "" {
			events = append(events, s.numbered(parts[0]+".delta", map[string]any{"type": parts[0] + ".delta", "delta": tail, "sequence_number": 0}))
		}
	}
	return events
}

func (s *openAIStream) flushChat() []Event {
	if len(s.streams) == 0 {
		return nil
	}
	var choices []any
	for key := range s.streams {
		index, field, _ := strings.Cut(key, ":")
		var tool any
		if strings.HasPrefix(field, "call:") {
			tool = s.toolOf(key)
		}
		tail := s.push(key, strings.HasPrefix(field, "call:"), "", true, tool)
		if tail == "" {
			continue
		}
		delta := map[string]any{field: tail}
		if call, ok := strings.CutPrefix(field, "call:"); ok {
			delta = map[string]any{"tool_calls": []any{map[string]any{"index": json.Number(call), "function": map[string]any{"arguments": tail}}}}
		}
		choices = append(choices, map[string]any{"index": json.Number(index), "delta": delta})
	}
	if len(choices) == 0 {
		return nil
	}
	data, _ := encodeJSON(map[string]any{"object": "chat.completion.chunk", "choices": choices})
	return []Event{{Data: string(data)}}
}

// maxOpenStates bounds the content blocks and tool calls a stream may keep open at once.
const maxOpenStates = 1024

type sseHandler struct {
	transform func(Event) ([]Event, bool, error)
	finish    func() []Event
	open      func() int
	completed func() bool
}

// pipeSSE restores an SSE stream event by event. An event larger than maxEvent in total is a
// format error, and a stream that ends before its final event is io.ErrUnexpectedEOF.
func pipeSSE(input io.Reader, output io.Writer, maxEvent int, handler sseHandler) (StreamStats, error) {
	scanner := bufio.NewScanner(input)
	scanner.Buffer(make([]byte, min(64<<10, maxEvent)), maxEvent)
	var stats StreamStats
	var event Event
	size, hasData := 0, false
	write := func(events []Event) error {
		for _, e := range events {
			if err := WriteEvent(output, e); err != nil {
				return err
			}
		}
		return nil
	}
	flush := func() error {
		if !hasData {
			return nil
		}
		stats.Events++
		events, changed, err := handler.transform(event)
		if err != nil {
			return err
		}
		if handler.open() > maxOpenStates {
			return fmt.Errorf("%w: more than %d open stream blocks", ErrFormat, maxOpenStates)
		}
		stats.Completed = stats.Completed || handler.completed()
		if changed {
			stats.Restored++
		}
		event, size, hasData = Event{}, 0, false
		return write(events)
	}
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			if err := flush(); err != nil {
				return stats, err
			}
			event, size = Event{}, 0
			continue
		}
		if size += len(line) + 1; size > maxEvent {
			return stats, fmt.Errorf("%w: stream event larger than %d bytes", ErrFormat, maxEvent)
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
					return stats, err
				}
			}
		}
	}
	if err := scanner.Err(); err != nil {
		if errors.Is(err, bufio.ErrTooLong) {
			return stats, fmt.Errorf("%w: stream line larger than %d bytes", ErrFormat, maxEvent)
		}
		return stats, err
	}
	if err := flush(); err != nil {
		return stats, err
	}
	if err := write(handler.finish()); err != nil {
		return stats, err
	}
	if !stats.Completed {
		return stats, io.ErrUnexpectedEOF
	}
	return stats, nil
}

func RestoreOpenAIChatStream(input io.Reader, output io.Writer, session *mask.Session, maxEvent int) (StreamStats, error) {
	state := newOpenAIStream(session)
	stats, err := pipeSSE(input, output, maxEvent, sseHandler{state.chat, state.flushChat, state.open, state.done})
	stats.InputTokens, stats.Withheld, stats.Kept = state.inputTokens, state.withheld, state.kept
	return stats, err
}

func RestoreOpenAIResponsesStream(input io.Reader, output io.Writer, session *mask.Session, maxEvent int) (StreamStats, error) {
	state := newOpenAIStream(session)
	stats, err := pipeSSE(input, output, maxEvent, sseHandler{state.responses, state.flushResponses, state.open, state.done})
	stats.InputTokens, stats.Withheld, stats.Kept = state.inputTokens, state.withheld, state.kept
	return stats, err
}
