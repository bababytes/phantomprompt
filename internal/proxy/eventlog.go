package proxy

import (
	"context"
	"log/slog"

	"phantomprompt/internal/applog"
)

func logEvent(log *applog.Log, event Event) {
	level, message, ok := eventImportance(event)
	if !ok || log == nil {
		return
	}
	attrs := []slog.Attr{slog.String("reason", event.Reason)}
	if event.Phase != "control" {
		attrs = append(attrs, slog.Int("status", event.Status), slog.String("service", event.Adapter), slog.String("route", event.Route))
	}
	if event.Unrestored > 0 {
		attrs = append(attrs, slog.Int("unrestored", event.Unrestored))
	}
	if event.Folder != "" {
		attrs = append(attrs, slog.String("folder", event.Folder))
	}
	log.Logger().LogAttrs(context.Background(), level, message, attrs...)
}

func eventImportance(event Event) (slog.Level, string, bool) {
	switch {
	case event.Reason == "protection_disabled":
		return slog.LevelWarn, "protection turned off", true
	case event.Reason == "protection_enabled":
		return slog.LevelInfo, "protection turned on", true
	case event.Reason == "passed_without_restore":
		return slog.LevelError, "AI service returned an error", true
	case event.Status >= 500:
		return slog.LevelError, event.Phase + " failed", true
	case event.Status >= 400:
		return slog.LevelError, event.Phase + " blocked", true
	case event.Reason == "placeholders_not_restored":
		return slog.LevelWarn, "placeholders not restored", true
	case event.Reason == "secret_kept_masked_in_tool":
		return slog.LevelWarn, "secret kept masked in a tool call", true
	}
	return 0, "", false
}
