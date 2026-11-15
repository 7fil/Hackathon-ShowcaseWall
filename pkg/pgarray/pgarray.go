// Package pgarray maps Go string slices to PostgreSQL TEXT[] columns.
package pgarray

import (
	"database/sql/driver"
	"fmt"
	"strings"
)

// StringArray implements driver.Valuer and sql.Scanner for TEXT[].
type StringArray []string

// Value renders the slice as a PostgreSQL array literal.
func (a StringArray) Value() (driver.Value, error) {
	if len(a) == 0 {
		return "{}", nil
	}
	elems := make([]string, len(a))
	for i, s := range a {
		s = strings.ReplaceAll(s, `\`, `\\`)
		s = strings.ReplaceAll(s, `"`, `\"`)
		elems[i] = `"` + s + `"`
	}
	return "{" + strings.Join(elems, ",") + "}", nil
}

// Scan reads a PostgreSQL array literal (text format).
func (a *StringArray) Scan(src any) error {
	switch v := src.(type) {
	case nil:
		*a = StringArray{}
	case string:
		*a = Parse(v)
	case []byte:
		*a = Parse(string(v))
	case []string:
		*a = StringArray(v)
	default:
		return fmt.Errorf("pgarray: unsupported scan type %T", src)
	}
	return nil
}

// Parse decodes a one-dimensional text array literal such as {a,"b c"}.
func Parse(s string) []string {
	s = strings.TrimSpace(s)
	if len(s) < 2 || s[0] != '{' || s[len(s)-1] != '}' {
		return []string{}
	}
	inner := s[1 : len(s)-1]
	if inner == "" {
		return []string{}
	}
	out := []string{}
	var cur strings.Builder
	inQuotes, escaped := false, false
	for i := 0; i < len(inner); i++ {
		ch := inner[i]
		switch {
		case escaped:
			cur.WriteByte(ch)
			escaped = false
		case ch == '\\':
			escaped = true
		case ch == '"':
			inQuotes = !inQuotes
		case ch == ',' && !inQuotes:
			out = append(out, cur.String())
			cur.Reset()
		default:
			cur.WriteByte(ch)
		}
	}
	return append(out, cur.String())
}
