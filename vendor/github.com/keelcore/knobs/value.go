package knobs

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"reflect"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"go.yaml.in/yaml/v3"
)

var (
	integerText  = regexp.MustCompile(`^-?[0-9]+$`)
	numberText   = regexp.MustCompile(`^-?[0-9]+(\.[0-9]+)?([eE][-+]?[0-9]+)?$`)
	durationText = regexp.MustCompile(`^[-+]?([0-9]+(\.[0-9]+)?(ns|us|µs|ms|s|m|h))+$`)
)

// coerceText converts env, cli, runtime and header text to the property's type
// (spec/README.md, Layers). The second result is an error code, or "".
func coerceText(p *property, text string) (any, string) {
	switch p.typ {
	case TypeInteger:
		if !integerText.MatchString(text) {
			return nil, CodeInvalidType
		}
		n, err := strconv.ParseInt(text, 10, 64)
		if err != nil {
			return nil, CodeInvalidType
		}
		return n, ""
	case TypeNumber:
		if !numberText.MatchString(text) {
			return nil, CodeInvalidType
		}
		f, err := strconv.ParseFloat(text, 64)
		if err != nil {
			return nil, CodeInvalidType
		}
		return f, ""
	case TypeBoolean:
		switch text {
		case "true":
			return true, ""
		case "false":
			return false, ""
		}
		return nil, CodeInvalidType
	case TypeArray:
		return textList(p, text)
	case TypeObject:
		if v, err := decodeJSON([]byte(text)); err == nil {
			return coerceTyped(p, v)
		}
		return nil, CodeInvalidType
	case TypeAny:
		if v, err := decodeJSON([]byte(text)); err == nil {
			return v, ""
		}
		return text, ""
	}
	return coerceTyped(p, text)
}

// textList converts array text: a JSON list, or comma-separated items, each trimmed, empty ones
// dropped, and each converted to the item type.
func textList(p *property, text string) (any, string) {
	if strings.HasPrefix(strings.TrimSpace(text), "[") {
		v, err := decodeJSON([]byte(text))
		if err != nil {
			return nil, CodeInvalidType
		}
		return coerceTyped(p, v)
	}
	item := &property{typ: p.items}
	out := []any{}
	for _, part := range strings.Split(text, ",") {
		s := strings.TrimSpace(part)
		if s == "" {
			continue
		}
		v, code := coerceText(item, s)
		if code != "" {
			return nil, code
		}
		out = append(out, v)
	}
	return out, ""
}

// coerceTyped checks a typed value (from a file, code or a schema) against the property's type and
// normalizes it: integers to int64, numbers to float64, lists to []any, mappings to map[string]any.
func coerceTyped(p *property, v any) (any, string) {
	v = normalizeValue(v)
	switch p.typ {
	case TypeInteger:
		if n, ok := asInt(v); ok {
			return n, ""
		}
	case TypeNumber:
		if f, ok := asFloat(v); ok {
			return f, ""
		}
	case TypeBoolean:
		if b, ok := v.(bool); ok {
			return b, ""
		}
	case TypeString:
		if s, ok := v.(string); ok {
			return s, ""
		}
	case TypeDuration:
		if s, ok := v.(string); ok && durationText.MatchString(s) {
			return s, ""
		}
	case TypeArray:
		if list, ok := v.([]any); ok && plain(list) {
			return coerceItems(p, list), ""
		}
	case TypeObject:
		if m, ok := v.(map[string]any); ok && plain(m) {
			return m, ""
		}
	default:
		if plain(v) {
			return v, ""
		}
	}
	return nil, CodeInvalidType
}

// plain says a normalized value is JSON data throughout: what the validator can judge.
func plain(v any) bool {
	switch t := v.(type) {
	case nil, bool, string, int64, uint64, float64:
		return true
	case []any:
		return !slices.ContainsFunc(t, func(e any) bool { return !plain(e) })
	case map[string]any:
		for _, e := range t {
			if !plain(e) {
				return false
			}
		}
		return true
	}
	return false
}

