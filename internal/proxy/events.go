package proxy

import (
	"io"
	"maps"
	"sync"
	"time"

	"phantomprompt/internal/applog"
	"phantomprompt/internal/mask"
)

const maxEvents = 200
const repeatWindow = time.Minute

type Event struct {
	ID          uint64             `json:"id"`
	Time        time.Time          `json:"time"`
	Phase       string             `json:"phase"`
	Adapter     string             `json:"adapter"`
	Route       string             `json:"route,omitempty"`
	Status      int                `json:"status"`
	Reason      string             `json:"reason"`
	InputBytes  int                `json:"input_bytes"`
	MaskedBytes int                `json:"masked_bytes"`
	Matches     mask.Counts        `json:"matches"`
	MaskMS      float64            `json:"mask_ms"`
	ElapsedMS   float64            `json:"elapsed_ms"`
	OverheadMS  float64            `json:"overhead_ms"`
	Stream      bool               `json:"stream"`
	InputTokens int                `json:"input_tokens,omitempty"`
	Repeats     int                `json:"repeats,omitempty"`
	Unrestored  int                `json:"unrestored,omitempty"`
	Requests    int                `json:"requests,omitempty"`
	Folder      string             `json:"folder,omitempty"`
	Rules       map[string]RuleUse `json:"rules,omitempty"`
}

// Events contains metadata only; no payload, URL, header, session ID or regex is retained.
type Events struct {
	mu    sync.Mutex
	next  uint64
	items []Event
	log   *applog.Log
}

func sameBlock(a, b Event) bool {
	return a.Status >= 400 && a.Phase == b.Phase && a.Adapter == b.Adapter && a.Route == b.Route && a.Status == b.Status && a.Reason == b.Reason
}

// Add records an event in the history and, when it matters, in the app log.
func (e *Events) Add(event Event) uint64 {
	event.Time = time.Now().UTC()
	logEvent(e.log, event)
	return e.keep(event)
}

// keep adds an event to the history only; logTurn uses it for rows that merge events it already
// logged.
func (e *Events) keep(event Event) uint64 {
	e.mu.Lock()
	defer e.mu.Unlock()
	now := time.Now().UTC()
	for i := len(e.items) - 1; i >= 0 && now.Sub(e.items[i].Time) < repeatWindow; i-- {
		if sameBlock(event, e.items[i]) {
			merged := e.items[i]
			merged.Repeats++
			merged.Time = now
			e.items = append(append(e.items[:i:i], e.items[i+1:]...), merged)
			return merged.ID
		}
	}
	e.next++
	event.ID = e.next
	event.Time = now
	if len(e.items) == maxEvents {
		copy(e.items, e.items[1:])
		e.items[len(e.items)-1] = event
	} else {
		e.items = append(e.items, event)
	}
	return event.ID
}

func (e *Events) Update(id uint64, change func(*Event)) bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	for i := range e.items {
		if e.items[i].ID == id {
			change(&e.items[i])
			return true
		}
	}
	return false
}

type PromptStats struct {
	Checked           uint64  `json:"checked"`
	Sensitive         uint64  `json:"sensitive"`
	AverageOverheadMS float64 `json:"average_overhead_ms"`
}

type promptChecks struct {
	mu                        sync.Mutex
	checked, sensitive, timed uint64
	overheadMS                float64
}

func (p *promptChecks) check(sensitive bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.checked++
	if sensitive {
		p.sensitive++
	}
}

func (p *promptChecks) time(overheadMS float64) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.timed++
	p.overheadMS += overheadMS
}

func (p *promptChecks) Snapshot() PromptStats {
	p.mu.Lock()
	defer p.mu.Unlock()
	stats := PromptStats{Checked: p.checked, Sensitive: p.sensitive}
	if p.timed > 0 {
		stats.AverageOverheadMS = p.overheadMS / float64(p.timed)
	}
	return stats
}

func milliseconds(d time.Duration) float64 { return float64(d.Microseconds()) / 1000 }

type timedReader struct {
	io.Reader
	waited time.Duration
}

func (r *timedReader) Read(p []byte) (int, error) {
	started := time.Now()
	n, err := r.Reader.Read(p)
	r.waited += time.Since(started)
	return n, err
}

type timedWriter struct {
	io.Writer
	waited time.Duration
}

func (w *timedWriter) Write(p []byte) (int, error) {
	started := time.Now()
	n, err := w.Writer.Write(p)
	w.waited += time.Since(started)
	return n, err
}

func (e *Events) Snapshot() []Event {
	e.mu.Lock()
	defer e.mu.Unlock()
	items := make([]Event, len(e.items))
	for i, event := range e.items {
		items[i] = event.clone()
	}
	return items
}

func (e Event) clone() Event {
	e.Matches = mask.Counts{}.Add(e.Matches)
	e.Rules = maps.Clone(e.Rules)
	return e
}

func (e *Events) Clear() { e.mu.Lock(); defer e.mu.Unlock(); e.items = nil }
