package linear

import (
	"reflect"
	"strings"
	"unicode"
)

// Anyone in the workspace can write issue text, comments and names, and the
// plugin prints them to a terminal: an embedded escape sequence would be run
// by it (retitle the window, write the clipboard via OSC 52, …). So every
// string from Linear loses its control characters on the way in.

// Clean drops control characters (C0, DEL, C1) except newline and tab.
func Clean(s string) string {
	if strings.IndexFunc(s, isBadControl) < 0 {
		return s
	}
	return strings.Map(func(r rune) rune {
		if isBadControl(r) {
			return -1
		}
		return r
	}, s)
}

func isBadControl(r rune) bool {
	return r != '\n' && r != '\t' && unicode.IsControl(r)
}

// Sanitize cleans every string reachable from v (a pointer) in place.
func Sanitize(v any) { clean(reflect.ValueOf(v)) }

func clean(v reflect.Value) {
	switch v.Kind() {
	case reflect.Pointer, reflect.Interface:
		if !v.IsNil() {
			clean(v.Elem())
		}
	case reflect.Struct:
		for i := range v.NumField() {
			if f := v.Field(i); f.CanSet() || f.Kind() != reflect.String {
				clean(f)
			}
		}
	case reflect.Slice, reflect.Array:
		for i := range v.Len() {
			clean(v.Index(i))
		}
	case reflect.Map:
		for _, k := range v.MapKeys() {
			e := v.MapIndex(k)
			if e.Kind() == reflect.Interface && !e.IsNil() { // map[string]any: metadata
				e = e.Elem()
			}
			if e.Kind() == reflect.String {
				v.SetMapIndex(k, reflect.ValueOf(Clean(e.String())).Convert(e.Type()))
			}
		}
	case reflect.String:
		if v.CanSet() {
			v.SetString(Clean(v.String()))
		}
	}
}
