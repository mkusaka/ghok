package protocol

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"regexp"
	"strings"
)

const TransformPathPattern = `^/operations/(0|[1-9][0-9]*)/(texts/(title|body|notes)/value|parameters/([^/~]|~[01])+|target/(repository|number))$`

var transformPath = regexp.MustCompile(TransformPathPattern)

// ValidateTransformPath accepts only the editable JSON pointer namespace.
func ValidateTransformPath(path string) error {
	if !transformPath.MatchString(path) {
		return fmt.Errorf("invalid transform path %q", path)
	}
	return nil
}

// ParseTransformResponse rejects unknown fields, duplicate keys, and trailing
// documents. Empty stdout is the only response without an explicit version.
func ParseTransformResponse(data []byte) (TransformResponse, error) {
	response := TransformResponse{SchemaVersion: 1}
	if len(bytes.TrimSpace(data)) == 0 {
		return response, nil
	}
	scan := json.NewDecoder(bytes.NewReader(data))
	scan.UseNumber()
	if err := uniqueJSON(scan); err != nil {
		return TransformResponse{}, err
	}
	if _, err := scan.Token(); err != io.EOF {
		return TransformResponse{}, fmt.Errorf("trailing data after transform response")
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	decoder.UseNumber()
	response.SchemaVersion = 0
	if err := decoder.Decode(&response); err != nil {
		return TransformResponse{}, err
	}
	if response.SchemaVersion != 1 {
		return TransformResponse{}, fmt.Errorf("transform schemaVersion must be 1")
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return TransformResponse{}, err
	}
	for _, key := range []string{"set", "unset"} {
		if raw, exists := fields[key]; exists && bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
			return TransformResponse{}, fmt.Errorf("transform %s cannot be null", key)
		}
	}
	seen := make(map[string]bool, len(response.Set)+len(response.Unset))
	for path := range response.Set {
		if err := ValidateTransformPath(path); err != nil {
			return TransformResponse{}, err
		}
		seen[path] = true
	}
	for _, path := range response.Unset {
		if err := ValidateTransformPath(path); err != nil {
			return TransformResponse{}, err
		}
		if seen[path] {
			return TransformResponse{}, fmt.Errorf("duplicate transform path %q", path)
		}
		seen[path] = true
	}
	return response, nil
}

func uniqueJSON(d *json.Decoder) error {
	token, err := d.Token()
	if err != nil {
		return err
	}
	delim, ok := token.(json.Delim)
	if !ok {
		return nil
	}
	switch delim {
	case '{':
		keys := map[string]bool{}
		for d.More() {
			token, err := d.Token()
			if err != nil {
				return err
			}
			key, ok := token.(string)
			if !ok || keys[key] {
				return fmt.Errorf("duplicate or invalid JSON key %q", token)
			}
			keys[key] = true
			if err := uniqueJSON(d); err != nil {
				return err
			}
		}
	case '[':
		for d.More() {
			if err := uniqueJSON(d); err != nil {
				return err
			}
		}
	default:
		return fmt.Errorf("unexpected JSON delimiter %s", strings.TrimSpace(fmt.Sprint(delim)))
	}
	_, err = d.Token()
	return err
}