// coerceItems normalizes the items of a list whose item type is integer or number; the schema
// checks every item.
func coerceItems(p *property, list []any) []any {
	item := &property{typ: p.items}
	if item.typ != TypeInteger && item.typ != TypeNumber {
		return list
	}
	out := make([]any, len(list))
	for i, e := range list {
		if v, code := coerceTyped(item, e); code == "" {
			e = v
		}
		out[i] = e
	}
	return out
}

// normalizeValue converts a decoded or Go value to the forms the engine keeps: int64 (uint64 past
// its range), float64, bool, string, []any and map[string]any, a time.Duration as its text.
func normalizeValue(v any) any {
	switch t := v.(type) {
	case nil, bool, string, int64, float64:
		return v
	case json.Number:
		if n, err := t.Int64(); err == nil {
			return n
		}
		f, _ := t.Float64()
		return f
	case time.Duration:
		return t.String()
	}
	rv := reflect.ValueOf(v)
	switch rv.Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return rv.Int()
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		if n := rv.Uint(); n <= math.MaxInt64 {
			return int64(n)
		}
		return rv.Uint()
	case reflect.Float32, reflect.Float64:
		return rv.Float()
	case reflect.String:
		return rv.String()
	case reflect.Bool:
		return rv.Bool()
	case reflect.Slice, reflect.Array:
		out := make([]any, rv.Len())
		for i := range out {
			out[i] = normalizeValue(rv.Index(i).Interface())
		}
		return out
	case reflect.Map:
		if rv.Type().Key().Kind() != reflect.String {
			return v
		}
		out := make(map[string]any, rv.Len())
		for it := rv.MapRange(); it.Next(); {
			out[it.Key().String()] = normalizeValue(it.Value().Interface())
		}
		return out
	}
	return v
}

// decodeJSON decodes one JSON value, keeping integers exact.
func decodeJSON(data []byte) (any, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return nil, err
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return nil, errors.New("trailing data after the JSON value")
	}
	return normalizeValue(v), nil
}

// decodeDocument decodes a schema document: JSON when it starts with "{", else YAML.
func decodeDocument(data []byte) (map[string]any, error) {
	var v any
	var err error
	if bytes.HasPrefix(bytes.TrimSpace(data), []byte("{")) {
		v, err = decodeJSON(data)
	} else if err = yaml.Unmarshal(data, &v); err == nil {
		v = normalizeValue(v)
	}
	if err != nil {
		return nil, err
	}
	obj, ok := v.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("top level is %T", v)
	}
	return obj, nil
}

// asInt accepts a normalized integer (normalizeValue), and a float with no fractional part.
func asInt(v any) (int64, bool) {
	switch n := v.(type) {
	case int64:
		return n, true
	case float64:
		if n == math.Trunc(n) && math.Abs(n) < 1<<63 {
			return int64(n), true
		}
	}
	return 0, false
}

// asFloat accepts a normalized number (normalizeValue): an integer past int64 is a uint64.
func asFloat(v any) (float64, bool) {
	switch n := v.(type) {
	case int64:
		return float64(n), true
	case uint64:
		return float64(n), true
	case float64:
		return n, !math.IsNaN(n) && !math.IsInf(n, 0)
	}
	return 0, false
}

// clampNumber sets a number outside the property's bounds to the nearer bound; an integer
// property clamps to the nearest integer inside the bounds. It reports whether it clamped.
func clampNumber(p *property, v any) (any, bool) {
	f, ok := asFloat(v)
	if !ok {
		return v, false
	}
	lo, hi := math.Inf(-1), math.Inf(1)
	if p.min != nil {
		lo = *p.min
	}
	if p.max != nil {
		hi = *p.max
	}
	if p.typ == TypeInteger {
		lo, hi = math.Ceil(lo), math.Floor(hi)
	}
	switch {
	case f < lo:
		f = lo
	case f > hi:
		f = hi
	default:
		return v, false
	}
	if p.typ == TypeInteger {
		return int64(f), true
	}
	return f, true
}
