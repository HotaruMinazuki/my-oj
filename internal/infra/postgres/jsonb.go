package postgres

import (
	"encoding/json"
	"fmt"
)

// scanJSONB unmarshals a PostgreSQL JSONB column value into dst.
// Handles SQL NULL (src == nil) gracefully by leaving dst unchanged.
// Accepts both []byte (lib/pq) and string representations of JSON.
func scanJSONB[T any](src any, dst *T) error {
	if src == nil {
		return nil
	}
	var b []byte
	switch v := src.(type) {
	case []byte:
		b = v
	case string:
		b = []byte(v)
	default:
		return fmt.Errorf("scanJSONB: unsupported source type %T", src)
	}
	return json.Unmarshal(b, dst)
}

// marshalJSONB serialises v to a JSON []byte suitable for a JSONB parameter.
// Returns nil (SQL NULL) when v marshals to the JSON literal "null" — this
// keeps nullable JSONB columns as SQL NULL rather than the JSON value null.
func marshalJSONB(v any) ([]byte, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	if string(b) == "null" {
		return nil, nil
	}
	return b, nil
}
