package apiargs

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
)

// DecodeObject retains JSON number spelling so a rewritten neighbouring key
// cannot round large integers or alter an otherwise untouched request value.
func DecodeObject(data []byte) (map[string]any, error) {
	var object map[string]any
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	if err := decoder.Decode(&object); err != nil {
		return nil, err
	}
	if object == nil {
		return nil, fmt.Errorf("input body must be an object")
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		if err == nil {
			return nil, fmt.Errorf("multiple JSON values")
		}
		return nil, err
	}
	return object, nil
}
