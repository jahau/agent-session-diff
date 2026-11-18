package parse

import (
	"encoding/json"
	"time"
)

// obj is a JSON object decoded lazily. Every accessor tolerates missing keys
// and wrong value types by returning the zero value and ok=false, so one odd
// field never discards a whole record.
type obj map[string]json.RawMessage

func decodeObj(raw []byte) (obj, bool) {
	if len(raw) == 0 {
		return nil, false
	}
	var o obj
	if err := json.Unmarshal(raw, &o); err != nil || o == nil {
		return nil, false
	}
	return o, true
}

func (o obj) str(key string) (string, bool) {
	raw, ok := o[key]
	if !ok {
		return "", false
	}
	var s string
	if json.Unmarshal(raw, &s) != nil {
		return "", false
	}
	return s, true
}

func (o obj) s(key string) string { v, _ := o.str(key); return v }

func (o obj) boolean(key string) (bool, bool) {
	raw, ok := o[key]
	if !ok {
		return false, false
	}
	var b bool
	if json.Unmarshal(raw, &b) != nil {
		return false, false
	}
	return b, true
}

func (o obj) integer(key string) (int, bool) {
	raw, ok := o[key]
	if !ok {
		return 0, false
	}
	var f float64
	if json.Unmarshal(raw, &f) != nil {
		return 0, false
	}
	if f != float64(int(f)) {
		return 0, false
	}
	return int(f), true
}

func (o obj) number(key string) (float64, bool) {
	raw, ok := o[key]
	if !ok {
		return 0, false
	}
	var f float64
	if json.Unmarshal(raw, &f) != nil {
		return 0, false
	}
	return f, true
}

func (o obj) child(key string) (obj, bool) { return decodeObj(o[key]) }

func (o obj) array(key string) ([]json.RawMessage, bool) {
	raw, ok := o[key]
	if !ok {
		return nil, false
	}
	var a []json.RawMessage
	if json.Unmarshal(raw, &a) != nil {
		return nil, false
	}
	return a, true
}

func (o obj) strings(key string) ([]string, bool) {
	raw, ok := o[key]
	if !ok {
		return nil, false
	}
	var a []string
	if json.Unmarshal(raw, &a) != nil {
		return nil, false
	}
	return a, true
}

// timestamp parses an RFC 3339 timestamp field. Only string timestamps are
// accepted; anything else is treated as absent.
func (o obj) timestamp(key string) (*time.Time, bool) {
	s, ok := o.str(key)
	if !ok || s == "" {
		return nil, false
	}
	t, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		return nil, false
	}
	t = t.UTC()
	return &t, true
}

// textOf flattens a tool-result content value: a plain string, or an array of
// {type:"text"|"input_text"|"output_text", text:"..."} blocks.
func textOf(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	var blocks []json.RawMessage
	if json.Unmarshal(raw, &blocks) != nil {
		return ""
	}
	out := ""
	for _, b := range blocks {
		bo, ok := decodeObj(b)
		if !ok {
			continue
		}
		if t, ok := bo.str("text"); ok {
			if out != "" {
				out += "\n"
			}
			out += t
		}
	}
	return out
}
