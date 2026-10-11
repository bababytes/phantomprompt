package proxy

import (
	"errors"
	"net"
	"net/url"
	"strings"

	"phantomprompt/internal/mask"
	"phantomprompt/internal/protocol"
)

type Rule struct {
	Origin  string `json:"origin"`
	Path    string `json:"path"`
	Adapter string `json:"adapter"`
}
type Config struct {
	Rules           []Rule           `json:"rules"`
	Integrations    []string         `json:"integrations,omitempty"`
	GatewayOrigin   string           `json:"gateway_origin,omitempty"`
	Detectors       []string         `json:"detectors,omitempty"`
	SensitiveFields []string         `json:"sensitive_fields,omitempty"`
	CustomRules     []mask.RegexRule `json:"custom_rules,omitempty"`
}

func Policy(names []string) (mask.Policy, error) {
	if len(names) == 0 {
		return mask.DefaultPolicy(), nil
	}
	p := mask.Policy{}
	for _, name := range names {
		switch name {
		case "vin":
			p.VIN = true
		case "pesel":
			p.PESEL = true
		case "guid":
			p.GUID = true
		case "email":
			p.Email = true
		case "fields":
			p.Fields = true
		case "secrets":
			p.Secrets = true
		default:
			return p, errors.New("unknown detector")
		}
	}
	return p, nil
}

func (c Config) Validate() error {
	if len(c.Rules) == 0 && len(c.Integrations) == 0 || len(c.Rules) > 16 {
		return errors.New("configure up to 16 rules and at least one rule or browser integration")
	}
	enabled := map[string]bool{}
	for _, name := range c.Integrations {
		if !knownIntegration(name) || enabled[name] {
			return errors.New("unknown or duplicate integration")
		}
		enabled[name] = true
	}
	seen := map[string]bool{}
	for _, r := range c.Rules {
		u, err := url.Parse(r.Origin)
		if err != nil || u.Host == "" || u.User != nil || u.Path != "" || u.RawQuery != "" || u.Fragment != "" || u.Host != strings.ToLower(u.Host) {
			return errors.New("invalid rule origin")
		}
		if u.Scheme != "https" && !(u.Scheme == "http" && net.ParseIP(u.Hostname()).IsLoopback()) {
			return errors.New("upstream requires HTTPS except loopback tests")
		}
		if r.Path == "" || r.Path[0] != '/' || strings.ContainsAny(r.Path, "?#%") || strings.Contains(r.Path, "..") {
			return errors.New("invalid rule path")
		}
		if r.Adapter != protocol.Demo && r.Adapter != protocol.OpenAI && r.Adapter != protocol.Anthropic {
			return errors.New("unknown adapter")
		}
		key := r.Origin + r.Path
		if seen[key] {
			return errors.New("duplicate rule")
		}
		seen[key] = true
	}
	if c.GatewayOrigin != "" {
		found := false
		for _, r := range c.Rules {
			if r.Origin == c.GatewayOrigin {
				found = true
			}
		}
		if !found {
			return errors.New("gateway origin requires a rule")
		}
	}
	if _, err := FieldPolicy(c); err != nil {
		return err
	}
	return nil
}

func authority(u *url.URL) string {
	port := u.Port()
	if port == "" {
		port = "443"
		if u.Scheme == "http" {
			port = "80"
		}
	}
	return net.JoinHostPort(strings.ToLower(u.Hostname()), port)
}

func FieldPolicy(c Config) (mask.Policy, error) {
	p, err := Policy(c.Detectors)
	if err != nil {
		return p, err
	}
	if _, err = mask.FieldNames(c.SensitiveFields, nil); err != nil {
		return p, err
	}
	p.CustomFields = append([]string(nil), c.SensitiveFields...)
	if _, err = mask.CompileRules(c.CustomRules); err != nil {
		return p, err
	}
	p.CustomRules = append([]mask.RegexRule(nil), c.CustomRules...)
	return p, nil
}
