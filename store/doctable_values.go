package store

import (
	"bytes"
	"database/sql/driver"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"math"
	"math/big"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"modernc.org/sqlite"
	"ofam.dev/ochazuke"
)

// The SQL functions a DocTable's queries call, so SQLite orders, groups
// and searches documents by the same rules as ochazuke's in-memory list
// (Runtime.QueryAll, MemDocTable): each takes a field's JSON text (row ->
// path; NULL when the row has no such field) and reads it as Go does.
//
//   - ofam_doc_blank(j): 1 when the value sorts as blank (missing, null,
//     only spaces, an empty list), else 0.
//   - ofam_doc_isdate(j): 1 when the value is an ISO date or RFC 3339 time.
//   - ofam_doc_sort(j, kind, enum, date, desc): the value's sort key, a blob
//     whose byte order is the list's order for that key: blanks last
//     whichever way it runs, then the rank (an enum's position; a value
//     that does not read as the kind after those that do), then the number,
//     the time or the lower-cased text.
//   - ofam_doc_label(j): the group label of the value ("(none)" for blank).
//   - ofam_doc_cell(j): the lower-cased text a text search reads; NULL for
//     a missing or null value.
func init() {
	for name, f := range map[string]struct {
		args int32
		fn   func([]driver.Value) (driver.Value, error)
	}{
		"ofam_doc_blank":  {1, sqlBlank},
		"ofam_doc_isdate": {1, sqlIsDate},
		"ofam_doc_sort":   {5, sqlSortKey},
		"ofam_doc_label":  {1, sqlLabel},
		"ofam_doc_cell":   {1, sqlCell},
	} {
		fn := f.fn
		sqlite.MustRegisterFunction(name, &sqlite.FunctionImpl{NArgs: f.args, Deterministic: true, Scalar: func(_ *sqlite.FunctionContext, args []driver.Value) (driver.Value, error) {
			return fn(args)
		}})
	}
}

// Sort kinds: how a key's values compare (ochazuke's normalizedSort by
// the prop's type).
const (
	sortText     = iota // the lower-cased text
	sortDateText        // a text prop: as times when every value is one
	sortEnum            // the position in the prop's enum
	sortNumber          // integer and number
	sortDuration        // a duration's length
	sortBool            // false before true
)

// Filter kinds: how an equality reads a stored value (the prop's type).
const (
	filterNone  = iota // not pushed: answered in Go
	filterText         // string, path, url
	filterInt          // integer
	filterFloat        // number
	filterBool         // boolean
)

type propKinds struct {
	sort, filter int
	list, float  bool
}

// kindsOfType is what each prop type sorts and filters as; a type it does
// not name sorts as text and is filtered in Go.
var kindsOfType = sync.OnceValue(typeKinds)

func typeKinds() map[string]propKinds {
	return map[string]propKinds{
		"":                       {sort: sortDateText, filter: filterText},
		ochazuke.SettingString:   {sort: sortDateText, filter: filterText},
		ochazuke.PropPath:        {sort: sortDateText, filter: filterText},
		ochazuke.PropURL:         {sort: sortText, filter: filterText},
		ochazuke.SettingInteger:  {sort: sortNumber, filter: filterInt},
		ochazuke.SettingNumber:   {sort: sortNumber, filter: filterFloat, float: true},
		ochazuke.SettingDuration: {sort: sortDuration},
		ochazuke.SettingBoolean:  {sort: sortBool, filter: filterBool},
		ochazuke.SettingList:     {sort: sortText, list: true},
	}
}

func kindsOf(typ string) propKinds {
	k, ok := kindsOfType()[typ]
	if !ok {
		return propKinds{sort: sortText}
	}
	return k
}

