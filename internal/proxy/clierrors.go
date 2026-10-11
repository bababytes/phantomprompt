package proxy

import (
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"

	"phantomprompt/internal/protocol"
)

// blockHints are shown by the coding tool, so they say what happened and what to do next.
var blockHints = map[string]string{
	"session_expired_restart_cli":          "the protected session ended or PhantomPrompt restarted. Exit and start the command again with phantom",
	"session_unavailable":                  "the protected session ended or PhantomPrompt restarted. Exit and start the command again with phantom",
	"unsupported_content":                  "the request has content PhantomPrompt cannot inspect yet, so nothing was sent. See the format alert in the PhantomPrompt panel",
	"attachments_not_supported":            "audio and unknown attachments cannot be checked yet, so nothing was sent",
	"masking_limit":                        "this conversation holds too many protected values. Start a new conversation",
	"body_limit":                           "the request is larger than PhantomPrompt accepts. Compact the conversation",
	"invalid_compression":                  "the request body could not be decompressed, so nothing was sent",
	"unsupported_encoding":                 "the request uses an unsupported encoding, so nothing was sent",
	"sensitive_value_not_masked_yet":       "a protected value was found in a request PhantomPrompt cannot mask, so it was blocked",
	"sensitive_value_outside_prompt":       "a protected value was found outside the prompt, so the request was blocked",
	"upgrade_not_supported":                "WebSocket connections are not supported",
	"concurrency_limit":                    "too many requests are in flight. Retrying shortly",
	"upstream_TLS_or_connection_failed":    "PhantomPrompt could not reach the AI service",
	"response_limit":                       "the response was too large to restore",
	"unsupported_response_content":         "PhantomPrompt could not restore the response",
	"destination_not_allowed":              "this AI service is not supported by PhantomPrompt, so it is blocked",
	"tunnel_target_mismatch":               "the request did not match its connection target",
	"unsupported_content_or_masking_limit": "the request could not be masked, so nothing was sent",
	"blocked_folder_content":               "the message contains content from a blocked folder, so nothing was sent",
	"stream_interrupted":                   "the AI service's answer ended early or could not be restored, so it is incomplete. Ask again",
	"protected_folder_not_indexed":         "a protected folder could not be indexed completely, so nothing was sent. See its status in the PhantomPrompt panel and fix or remove it",
}

var errorTypes = map[int]string{403: "permission_error", 404: "not_found_error", 413: "request_too_large", 502: "api_error", 503: "api_error"}

// cliFailure answers in the error format of the client's API, so Claude Code and Copilot show the
// reason, and tells them whether retrying can help (x-should-retry).
func cliFailure(r *http.Request, code int, reason string) *http.Response {
	body, _ := json.Marshal(failurePayload(r, code, reason))
	header := http.Header{"Content-Type": {"application/json"}, "Cache-Control": {"no-store"}, "X-Should-Retry": {"false"}}
	if code == http.StatusServiceUnavailable {
		header.Set("X-Should-Retry", "true")
		header.Set("Retry-After", "1")
	}
	return &http.Response{StatusCode: code, Status: strconv.Itoa(code) + " " + http.StatusText(code), Proto: "HTTP/1.1", ProtoMajor: 1, ProtoMinor: 1, Request: r, Header: header, Body: io.NopCloser(strings.NewReader(string(body))), ContentLength: int64(len(body)), Close: true}
}

func failurePayload(r *http.Request, code int, reason string) map[string]any {
	hint := blockHints[reason]
	if hint == "" {
		hint = "the request was blocked"
	}
	message := "PhantomPrompt: " + hint + " (" + reason + ")."
	kind := errorTypes[code]
	if kind == "" {
		kind = "invalid_request_error"
	}
	if r != nil && r.URL != nil && (strings.HasSuffix(r.URL.Path, "/chat/completions") || strings.HasSuffix(r.URL.Path, "/responses")) {
		return map[string]any{"error": map[string]string{"type": kind, "code": reason, "message": message}}
	}
	return map[string]any{"type": "error", "error": map[string]string{"type": kind, "message": message}}
}

// streamFailure ends a broken answer stream with an error event the client shows, instead of a
// stream that just stops.
func streamFailure(r *http.Request) protocol.Event {
	reason := "stream_interrupted"
	payload := failurePayload(r, http.StatusBadGateway, reason)
	if r != nil && r.URL != nil && strings.HasSuffix(r.URL.Path, "/responses") {
		payload = map[string]any{"type": "error", "code": reason, "message": payload["error"].(map[string]string)["message"]}
	}
	data, _ := json.Marshal(payload)
	if r != nil && r.URL != nil && strings.HasSuffix(r.URL.Path, "/chat/completions") {
		return protocol.Event{Data: string(data)}
	}
	return protocol.Event{Name: "error", Data: string(data)}
}
