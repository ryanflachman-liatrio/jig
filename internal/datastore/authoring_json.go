package datastore

import (
	"bytes"
	"encoding/json"
	"io"
)

// CheckAuthoringJSON bounds JSON metadata containers before allocating typed
// records. Duplicate keys and excessive nesting fail closed.
func CheckAuthoringJSON(data []byte, limit int) error {
	if len(data) > limit {
		return ErrAuthoringStorage
	}
	d := json.NewDecoder(bytes.NewReader(data))
	type frame struct {
		kind  json.Delim
		count int
		keys  map[string]bool
		key   bool
	}
	var stack []frame
	for {
		tok, err := d.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return ErrAuthoringStorage
		}
		if len(stack) > 0 {
			f := &stack[len(stack)-1]
			if f.kind == '{' && f.key {
				if s, ok := tok.(string); ok {
					if f.keys[s] {
						return ErrAuthoringStorage
					}
					f.keys[s] = true
					f.key = false
					continue
				}
			}
			if delim, ok := tok.(json.Delim); !ok || delim != '}' && delim != ']' {
				f.count++
				if f.kind == '[' && f.count > 2048 {
					return ErrAuthoringStorage
				}
				if f.kind == '{' {
					f.key = true
				}
			}
		}
		if delim, ok := tok.(json.Delim); ok {
			if delim == '{' || delim == '[' {
				// The wire records have a finite known structure. A deeper
				// arbitrary JSON document is never part of the asset manifest contract.
				if len(stack) >= 64 {
					return ErrAuthoringStorage
				}
				stack = append(stack, frame{kind: delim, keys: map[string]bool{}, key: delim == '{'})
			} else {
				if len(stack) == 0 {
					return ErrAuthoringStorage
				}
				stack = stack[:len(stack)-1]
			}
		}
	}
	if !json.Valid(data) || len(stack) != 0 {
		return ErrAuthoringStorage
	}
	return nil
}
func DecodeAuthoringRecord(data []byte, limit int, v any) error {
	if err := CheckAuthoringJSON(data, limit); err != nil {
		return err
	}
	d := json.NewDecoder(bytes.NewReader(data))
	d.DisallowUnknownFields()
	if err := d.Decode(v); err != nil {
		return ErrAuthoringStorage
	}
	return nil
}
