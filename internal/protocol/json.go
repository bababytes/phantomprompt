package protocol

import (
	"bytes"
	"encoding/json"
	"io"
	"strings"
)

func decodeExact(text string) (any, error) {
	decoder := json.NewDecoder(strings.NewReader(text))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return nil, ErrFormat
	}
	if _, err := decoder.Token(); err != io.EOF {
		return nil, ErrFormat
	}
	return value, nil
}

func encodeJSON(value any) ([]byte, error) {
	var b bytes.Buffer
	encoder := json.NewEncoder(&b)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(value); err != nil {
		return nil, err
	}
	return bytes.TrimSuffix(b.Bytes(), []byte("\n")), nil
}
