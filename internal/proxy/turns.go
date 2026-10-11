package proxy

import (
	"cmp"
	"encoding/json"
	"net/http"
	"sync"
	"time"
)

const quietGap = 10 * time.Second

type turnGroup struct {
	row     uint64
	prompt  string
	counted bool
	lastEnd time.Time
	pending *Event
}

type turnSignal struct {
	prompt     string
	background bool
	newTurn    bool
}

var backgroundRequests = map[string]bool{
	"auxiliary": true, "subagent": true, "workflow": true, "compaction": true,
	"conversation-subagent": true, "conversation-sampling": true, "conversation-background": true, "conversation-compaction": true,
}

func clientTurn(adapter string, header http.Header, newTurn bool) turnSignal {
	turn := turnSignal{newTurn: newTurn}
	switch adapter {
	case ClaudeCode:
		turn.prompt = header.Get("X-Claude-Code-Prompt-Id")
		turn.background = backgroundRequests[header.Get("X-Claude-Code-Request-Class")]
	case CopilotCLI:
		turn.prompt = header.Get("X-Interaction-Id")
		turn.background = backgroundRequests[header.Get("X-Interaction-Type")]
	case CodexCLI:
		var metadata struct {
			TurnID     string `json:"turn_id"`
			RootTurnID string `json:"root_turn_id"`
		}
		json.Unmarshal([]byte(header.Get("X-Codex-Turn-Metadata")), &metadata)
		turn.prompt = cmp.Or(metadata.RootTurnID, metadata.TurnID)
		turn.background = header.Get("X-Openai-Subagent") != ""
	}
	if len(turn.prompt) > 128 {
		turn.prompt = ""
	}
	return turn
}

type turnLog struct {
	mu     sync.Mutex
	groups map[string]*turnGroup
	now    func() time.Time
}

func mergeTurn(row *Event, event Event) {
	row.Matches = row.Matches.Add(event.Matches)
	for name, use := range event.Rules {
		if row.Rules == nil {
			row.Rules = make(map[string]RuleUse)
		}
		row.Rules[name] = row.Rules[name].add(use)
	}
	row.Requests++
	row.InputBytes += event.InputBytes
	row.MaskedBytes += event.MaskedBytes
	row.InputTokens += event.InputTokens
	row.ElapsedMS += event.ElapsedMS
	row.OverheadMS += event.OverheadMS
	row.MaskMS += event.MaskMS
	row.Stream = row.Stream || event.Stream
	if event.Status >= 400 {
		row.AIErrors++
	}
	row.Status, row.Reason = event.Status, event.Reason
	if row.Folder == "" {
		row.Folder = event.Folder
	}
}

func (a *App) logTurn(event Event, session string, turn turnSignal) {
	event.Folder = a.cliSessions.folder(session)
	logEvent(a.Events.log, event)
	aiError := event.Status >= 400 && event.Reason == "passed_without_restore"
	if event.Status >= 400 && !aiError || session == "" {
		a.keepPrompt(event)
		return
	}
	if aiError && turn.prompt == "" {
		return
	}
	a.turns.mu.Lock()
	defer a.turns.mu.Unlock()
	now := time.Now()
	if a.turns.now != nil {
		now = a.turns.now()
	}
	if a.turns.groups == nil {
		a.turns.groups = make(map[string]*turnGroup)
	}
	start := now.Add(-time.Duration(event.ElapsedMS * float64(time.Millisecond)))
	group := a.turns.groups[session]
	same := group != nil && (turn.prompt != "" && turn.prompt == group.prompt || turn.prompt == "" && (!turn.newTurn || start.Sub(group.lastEnd) <= quietGap))
	if !same {
		group = &turnGroup{prompt: turn.prompt}
		a.turns.groups[session] = group
	}
	group.lastEnd = now
	if !group.counted && !turn.background && (turn.prompt != "" || turn.newTurn) {
		group.counted = true
		a.countPrompt(session, event.Adapter)
	}
	if group.row != 0 && a.Events.Update(group.row, func(row *Event) { mergeTurn(row, event) }) {
		return
	}
	if group.pending == nil {
		group.pending = &Event{Phase: event.Phase, Adapter: event.Adapter, Route: event.Route, Folder: event.Folder}
	}
	mergeTurn(group.pending, event)
	if event.Matches.New == 0 && !event.Matches.FolderAccess() && event.Matches.Attachments == 0 {
		return
	}
	group.row = a.Events.keep(*group.pending)
	group.pending = nil
}

func (a *App) countPrompt(session, adapter string) {
	a.PromptsSent.Add(1)
	service := cliIntegrations[adapter].Service
	a.cliSessions.record(session, service, func(s *CLISession) { s.Prompts++ })
	a.detected.record(service, true, func(i *Integration) { i.Prompts++ })
}

func (a *App) forgetTurns(session string) {
	a.turns.mu.Lock()
	defer a.turns.mu.Unlock()
	delete(a.turns.groups, session)
}
