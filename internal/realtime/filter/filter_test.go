package filter

import (
	"encoding/json"
	"errors"
	"testing"
	"time"
)

// mapRow is a Row implementation backed by a plain map. Used everywhere
// in tests so we don't have to import the wal package.
type mapRow map[string]any

func (r mapRow) Get(c string) (any, bool) {
	v, ok := r[c]
	return v, ok
}

// jsonTree parses a JSON filter string into the generic any tree the
// compiler expects, mirroring what the JSON codec would produce.
func jsonTree(t *testing.T, raw string) any {
	t.Helper()
	var v any
	dec := json.NewDecoder(stringsReader(raw))
	dec.UseNumber()
	if err := dec.Decode(&v); err != nil {
		t.Fatalf("parse filter %s: %v", raw, err)
	}
	return v
}

func stringsReader(s string) *jsonReader { return &jsonReader{s: s} }

type jsonReader struct {
	s string
	i int
}

func (r *jsonReader) Read(p []byte) (int, error) {
	if r.i >= len(r.s) {
		return 0, errEOF
	}
	n := copy(p, r.s[r.i:])
	r.i += n
	return n, nil
}

var errEOF = errors.New("EOF")

func TestCompile_Nil(t *testing.T) {
	p, err := Compile(nil)
	if err != nil {
		t.Fatalf("nil tree should compile: %v", err)
	}
	if !p(mapRow{}) {
		t.Fatalf("Always should match empty row")
	}
}

func TestCompile_Eq(t *testing.T) {
	tree := jsonTree(t, `{"column":"room_id","op":"eq","value":42}`)
	p, err := Compile(tree)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	if !p(mapRow{"room_id": int64(42)}) {
		t.Fatalf("int64 should match")
	}
	if !p(mapRow{"room_id": 42.0}) {
		t.Fatalf("float64 should match")
	}
	if !p(mapRow{"room_id": json.Number("42")}) {
		t.Fatalf("json.Number should match")
	}
	if p(mapRow{"room_id": 43}) {
		t.Fatalf("43 should not match")
	}
	if p(mapRow{"room_id": nil}) {
		t.Fatalf("nil should not equal 42")
	}
}

func TestCompile_NeqAndStringEq(t *testing.T) {
	p, err := Compile(jsonTree(t, `{"column":"status","op":"neq","value":"done"}`))
	if err != nil {
		t.Fatal(err)
	}
	if !p(mapRow{"status": "pending"}) {
		t.Fatalf("pending should not equal done")
	}
	if p(mapRow{"status": "done"}) {
		t.Fatalf("done should equal done")
	}
}

func TestCompile_AndOrNot(t *testing.T) {
	tree := jsonTree(t, `{"op":"and","conditions":[
		{"column":"room_id","op":"eq","value":42},
		{"op":"or","conditions":[
			{"column":"text","op":"like","value":"hola%"},
			{"op":"not","conditions":[
				{"column":"deleted","op":"is","value":true}
			]}
		]}
	]}`)
	p, err := Compile(tree)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	if !p(mapRow{"room_id": 42, "text": "hola mundo", "deleted": true}) {
		t.Fatalf("matches: room=42, text~='hola%%'")
	}
	if !p(mapRow{"room_id": 42, "text": "adiós", "deleted": false}) {
		t.Fatalf("matches: room=42, not deleted")
	}
	if p(mapRow{"room_id": 42, "text": "adiós", "deleted": true}) {
		t.Fatalf("should not match: deleted=true and text mismatch")
	}
	if p(mapRow{"room_id": 99, "text": "hola"}) {
		t.Fatalf("wrong room should not match")
	}
}

func TestCompile_LtLteGtGte(t *testing.T) {
	for op, want := range map[string]map[float64]bool{
		"lt":  {9: true, 10: false, 11: false},
		"lte": {9: true, 10: true, 11: false},
		"gt":  {9: false, 10: false, 11: true},
		"gte": {9: false, 10: true, 11: true},
	} {
		tree := jsonTree(t, `{"column":"age","op":"`+op+`","value":10}`)
		p, err := Compile(tree)
		if err != nil {
			t.Fatalf("compile %s: %v", op, err)
		}
		for v, expected := range want {
			if got := p(mapRow{"age": v}); got != expected {
				t.Fatalf("%s 10 / row=%v: got %v want %v", op, v, got, expected)
			}
		}
	}
}

