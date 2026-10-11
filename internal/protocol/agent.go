package protocol

import (
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"regexp"
	"slices"
	"strings"

	"phantomprompt/internal/mask"
)

var blockType = regexp.MustCompile(`^[a-z_]{1,40}$`)

type textTarget struct {
	text    string
	set     func(string)
	context bool
	label   string
	literal bool
}

type agentCollector struct {
	targets []textTarget
	renames []func()
	after   []func() error
	context bool
	unknown map[string]int
}

// Protocol keys never carry user text and must reach the API unchanged.
func protocolKey(key string) bool {
	switch key {
	case "type", "id", "name", "tool_name", "signature", "cache_control", "role":
		return true
	}
	return strings.HasSuffix(key, "_id") || strings.HasPrefix(key, "encrypted")
}

// generic masks every text leaf of a block type this adapter does not know yet, so a new Claude
// Code release keeps working without sending the values in clear. Only the block's own protocol
// keys are left alone; nested values are data wherever they sit.
func (c *agentCollector) generic(block map[string]any) {
	for key, item := range block {
		if !protocolKey(key) {
			c.leaves(item, key, func(updated any) { block[key] = updated })
		}
	}
}

func (c *agentCollector) jsonText(holder map[string]any, key string) error {
	text, ok := holder[key].(string)
	if !ok {
		if holder[key] == nil {
			return nil
		}
		return ErrFormat
	}
	value, err := decodeExact(text)
	if err != nil {
		return c.field(holder, key)
	}
	c.leaves(value, "", func(updated any) { value = updated })
	c.after = append(c.after, func() error {
		encoded, err := encodeJSON(value)
		if err != nil {
			return ErrFormat
		}
		holder[key] = string(encoded)
		return nil
	})
	return nil
}

func (c *agentCollector) mask(session *mask.Session, destination string, public []string) (mask.Counts, error) {
	var counts mask.Counts
	for _, context := range []bool{true, false} {
		var targets []textTarget
		var texts []mask.Text
		for _, target := range c.targets {
			if target.context == context {
				targets = append(targets, target)
				texts = append(texts, mask.Text{Value: target.text, Label: target.label, Literal: target.literal})
			}
		}
		masked, part, err := session.MaskTexts(destination, texts, public)
		if err != nil {
			return counts, err
		}
		for i, target := range targets {
			target.set(masked[i])
		}
		if context {
			part.ContextKinds = part.NewKinds
		}
		counts = counts.Add(part)
	}
	for _, rename := range c.renames {
		rename()
	}
	for _, finish := range c.after {
		if err := finish(); err != nil {
			return counts, err
		}
	}
	return counts, nil
}

func (c *agentCollector) field(holder map[string]any, key string) error {
	text, ok := holder[key].(string)
	if !ok {
		return ErrFormat
	}
	context := c.context || strings.HasPrefix(strings.TrimSpace(text), "<system-reminder>")
	c.targets = append(c.targets, textTarget{text: text, set: func(value string) { holder[key] = value }, context: context})
	return nil
}

// leaves collects the strings and numbers of a structured value with the key that holds them,
// and its keys, which may hold data too.
func (c *agentCollector) leaves(value any, label string, set func(any)) {
	switch v := value.(type) {
	case map[string]any:
		for key, item := range v {
			c.leaves(item, key, func(updated any) { v[key] = updated })
			c.targets = append(c.targets, textTarget{text: key, context: c.context, set: func(masked string) {
				if masked != key {
					c.renames = append(c.renames, func() { v[masked] = v[key]; delete(v, key) })
				}
			}})
		}
	case []any:
		for i, item := range v {
			c.leaves(item, label, func(updated any) { v[i] = updated })
		}
	case string:
		c.targets = append(c.targets, textTarget{text: v, set: func(text string) { set(text) }, context: c.context, label: label})
	case json.Number:
		c.targets = append(c.targets, textTarget{text: v.String(), context: c.context, label: label, literal: true, set: func(masked string) {
			var token string
			if masked != v.String() && json.Unmarshal([]byte(masked), &token) == nil {
				set(token)
			}
		}})
	}
}

