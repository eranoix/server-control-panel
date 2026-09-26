// Sanitizing the lists we serve to the front end.
//
// Motivation: Alpine.js throws "Cannot read properties of undefined
// (reading 'after')" whenever <template x-for :key="X"> resolves X as
// undefined OR when two items share the same key. Defending only in the
// front end (try/catch, _skl, etc.) treats the symptom — when the backend lets a
// struct with an empty Id/Name/Path escape (unstable Docker driver, truncated
// journalctl, legacy JSONL) the crash happens again on the next deploy.
//
// Policy: EVERY handler that returns []T to the front end goes through the
// function below. Items missing the expected key field are dropped; duplicates are
// kept only on first occurrence. Reflection is acceptable here — lists
// are in the order of tens of elements and the call happens once per request.
package api

import (
	"reflect"
	"strconv"
)

// sanitizeList filters the slice, removing entries with an empty key field and
// deduplicating by key. It accepts slices of structs or of map[string]any
// (including the interface{} case returned by the Docker SDK).
//
//	keys: acceptable field names (case-sensitive). The first non-empty
//	      key found on the item defines its identity.
//
// When v is not a slice/array, it returns the original value — the handler calls the
// helper without worrying about the exact shape (some endpoints return
// objects with a nested slice and the sanitizing descends one level only where it applies).
func sanitizeList(v any, keys ...string) any {
	if v == nil {
		return v
	}
	rv := reflect.ValueOf(v)
	for rv.Kind() == reflect.Interface || rv.Kind() == reflect.Pointer {
		if rv.IsNil() {
			return v
		}
		rv = rv.Elem()
	}
	if rv.Kind() != reflect.Slice && rv.Kind() != reflect.Array {
		return v
	}
	n := rv.Len()
	if n == 0 {
		return v
	}
	out := reflect.MakeSlice(rv.Type(), 0, n)
	seen := make(map[string]struct{}, n)
	for i := 0; i < n; i++ {
		item := rv.Index(i)
		k := extractKey(item, keys)
		if k == "" {
			continue
		}
		if _, dup := seen[k]; dup {
			continue
		}
		seen[k] = struct{}{}
		out = reflect.Append(out, item)
	}
	return out.Interface()
}

// extractKey tries each field name in the given order and returns the
// first non-empty string. It handles both structs (FieldByName) and
// map[string]any (MapIndex). Other types return the empty string, which makes the
// item be dropped — the correct behaviour: we do not want a slice of ints to
// fall through silently, but the caller only passes identifiable slices.
func extractKey(item reflect.Value, keys []string) string {
	for item.Kind() == reflect.Interface || item.Kind() == reflect.Pointer {
		if item.IsNil() {
			return ""
		}
		item = item.Elem()
	}
	switch item.Kind() {
	case reflect.Struct:
		for _, k := range keys {
			f := item.FieldByName(k)
			if !f.IsValid() {
				continue
			}
			if s, ok := stringFrom(f); ok && s != "" {
				return s
			}
		}
	case reflect.Map:
		for _, k := range keys {
			mv := item.MapIndex(reflect.ValueOf(k))
			if !mv.IsValid() {
				continue
			}
			if s, ok := stringFrom(mv); ok && s != "" {
				return s
			}
		}
	case reflect.String, reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		// Slice of string/int (e.g. auditActions = []string, secretKeys = []string).
		// The value itself is the Alpine key — no nested field.
		if s, ok := stringFrom(item); ok {
			return s
		}
	}
	return ""
}

// stringFrom converts a reflect.Value into a string when possible. It accepts a
// native string, an interface{} wrapping a string (coming from map[string]any) and
// integers (fields such as PID — an int never comes out "undefined" in JSON, but the
// front end uses the value as an Alpine key; uniform coercion avoids losing
// dupes). Exotic types return (_, false).
func stringFrom(v reflect.Value) (string, bool) {
	for v.Kind() == reflect.Interface || v.Kind() == reflect.Pointer {
		if v.IsNil() {
			return "", false
		}
		v = v.Elem()
	}
	switch v.Kind() {
	case reflect.String:
		return v.String(), true
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		n := v.Int()
		if n == 0 {
			// PID 0 is the "swapper/kernel" on Linux — effectively never shows up
			// in /proc for userspace; treating it as absent is safe.
			return "", false
		}
		return strconv.FormatInt(n, 10), true
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		n := v.Uint()
		if n == 0 {
			return "", false
		}
		return strconv.FormatUint(n, 10), true
	}
	return "", false
}