func TestCompile_TimeComparison(t *testing.T) {
	cutoff := "2026-01-01T00:00:00Z"
	tree := jsonTree(t, `{"column":"created_at","op":"gte","value":"`+cutoff+`"}`)
	p, err := Compile(tree)
	if err != nil {
		t.Fatal(err)
	}
	if !p(mapRow{"created_at": time.Date(2026, 5, 11, 0, 0, 0, 0, time.UTC)}) {
		t.Fatalf("after cutoff should match")
	}
	if p(mapRow{"created_at": time.Date(2025, 12, 31, 23, 0, 0, 0, time.UTC)}) {
		t.Fatalf("before cutoff should not match")
	}
}

func TestCompile_In_NotIn(t *testing.T) {
	tree := jsonTree(t, `{"column":"user_id","op":"in","value":[7,9,12]}`)
	p, err := Compile(tree)
	if err != nil {
		t.Fatal(err)
	}
	if !p(mapRow{"user_id": 9}) {
		t.Fatalf("9 should be in")
	}
	if p(mapRow{"user_id": 8}) {
		t.Fatalf("8 should not be in")
	}

	tree = jsonTree(t, `{"column":"user_id","op":"nin","value":[7,9,12]}`)
	p, err = Compile(tree)
	if err != nil {
		t.Fatal(err)
	}
	if !p(mapRow{"user_id": 8}) {
		t.Fatalf("8 should be not-in")
	}
	if p(mapRow{"user_id": 7}) {
		t.Fatalf("7 should be in")
	}
}

func TestCompile_Is(t *testing.T) {
	cases := []struct {
		filter string
		row    any
		want   bool
	}{
		{`{"column":"deleted_at","op":"is","value":null}`, nil, true},
		{`{"column":"deleted_at","op":"is","value":null}`, "2026-01-01", false},
		{`{"column":"archived","op":"is","value":true}`, true, true},
		{`{"column":"archived","op":"is","value":true}`, false, false},
		{`{"column":"archived","op":"is","value":"false"}`, false, true},
		{`{"column":"archived","op":"is","value":"null"}`, nil, true},
	}
	for _, c := range cases {
		p, err := Compile(jsonTree(t, c.filter))
		if err != nil {
			t.Fatalf("compile %s: %v", c.filter, err)
		}
		var row mapRow
		if c.row == nil {
			row = mapRow{"deleted_at": nil, "archived": nil}
		} else {
			row = mapRow{"deleted_at": c.row, "archived": c.row}
		}
		if got := p(row); got != c.want {
			t.Fatalf("%s row=%v: got %v want %v", c.filter, c.row, got, c.want)
		}
	}
}

func TestCompile_LikeIlike(t *testing.T) {
	p, err := Compile(jsonTree(t, `{"column":"text","op":"like","value":"Hola%"}`))
	if err != nil {
		t.Fatal(err)
	}
	if !p(mapRow{"text": "Hola mundo"}) {
		t.Fatal("Hola-prefix wildcard should match Hola mundo")
	}
	if p(mapRow{"text": "hola mundo"}) {
		t.Fatalf("LIKE is case-sensitive")
	}

	p, err = Compile(jsonTree(t, `{"column":"text","op":"ilike","value":"%mundo"}`))
	if err != nil {
		t.Fatal(err)
	}
	if !p(mapRow{"text": "Hola MUNDO"}) {
		t.Fatalf("ILIKE should be case-insensitive")
	}

	// Underscore wildcard matches single char.
	p, err = Compile(jsonTree(t, `{"column":"code","op":"like","value":"A_C"}`))
	if err != nil {
		t.Fatal(err)
	}
	if !p(mapRow{"code": "ABC"}) {
		t.Fatalf("ABC should match A_C")
	}
	if p(mapRow{"code": "ABBC"}) {
		t.Fatalf("ABBC should not match A_C")
	}

	// Literal % via escape.
	p, err = Compile(jsonTree(t, `{"column":"text","op":"like","value":"100\\%"}`))
	if err != nil {
		t.Fatal(err)
	}
	if !p(mapRow{"text": "100%"}) {
		t.Fatal("escaped percent should match literal")
	}
	if p(mapRow{"text": "10000"}) {
		t.Fatal("escaped percent should not be wildcard")
	}
}

func TestCompile_Contains_JSONB(t *testing.T) {
	p, err := Compile(jsonTree(t, `{
		"column":"metadata","op":"contains",
		"value":{"tag":"v1","owner":{"id":7}}
	}`))
	if err != nil {
		t.Fatal(err)
	}
	row := mapRow{"metadata": map[string]any{
		"tag":   "v1",
		"owner": map[string]any{"id": 7, "name": "Joel"},
		"extra": "ignored",
	}}
	if !p(row) {
		t.Fatalf("nested subset should match")
	}

	// Missing inner field → no match.
	if p(mapRow{"metadata": map[string]any{"tag": "v2"}}) {
		t.Fatalf("wrong tag should not match")
	}
}