// fieldValue decodes a function's JSON argument; ok is false for SQL NULL
// (no such field).
func fieldValue(arg driver.Value) (v any, ok bool, err error) {
	var raw []byte
	switch a := arg.(type) {
	case nil:
		return nil, false, nil
	case string:
		raw = []byte(a)
	case []byte:
		raw = a
	default:
		return a, true, nil
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	if err := dec.Decode(&v); err != nil {
		return nil, false, err
	}
	return docValue(v, ""), true, nil
}

func sqlBool(b bool) int64 {
	if b {
		return 1
	}
	return 0
}

func sqlBlank(args []driver.Value) (driver.Value, error) {
	v, _, err := fieldValue(args[0])
	return sqlBool(sortBlank(v)), err
}

func sqlIsDate(args []driver.Value) (driver.Value, error) {
	v, _, err := fieldValue(args[0])
	_, ok := sortDate(v)
	return sqlBool(ok), err
}

func sqlLabel(args []driver.Value) (driver.Value, error) {
	v, _, err := fieldValue(args[0])
	if sortBlank(v) {
		return ochazuke.NoGroup, err
	}
	return fmt.Sprint(looseText(v)), err
}

func sqlCell(args []driver.Value) (driver.Value, error) {
	v, ok, err := fieldValue(args[0])
	if !ok || v == nil || err != nil {
		return nil, err
	}
	return strings.ToLower(cellText(v)), nil
}

func intArg(v driver.Value) int {
	i, _ := v.(int64)
	return int(i)
}

func sqlSortKey(args []driver.Value) (driver.Value, error) {
	v, _, err := fieldValue(args[0])
	if err != nil {
		return nil, err
	}
	var enum []string
	if s, ok := args[2].(string); ok && s != "" {
		if err := json.Unmarshal([]byte(s), &enum); err != nil {
			return nil, err
		}
	}
	return sortKey(v, intArg(args[1]), enum, intArg(args[3]) == 1, intArg(args[4]) == 1), nil
}

// sortKey is the byte form of ochazuke's sort comparison of one value: a
// blank is [1] (last both ways); otherwise 0, the rank, a tag (0 number,
// 1 time, 2 text) and the value, every byte after the first inverted for
// a descending key.
func sortKey(v any, kind int, enum []string, date, desc bool) []byte {
	if sortBlank(v) {
		return []byte{1}
	}
	rank, tag, payload := sortParts(v, kind, enum, date)
	out := make([]byte, 0, 6+len(payload))
	out = append(out, 0)
	out = binary.BigEndian.AppendUint32(out, uint32(int32(rank))^1<<31)
	out = append(out, tag)
	out = append(out, payload...)
	if desc {
		for i := 1; i < len(out); i++ {
			out[i] = ^out[i]
		}
	}
	return out
}

// sortParts is a non-blank value's rank and compared value.
func sortParts(v any, kind int, enum []string, date bool) (rank int, tag byte, payload []byte) {
	text := func() []byte { return textKey(strings.ToLower(fmt.Sprint(looseText(v)))) }
	switch kind {
	case sortEnum:
		rank = slices.Index(enum, fmt.Sprint(v))
		if rank < 0 {
			rank = len(enum)
		}
		return rank, 2, text()
	case sortNumber:
		if r, ok := new(big.Rat).SetString(strings.TrimSpace(fmt.Sprint(v))); ok {
			f, _ := r.Float64()
			return 0, 0, floatKey(f)
		}
	case sortDuration:
		if d, err := time.ParseDuration(fmt.Sprint(v)); err == nil {
			return 0, 0, floatKey(float64(d))
		}
	case sortBool:
		if b, err := strconv.ParseBool(fmt.Sprint(v)); err == nil {
			return 0, 0, floatKey(float64(sqlBool(b)))
		}
	case sortDateText:
		if !date {
			return 0, 2, text()
		}
		if t, ok := sortDate(v); ok && !t.IsZero() {
			return 0, 1, binary.BigEndian.AppendUint64(nil, uint64(t.UnixNano())^1<<63)
		} else if ok {
			return 0, 2, text()
		}
	default:
		return 0, 2, text()
	}
	return 1, 2, text()
}

// floatKey orders float64s by their bytes.
func floatKey(f float64) []byte {
	bits := math.Float64bits(f)
	if bits&(1<<63) != 0 {
		bits = ^bits
	} else {
		bits |= 1 << 63
	}
	return binary.BigEndian.AppendUint64(nil, bits)
}

// textKey orders strings by their bytes and keeps a prefix first when its
// bytes are inverted: a 0 byte is written 0 0xff and the text ends 0 0.
func textKey(s string) []byte {
	out := make([]byte, 0, len(s)+2)
	for i := 0; i < len(s); i++ {
		out = append(out, s[i])
		if s[i] == 0 {
			out = append(out, 0xff)
		}
	}
	return append(out, 0, 0)
}

// sortBlank is ochazuke's: nothing, only spaces, an empty list.
func sortBlank(v any) bool {
	switch x := v.(type) {
	case nil:
		return true
	case string:
		return strings.TrimSpace(x) == ""
	case []string:
		return len(x) == 0
	case []any:
		return len(x) == 0
	}
	return false
}

// sortDate is ochazuke's: an RFC 3339 time or an ISO date.
func sortDate(v any) (time.Time, bool) {
	s, ok := v.(string)
	if !ok {
		return time.Time{}, false
	}
	for _, layout := range []string{time.RFC3339Nano, time.DateOnly} {
		if t, err := time.Parse(layout, s); err == nil {
			return t, true
		}
	}
	return time.Time{}, false
}

// looseText is ochazuke's: text as it is, anything else as compact JSON.
func looseText(v any) any {
	switch v.(type) {
	case nil, string:
		return v
	}
	raw, err := json.Marshal(v)
	if err != nil {
		return fmt.Sprint(v)
	}
	return string(raw)
}

// cellText is ochazuke's cellValueText: text as it is, a list joined by
// commas, anything else as loose text.
func cellText(v any) string {
	switch x := v.(type) {
	case nil:
		return ""
	case string:
		return x
	case []string:
		return strings.Join(x, ", ")
	case []any:
		words := make([]string, len(x))
		for i, w := range x {
			words[i] = cellText(w)
		}
		return strings.Join(words, ", ")
	}
	return fmt.Sprint(looseText(v))
}