func unsupportedBlock(kind any) error {
	name, _ := kind.(string)
	if !blockType.MatchString(name) {
		name = "unknown"
	}
	return fmt.Errorf("%w: content block %s", ErrFormat, name)
}

func (c *agentCollector) block(value any) error {
	block, ok := value.(map[string]any)
	if !ok {
		return ErrFormat
	}
	switch block["type"] {
	case "text":
		return c.field(block, "text")
	case "tool_use":
		c.leaves(block["input"], "", func(updated any) { block["input"] = updated })
		return nil
	case "tool_result":
		return c.content(block, "content")
	case "thinking", "redacted_thinking":
		return nil
	case "tool_addition", "tool_reference":
		// Tool definitions and references to them, like the top-level tools array; not user content.
		return nil
	case "image", "document":
		return ErrAttachment
	}
	return c.unknownBlock(block)
}

// unknownBlock masks a block or item type the adapter does not know yet (any provider) and
// records it, so the panel shows a format alert instead of the conversation stopping.
func (c *agentCollector) unknownBlock(block map[string]any) error {
	name, _ := block["type"].(string)
	if !blockType.MatchString(name) {
		return unsupportedBlock(block["type"])
	}
	if _, binary := block["source"]; binary {
		return ErrAttachment
	}
	if c.unknown == nil {
		c.unknown = make(map[string]int)
	}
	c.unknown[name]++
	c.generic(block)
	return nil
}

func (c *agentCollector) content(holder map[string]any, key string) error {
	switch content := holder[key].(type) {
	case nil:
		return nil
	case string:
		return c.field(holder, key)
	case []any:
		for _, block := range content {
			if err := c.block(block); err != nil {
				return err
			}
		}
		return nil
	}
	return ErrFormat
}

