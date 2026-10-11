package mask

import "strings"

// maxScanText keeps each scanned piece of a file under the number of matches one text may hold.
const maxScanText = 64 << 10

// ValueScanner finds the values a policy masks in file content, so that the values of a trusted
// folder can be sent as they are.
type ValueScanner struct {
	session *Session
}

func NewValueScanner(policy Policy) (*ValueScanner, error) {
	policy = policy.clone()
	policy.LenientFields = true
	session := NewSession(1, 1, policy)
	if session.initErr != nil {
		return nil, session.initErr
	}
	return &ValueScanner{session: session}, nil
}

// Values returns the distinct values found in text. A piece that cannot be scanned is skipped,
// and its values stay masked.
func (v *ValueScanner) Values(text string) []string {
	seen := make(map[string]bool)
	var values []string
	for text != "" {
		piece := text
		if len(piece) > maxScanText {
			piece = piece[:strings.LastIndexByte(piece[:maxScanText], '\n')+1]
			if piece == "" {
				piece = text[:maxScanText]
			}
		}
		text = text[len(piece):]
		matches, err := v.session.matches(piece)
		if err != nil {
			continue
		}
		for _, m := range matches {
			if value := piece[m.Start:m.End]; m.Kind != "FILE" && !seen[value] {
				seen[value] = true
				values = append(values, value)
			}
		}
	}
	return values
}
