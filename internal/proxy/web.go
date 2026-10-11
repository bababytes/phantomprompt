package proxy

import (
	"errors"
	"net"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"phantomprompt/internal/mask"
	"phantomprompt/internal/protocol"
)

var aiServices = map[string]string{
	"gemini.google.com":                 "Gemini web",
	"aistudio.google.com":               "Google AI Studio",
	"notebooklm.google.com":             "NotebookLM",
	"generativelanguage.googleapis.com": "Gemini API",
	"cloudcode-pa.googleapis.com":       "Gemini CLI",
	"aiplatform.googleapis.com":         "Vertex AI",
	"chatgpt.com":                       "ChatGPT web",
	"chat.openai.com":                   "ChatGPT web",
	"api.openai.com":                    "OpenAI API",
	"openai.com":                        "OpenAI",
	"openai.azure.com":                  "Azure OpenAI",
	"claude.ai":                         "Claude web",
	"api.anthropic.com":                 "Anthropic API",
	"anthropic.com":                     "Anthropic",
	"copilot.microsoft.com":             "Microsoft Copilot web",
	"githubcopilot.com":                 "GitHub Copilot API",
	"mistral.ai":                        "Mistral",
	"api.groq.com":                      "Groq",
	"deepseek.com":                      "DeepSeek",
	"x.ai":                              "xAI",
	"perplexity.ai":                     "Perplexity",
	"openrouter.ai":                     "OpenRouter",
	"cohere.com":                        "Cohere",
	"cohere.ai":                         "Cohere",
	"together.xyz":                      "Together AI",
}

func aiService(name string) (string, bool) {
	for domain := name; ; {
		if service, known := aiServices[domain]; known {
			return service, true
		}
		_, parent, found := strings.Cut(domain, ".")
		if !found {
			break
		}
		domain = parent
	}
	switch {
	case strings.HasSuffix(name, "-aiplatform.googleapis.com"):
		return "Vertex AI", true
	case strings.HasPrefix(name, "bedrock-runtime.") && strings.HasSuffix(name, ".amazonaws.com"):
		return "Amazon Bedrock", true
	}
	return "", false
}

var rejectedDomains = []string{"content-push.googleapis.com", "sentry.io"}

type Integration struct {
	Service   string     `json:"service"`
	Protected bool       `json:"protected"`
	Requests  uint64     `json:"requests"`
	Blocked   uint64     `json:"blocked"`
	LastSeen  *time.Time `json:"last_seen,omitempty"`
}

type detections struct {
	mu    sync.Mutex
	items map[string]*Integration
}

func (d *detections) record(service string, protected bool, update func(*Integration)) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.items == nil {
		d.items = make(map[string]*Integration)
	}
	item := d.items[service]
	if item == nil {
		item = &Integration{Service: service, Protected: protected}
		d.items[service] = item
	}
	now := time.Now().UTC()
	item.LastSeen = &now
	update(item)
}

func (a *App) Integrations() []Integration {
	a.detected.mu.Lock()
	defer a.detected.mu.Unlock()
	result := make([]Integration, 0, len(a.detected.items))
	for _, item := range a.detected.items {
		result = append(result, *item)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Service < result[j].Service })
	return result
}

func (a *App) connectPolicy(host string) (adapter string, inspect, tunnel bool) {
	if a.allowedTunnel(host) {
		return "", true, false
	}
	name, port, err := net.SplitHostPort(host)
	if err != nil {
		return "", false, false
	}
	name = strings.ToLower(name)
	if port == "443" {
		for _, adapter := range a.Config.Integrations {
			if cli, ok := cliIntegrations[adapter]; ok && cli.Inspect(name) {
				return adapter, true, false
			}
		}
		// CLI login and asset hosts tunnel even when they are also recognized AI web UIs (e.g. claude.ai).
		for _, adapter := range a.Config.Integrations {
			if cli, ok := cliIntegrations[adapter]; ok && cli.Support(name) {
				return "", false, true
			}
		}
	}
	return "", false, a.passThrough(name)
}

func (a *App) passThrough(name string) bool {
	if service, known := aiService(name); known {
		a.Blocked.Add(1)
		a.detected.record(service, false, func(i *Integration) { i.Blocked++ })
		return false
	}
	return !slices.ContainsFunc(rejectedDomains, func(domain string) bool { return name == domain || strings.HasSuffix(name, "."+domain) })
}

func webRejection(err error) (int, string) {
	switch {
	case errors.Is(err, protocol.ErrAttachment):
		return 422, "attachments_not_supported"
	case errors.Is(err, protocol.ErrExposed):
		return 403, "sensitive_value_outside_prompt"
	case errors.Is(err, mask.ErrToken):
		return 422, "unknown_token_in_input"
	case errors.Is(err, mask.ErrLimit):
		return 422, "masking_limit"
	case errors.Is(err, mask.ErrSession):
		return 409, "session_unavailable"
	case errors.Is(err, mask.ErrFolders):
		return 409, "protected_folder_not_indexed"
	}
	return 422, "unsupported_content"
}