func TestCompile_Contains_Array(t *testing.T) {
	p, err := Compile(jsonTree(t, `{"column":"tags","op":"contains","value":["go","db"]}`))
	if err != nil {
		t.Fatal(err)
	}
	if !p(mapRow{"tags": []any{"go", "db", "linux"}}) {
		t.Fatalf("array should contain subset")
	}
	if p(mapRow{"tags": []any{"go"}}) {
		t.Fatalf("missing 'db' should not match")
	}
}

func TestCompile_ContainedBy(t *testing.T) {
	p, err := Compile(jsonTree(t, `{"column":"tags","op":"contained_by","value":["go","db","linux"]}`))
	if err != nil {
		t.Fatal(err)
	}
	if !p(mapRow{"tags": []any{"go", "db"}}) {
		t.Fatalf("subset should be contained_by superset")
	}
	if p(mapRow{"tags": []any{"go", "rust"}}) {
		t.Fatalf("rust not in superset")
	}
}

func TestCompile_Match(t *testing.T) {
	p, err := Compile(jsonTree(t, `{"column":"body","op":"match","value":"PostgreSQL"}`))
	if err != nil {
		t.Fatal(err)
	}
	if !p(mapRow{"body": "we use postgresql in production"}) {
		t.Fatalf("substring case-insensitive match expected")
	}
	if p(mapRow{"body": "we use sqlite"}) {
		t.Fatalf("should not match unrelated text")
	}
}

func TestCompile_Errors(t *testing.T) {
	cases := []struct {
		name string
		raw  string
	}{
		{"unknown op", `{"column":"x","op":"weird","value":1}`},
		{"missing op", `{"column":"x","value":1}`},
		{"and without conditions", `{"op":"and"}`},
		{"not multi", `{"op":"not","conditions":[{"column":"x","op":"eq","value":1},{"column":"y","op":"eq","value":2}]}`},
		{"is bogus", `{"column":"x","op":"is","value":"maybe"}`},
		{"in not array", `{"column":"x","op":"in","value":7}`},
		{"like not string", `{"column":"x","op":"like","value":7}`},
	}
	for _, c := range cases {
		_, err := Compile(jsonTree(t, c.raw))
		if err == nil {
			t.Fatalf("%s: expected error, got nil", c.name)
		}
	}
}

func TestReferencedColumns(t *testing.T) {
	tree := jsonTree(t, `{"op":"and","conditions":[
		{"column":"room_id","op":"eq","value":42},
		{"op":"or","conditions":[
			{"column":"text","op":"like","value":"hola%"},
			{"column":"author_id","op":"eq","value":7}
		]}
	]}`)
	got := ReferencedColumns(tree)
	want := []string{"author_id", "room_id", "text"}
	if len(got) != len(want) {
		t.Fatalf("got %v want %v", got, want)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Fatalf("got %v want %v", got, want)
		}
	}
}

// schemaMap implements Schema for tests.
type schemaMap map[string]string

func (s schemaMap) ColumnType(c string) (string, bool) {
	v, ok := s[c]
	return v, ok
}

func TestCompileWithSchema_UnknownColumn(t *testing.T) {
	tree := jsonTree(t, `{"column":"missing","op":"eq","value":1}`)
	_, err := CompileWithSchema(tree, schemaMap{"present": "int"})
	if err == nil {
		t.Fatalf("expected error for missing column")
	}
}

func TestCompileWithSchema_AllPresent(t *testing.T) {
	tree := jsonTree(t, `{"column":"id","op":"eq","value":1}`)
	p, err := CompileWithSchema(tree, schemaMap{"id": "int"})
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	if !p(mapRow{"id": 1}) {
		t.Fatalf("predicate should match")
	}
}

func TestPermissions_ValidateColumns(t *testing.T) {
	checker := stubChecker{denied: map[string]bool{"secret": true}}
	if err := ValidateColumns(checker, "user", "public", "t", []string{"a", "b"}); err != nil {
		t.Fatalf("allowed cols should pass: %v", err)
	}
	if err := ValidateColumns(checker, "user", "public", "t", []string{"a", "secret"}); err == nil {
		t.Fatalf("denied col should fail")
	}
}

type stubChecker struct{ denied map[string]bool }

func (c stubChecker) CanRead(_, _, _, col string) bool { return !c.denied[col] }
func (c stubChecker) ReadableColumns(_, _, _ string, cols []string) []string {
	out := make([]string, 0, len(cols))
	for _, col := range cols {
		if !c.denied[col] {
			out = append(out, col)
		}
	}
	return out
}
