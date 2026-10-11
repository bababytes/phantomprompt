package demo

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"unicode/utf8"

	"phantomprompt/internal/mask"
	"phantomprompt/internal/protocol"
)

func labError(err error) string {
	switch {
	case errors.Is(err, mask.ErrRegex):
		return "Invalid regex: use a valid Go (RE2) pattern of at most 512 bytes that cannot match empty text"
	case errors.Is(err, mask.ErrFields):
		return "Invalid field name or ambiguous field value in the sample text"
	case errors.Is(err, mask.ErrToken):
		return "The sample text contains an unknown PhantomPrompt token"
	case errors.Is(err, mask.ErrLimit):
		return "Masking limit reached; shorten the sample text"
	}
	return "Invalid rules"
}

func regexLab(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST required", 405)
		return
	}
	data, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 128<<10))
	if err != nil {
		http.Error(w, "Lab request too large", 413)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	switch r.URL.Path {
	case "/api/regex/generate":
		var request struct {
			Text          string `json:"text"`
			CaseSensitive bool   `json:"case_sensitive"`
			Shape         bool   `json:"shape"`
		}
		if protocol.Decode(data, &request) != nil {
			http.Error(w, "Invalid generator request", 400)
			return
		}
		pattern, description, err := generatePattern(request.Text, request.CaseSensitive, request.Shape)
		switch {
		case errors.Is(err, mask.ErrShape):
			http.Error(w, "This example has no layout to copy: use one with digits or separators such as - _ . / (for example abc-x7k2-91), or choose “Exactly this text”", 422)
			return
		case err != nil:
			http.Error(w, "Enter a non-empty example of at most 128 UTF-8 bytes", 422)
			return
		}
		json.NewEncoder(w).Encode(map[string]string{"pattern": pattern, "description": description})
	case "/api/regex/test":
		var request struct {
			Text   string      `json:"text"`
			Policy mask.Policy `json:"policy"`
		}
		if protocol.Decode(data, &request) != nil || len(request.Text) > 64<<10 || !utf8.ValidString(request.Text) {
			http.Error(w, "Invalid preview; text limit is 64 KiB", 422)
			return
		}
		session := mask.NewSession(4096, 512<<10, request.Policy)
		defer session.Clear()
		masked, counts, err := session.Mask([]string{request.Text})
		if err != nil {
			http.Error(w, labError(err), 422)
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"masked": masked[0], "restored": session.Restore(masked[0]), "matches": counts, "total": counts.Total()})
	default:
		http.NotFound(w, r)
	}
}

func generatePattern(text string, caseSensitive, shape bool) (string, string, error) {
	if shape {
		return mask.ShapePattern(text, caseSensitive)
	}
	pattern, err := mask.LiteralPattern(text, caseSensitive)
	if err != nil {
		return "", "", err
	}
	letterCase := ", same letter case"
	if !caseSensitive {
		letterCase = ", any letter case"
	}
	return pattern, "the exact text “" + text + "”" + letterCase, nil
}
