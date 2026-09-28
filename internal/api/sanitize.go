package api

import (
	"reflect"
	"strconv"
)

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
		if s, ok := stringFrom(item); ok {
			return s
		}
	}
	return ""
}

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
