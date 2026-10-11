package proxy

import (
	"sync"
	"time"
)

const quietGap = 10 * time.Second

type turnGroup struct {
	row     uint64
	prompt  string
	lastEnd time.Time
	pending *Event
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
	if row.Folder == "" {
		row.Folder = event.Folder
	}
}

func (a *App) logTurn(event Event, session, prompt string, newTurn bool) {
	event.Folder = a.cliSessions.folder(session)
	logEvent(a.Events.log, event)
	if event.Status >= 400 || session == "" {
		a.keepPrompt(event)
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
	same := group != nil && (!newTurn || prompt != "" && prompt == group.prompt || start.Sub(group.lastEnd) <= quietGap)
	if !same {
		group = &turnGroup{}
		a.turns.groups[session] = group
	}
	if group.prompt == "" {
		group.prompt = prompt
	}
	group.lastEnd = now
	if group.row != 0 && a.Events.Update(group.row, func(row *Event) { mergeTurn(row, event) }) {
		return
	}
	if group.pending == nil {
		group.pending = &Event{Phase: event.Phase, Adapter: event.Adapter, Route: event.Route, Status: event.Status, Reason: event.Reason, Folder: event.Folder}
	}
	mergeTurn(group.pending, event)
	if event.Matches.New == 0 && !event.Matches.FolderAccess() {
		return
	}
	group.pending.Status, group.pending.Reason = event.Status, event.Reason
	group.row = a.Events.keep(*group.pending)
	group.pending = nil
}

func (a *App) forgetTurns(session string) {
	a.turns.mu.Lock()
	defer a.turns.mu.Unlock()
	delete(a.turns.groups, session)
}
