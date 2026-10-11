package proxy

import (
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"

	"phantomprompt/internal/mask"
	"phantomprompt/internal/protocol"
)

const maxRulesFile = 64 << 10

var ErrRulesFile = errors.New("saved masking rules are invalid")

func LoadRules(path string) (mask.Policy, bool, error) {
	file, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return mask.Policy{}, false, nil
	}
	if err != nil {
		return mask.Policy{}, false, err
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, maxRulesFile+1))
	var policy mask.Policy
	if err != nil || len(data) > maxRulesFile || protocol.Decode(data, &policy) != nil {
		return mask.Policy{}, false, ErrRulesFile
	}
	policy.DisabledSecrets = mask.KnownSecretIDs(policy.DisabledSecrets)
	if mask.ValidatePolicy(policy) != nil {
		return mask.Policy{}, false, ErrRulesFile
	}
	policy.LenientFields = false
	return policy, true, nil
}

func saveRules(path string, policy mask.Policy) error {
	data, err := json.MarshalIndent(policy, "", "  ")
	if err != nil {
		return err
	}
	directory := filepath.Dir(path)
	if err := os.MkdirAll(directory, 0700); err != nil {
		return err
	}
	file, err := os.CreateTemp(directory, ".rules-*.json")
	if err != nil {
		return err
	}
	temporary := file.Name()
	defer os.Remove(temporary)
	if _, err := file.Write(append(data, '\n')); err != nil {
		file.Close()
		return err
	}
	if err := file.Sync(); err != nil {
		file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	return os.Rename(temporary, path)
}

func (a *App) Policy() mask.Policy {
	a.policyMu.RLock()
	defer a.policyMu.RUnlock()
	return clonePolicy(a.policy)
}

func (a *App) StartupPolicy() (mask.Policy, error) { return FieldPolicy(a.Config) }

func (a *App) SetPolicy(policy mask.Policy) error {
	a.editMu.Lock()
	defer a.editMu.Unlock()
	return a.setPolicy(policy)
}

// UpdatePolicy changes the current rules in place; edits from the panel and the CLI do not
// overwrite each other.
func (a *App) UpdatePolicy(change func(*mask.Policy)) error {
	a.editMu.Lock()
	defer a.editMu.Unlock()
	policy := a.Policy()
	change(&policy)
	return a.setPolicy(policy)
}

// SetTrustedPath adds a folder whose content is sent unmasked, or removes it.
// SetTrustedPath adds a folder whose content is sent unmasked, or removes it. The home folder is
// refused: trusting it would switch masking off for nearly everything.
func (a *App) SetTrustedPath(path string, trusted bool) error {
	target, err := expandHome(path)
	if err != nil {
		return err
	}
	if home, err := os.UserHomeDir(); trusted && (err != nil || target == filepath.Clean(home)) {
		return mask.ErrPaths
	}
	return a.UpdatePolicy(func(policy *mask.Policy) {
		policy.TrustedPaths = slices.DeleteFunc(policy.TrustedPaths, func(entry string) bool {
			expanded, err := expandHome(entry)
			return err == nil && expanded == target
		})
		if trusted {
			policy.TrustedPaths = append(policy.TrustedPaths, path)
		}
	})
}

func (a *App) setPolicy(policy mask.Policy) error {
	policy = clonePolicy(policy)
	policy.LenientFields = false
	if err := mask.ValidatePolicy(policy); err != nil {
		return err
	}
	if err := a.storePolicy(policy); err != nil {
		return err
	}
	a.refreshProtectedFiles(true)
	return nil
}

func (a *App) storePolicy(policy mask.Policy) error {
	a.policyMu.Lock()
	defer a.policyMu.Unlock()
	if a.rulesFile != "" {
		if err := saveRules(a.rulesFile, policy); err != nil {
			return err
		}
	}
	a.policy = policy
	return a.Sessions.SetPolicy(policy)
}

func clonePolicy(policy mask.Policy) mask.Policy {
	policy.CustomFields = append([]string(nil), policy.CustomFields...)
	policy.DisabledFields = append([]string(nil), policy.DisabledFields...)
	policy.DisabledSecrets = append([]string(nil), policy.DisabledSecrets...)
	policy.CustomRules = append([]mask.RegexRule(nil), policy.CustomRules...)
	policy.ProtectedPaths = append([]string(nil), policy.ProtectedPaths...)
	policy.BlockedPaths = append([]string(nil), policy.BlockedPaths...)
	policy.TrustedPaths = append([]string(nil), policy.TrustedPaths...)
	return policy
}