func MaskAgentMessages(data []byte, session *mask.Session, destination string, public []string) ([]byte, mask.Counts, error) {
	value, err := decodeExact(string(data))
	if err != nil {
		return nil, mask.Counts{}, err
	}
	root, ok := value.(map[string]any)
	if !ok {
		return nil, mask.Counts{}, ErrFormat
	}
	c := agentCollector{context: true}
	if err := c.content(root, "system"); err != nil {
		return nil, mask.Counts{}, err
	}
	messages, ok := root["messages"].([]any)
	if !ok {
		return nil, mask.Counts{}, ErrFormat
	}
	for _, item := range messages {
		message, ok := item.(map[string]any)
		role, _ := message["role"].(string)
		if !ok || !blockType.MatchString(role) {
			return nil, mask.Counts{}, ErrFormat
		}
		c.context = role == "system"
		if err := c.content(message, "content"); err != nil {
			return nil, mask.Counts{}, err
		}
	}
	if metadata, ok := root["metadata"].(map[string]any); ok {
		if userID, ok := metadata["user_id"].(string); ok {
			public = append(public, mask.GUIDs(userID)...)
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

func MaskToolArguments(data []byte, session *mask.Session) ([]byte, mask.Counts, error) {
	value, err := decodeExact(string(data))
	if err != nil {
		return nil, mask.Counts{}, err
	}
	root, ok := value.(map[string]any)
	if !ok {
		return nil, mask.Counts{}, ErrFormat
	}
	params, _ := root["params"].(map[string]any)
	if root["method"] != "tools/call" || params == nil || params["arguments"] == nil {
		return data, mask.Counts{}, nil
	}
	var c agentCollector
	c.leaves(params["arguments"], "", func(updated any) { params["arguments"] = updated })
	counts, err := c.mask(session, "", nil)
	if err != nil {
		return nil, counts, err
	}
	out, err := encodeJSON(root)
	return out, counts, err
}

func restoreLeaves(value any, session *mask.Session) any {
	switch v := value.(type) {
	case map[string]any:
		for key, item := range v {
			v[key] = restoreLeaves(item, session)
		}
	case []any:
		for i, item := range v {
			v[i] = restoreLeaves(item, session)
		}
	case string:
		return session.Restore(v)
	}
	return value
}

// Secrets and protected file lines are restored only for tools that write files locally. A
// shell, fetch or MCP call keeps the placeholder, so injected instructions cannot send it away.
var withheldOutsideEdits = map[string]bool{"SECRET": true, "FILE": true}

// File-writing tools of Claude Code, Copilot and Codex (compared without case).
var fileEditTools = map[string]bool{"write": true, "edit": true, "multiedit": true, "notebookedit": true, "create": true, "str_replace_editor": true, "str_replace_based_edit_tool": true, "str_replace": true, "insert": true, "apply_patch": true, "write_file": true, "edit_file": true, "create_file": true}

func withheldFor(tool any) map[string]bool {
	name, _ := tool.(string)
	if fileEditTools[strings.ToLower(name)] {
		return nil
	}
	return withheldOutsideEdits
}

func restoreLeavesExcept(value any, session *mask.Session, withhold, kept map[string]bool) any {
	switch v := value.(type) {
	case map[string]any:
		restored := make(map[string]any, len(v))
		for key, item := range v {
			original, _ := session.RestoreExcept(key, false, withhold, kept)
			restored[original] = restoreLeavesExcept(item, session, withhold, kept)
		}
		return restored
	case []any:
		for i, item := range v {
			v[i] = restoreLeavesExcept(item, session, withhold, kept)
		}
	case string:
		if typed, ok := session.RestoreTyped(v, withhold); ok {
			if restored, err := decodeExact(typed); err == nil {
				return restored
			}
		}
		out, _ := session.RestoreExcept(v, false, withhold, kept)
		return out
	}
	return value
}

func restoreBlock(value any, session *mask.Session, kept map[string]bool) {
	block, ok := value.(map[string]any)
	if !ok {
		return
	}
	switch block["type"] {
	case "text":
		if text, ok := block["text"].(string); ok {
			block["text"] = session.Restore(text)
		}
	case "tool_use":
		block["input"] = restoreLeavesExcept(block["input"], session, withheldFor(block["name"]), kept)
	case "thinking":
		if text, ok := block["thinking"].(string); ok {
			session.MarkKept(text, kept)
		}
	}
}

func RestoreAgentMessages(data []byte, session *mask.Session) ([]byte, map[string]bool, error) {
	value, err := decodeExact(string(data))
	if err != nil {
		return nil, nil, err
	}
	root, ok := value.(map[string]any)
	if !ok {
		return nil, nil, ErrFormat
	}
	content, hasContent := root["content"].([]any)
	if !hasContent {
		return data, nil, nil
	}
	kept := make(map[string]bool)
	for _, block := range content {
		restoreBlock(block, session, kept)
	}
	out, err := encodeJSON(root)
	return out, kept, err
}

// StreamStats describe a restored stream; Completed means the final event (message_stop,
// response.completed, [DONE]) was delivered, after which a client may simply hang up.
type StreamStats struct {
	Events, Restored, InputTokens, Withheld int
	Completed                               bool
	Kept                                    map[string]bool
}

type agentStream struct {
	session     *mask.Session
	streams     map[string]*mask.Stream
	tools       map[string]any
	kept        map[string]bool
	inputTokens int
	withheld    int
	completed   bool
}

func usageTokens(value any) int {
	usage, _ := value.(map[string]any)
	total := 0
	for _, key := range []string{"input_tokens", "cache_creation_input_tokens", "cache_read_input_tokens"} {
		switch number := usage[key].(type) {
		case json.Number:
			if n, err := number.Int64(); err == nil && n > 0 {
				total += int(n)
			}
		case float64:
			if number > 0 {
				total += int(number)
			}
		}
	}
	return total
}

func AgentInputTokens(data []byte) int {
	value, err := decodeExact(string(data))
	if err != nil {
		return 0
	}
	root, _ := value.(map[string]any)
	return usageTokens(root["usage"])
}

func (s *agentStream) push(index, kind, text string, final bool) string {
	key := index + ":" + kind
	stream := s.streams[key]
	if stream == nil {
		stream = mask.NewStream(s.session)
		if kind == "input_json_delta" {
			stream = mask.NewWithholdingJSONStream(s.session, withheldFor(s.tools[index]), s.kept)
		}
		s.streams[key] = stream
	}
	out := stream.Push(text, final)
	if final {
		s.withheld += stream.Withheld
		delete(s.streams, key)
	}
	return out
}

var deltaFields = map[string]string{"text_delta": "text", "input_json_delta": "partial_json"}

func (s *agentStream) transform(event Event) ([]Event, bool, error) {
	value, ok := decodeJSONObject(event.Data)
	if !ok {
		return []Event{event}, false, nil
	}
	index := fmt.Sprint(value["index"])
	switch value["type"] {
	case "message_stop":
		s.completed = true
	case "message_start":
		message, _ := value["message"].(map[string]any)
		s.inputTokens = usageTokens(message["usage"])
	case "content_block_start":
		block, _ := value["content_block"].(map[string]any)
		if block["type"] == "tool_use" {
			s.tools[index] = block["name"]
		}
		if text, ok := block["thinking"].(string); ok && block["type"] == "thinking" {
			s.session.MarkKept(text, s.kept)
		}
		if text, ok := block["text"].(string); ok && block["type"] == "text" && text != "" {
			block["text"] = s.push(index, "text_delta", text, false)
			return encodeEvent(event, value), true, nil
		}
	case "content_block_delta":
		delta, _ := value["delta"].(map[string]any)
		kind, ok := delta["type"].(string)
		if !ok {
			return nil, false, fmt.Errorf("%w: content_block_delta without a delta type", ErrFormat)
		}
		if text, ok := delta["thinking"].(string); ok && kind == "thinking_delta" {
			s.session.MarkKept(text, s.kept)
		}
		field := deltaFields[kind]
		text, ok := delta[field].(string)
		if field == "" || !ok {
			return []Event{event}, false, nil
		}
		delta[field] = s.push(index, kind, text, false)
		return encodeEvent(event, value), true, nil
	case "content_block_stop":
		var before []Event
		for _, kind := range []string{"text_delta", "input_json_delta"} {
			if s.streams[index+":"+kind] == nil {
				continue
			}
			if tail := s.push(index, kind, "", true); tail != "" {
				before = append(before, encodeEvent(Event{Name: "content_block_delta"}, map[string]any{"type": "content_block_delta", "index": value["index"], "delta": map[string]any{"type": kind, deltaFields[kind]: tail}})...)
			}
		}
		return append(before, event), len(before) > 0, nil
	}
	return []Event{event}, false, nil
}

func (s *agentStream) open() int { return len(s.streams) + len(s.tools) }

// finish sends what open blocks still hold back when the stream ends without closing them.
func (s *agentStream) finish() []Event {
	var events []Event
	for _, key := range slices.Sorted(maps.Keys(s.streams)) {
		index, kind, _ := strings.Cut(key, ":")
		tail := s.push(index, kind, "", true)
		if tail == "" {
			continue
		}
		var position any = index
		if json.Valid([]byte(index)) {
			position = json.RawMessage(index)
		}
		events = append(events, encodeEvent(Event{Name: "content_block_delta"}, map[string]any{"type": "content_block_delta", "index": position, "delta": map[string]any{"type": kind, deltaFields[kind]: tail}})...)
	}
	return events
}

func decodeJSONObject(data string) (map[string]any, bool) {
	value, err := decodeExact(data)
	if err != nil {
		return nil, false
	}
	object, ok := value.(map[string]any)
	return object, ok
}

func encodeEvent(original Event, value map[string]any) []Event {
	data, err := encodeJSON(value)
	if err != nil {
		return []Event{original}
	}
	return []Event{{Name: original.Name, Data: string(data)}}
}

func RestoreAgentStream(input io.Reader, output io.Writer, session *mask.Session, maxEvent int) (StreamStats, error) {
	state := &agentStream{session: session, streams: make(map[string]*mask.Stream), tools: make(map[string]any), kept: make(map[string]bool)}
	stats, err := pipeSSE(input, output, maxEvent, sseHandler{state.transform, state.finish, state.open, func() bool { return state.completed }})
	stats.InputTokens, stats.Withheld, stats.Kept = state.inputTokens, state.withheld, state.kept
	return stats, err
}
