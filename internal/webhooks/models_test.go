package webhooks

import "testing"

func TestMatchesData(t *testing.T) {
	row := map[string]interface{}{
		"total":  150.0,
		"status": "paid",
		"nota":   nil,
	}
	cases := []struct {
		name   string
		filter []FilterCond
		want   bool
	}{
		{"sin condiciones", nil, true},
		{"gt numérico cumple", []FilterCond{{Column: "total", Op: "gt", Value: "100"}}, true},
		{"gt numérico no cumple", []FilterCond{{Column: "total", Op: "gt", Value: "200"}}, false},
		{"eq string", []FilterCond{{Column: "status", Op: "eq", Value: "paid"}}, true},
		{"neq string", []FilterCond{{Column: "status", Op: "neq", Value: "pending"}}, true},
		{"contains", []FilterCond{{Column: "status", Op: "contains", Value: "AI"}}, true},
		{"is_null", []FilterCond{{Column: "nota", Op: "is_null"}}, true},
		{"not_null falla en null", []FilterCond{{Column: "nota", Op: "not_null"}}, false},
		{"columna ausente falla", []FilterCond{{Column: "no_existe", Op: "eq", Value: "x"}}, false},
		{"AND: una falla → todo falla", []FilterCond{
			{Column: "total", Op: "gt", Value: "100"},
			{Column: "status", Op: "eq", Value: "pending"},
		}, false},
		{"AND: ambas cumplen", []FilterCond{
			{Column: "total", Op: "gte", Value: "150"},
			{Column: "status", Op: "eq", Value: "paid"},
		}, true},
		{"numérico como string en el row", []FilterCond{{Column: "total", Op: "lte", Value: "150.5"}}, true},
	}
	for _, c := range cases {
		w := Webhook{Filter: c.filter}
		if got := w.MatchesData(row); got != c.want {
			t.Errorf("%s: got %v, want %v", c.name, got, c.want)
		}
	}
	// DELETE sin data (nil) con condiciones → no entrega
	if (Webhook{Filter: []FilterCond{{Column: "x", Op: "eq", Value: "1"}}}).MatchesData(nil) {
		t.Error("con condiciones y data nil debería ser false")
	}
}
