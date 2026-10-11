package proxy

import (
	"sort"
	"time"
)

var formatReasons = map[string]bool{
	"unsupported_content":                        true,
	"unsupported_content_or_masking_limit":       true,
	"unsupported_response_content":               true,
	"unsupported_upstream_response":              true,
	"compressed_upstream_response_not_supported": true,
	"SSE_required":                               true,
	"JSON_response_required":                     true,
	"new_content_type_masked":                    true,
}

type FormatAlert struct {
	Adapter string    `json:"adapter"`
	Route   string    `json:"route"`
	Reason  string    `json:"reason"`
	Detail  string    `json:"detail,omitempty"`
	Count   int       `json:"count"`
	First   time.Time `json:"first"`
	Last    time.Time `json:"last"`
}

func (a *App) FormatAlerts() []FormatAlert {
	notes := make(map[string]string)
	for _, shape := range a.Discovery() {
		if shape.Direction == "request" && shape.Note != "" {
			notes[shape.Path] = shape.Note
		}
	}
	groups := make(map[[3]string]*FormatAlert)
	for _, event := range a.Events.Snapshot() {
		if !formatReasons[event.Reason] || event.Adapter == "demo" {
			continue
		}
		key := [3]string{event.Adapter, event.Route, event.Reason}
		alert := groups[key]
		if alert == nil {
			detail := notes[event.Route]
			if site := notes[event.Adapter+":"+event.Route]; site != "" {
				detail = site
			}
			alert = &FormatAlert{Adapter: event.Adapter, Route: event.Route, Reason: event.Reason, Detail: detail, First: event.Time}
			groups[key] = alert
		}
		alert.Count += 1 + event.Repeats
		alert.Last = event.Time
	}
	alerts := make([]FormatAlert, 0, len(groups))
	for _, alert := range groups {
		alerts = append(alerts, *alert)
	}
	sort.Slice(alerts, func(i, j int) bool {
		if !alerts[i].Last.Equal(alerts[j].Last) {
			return alerts[i].Last.After(alerts[j].Last)
		}
		return alerts[i].Adapter+alerts[i].Route+alerts[i].Reason < alerts[j].Adapter+alerts[j].Route+alerts[j].Reason
	})
	return alerts
}
