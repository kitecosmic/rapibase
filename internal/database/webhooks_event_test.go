package database

import "testing"

func TestEventJSON(t *testing.T) {
	if got := eventJSON(`INSERT:a"]`); got != `["INSERT:a\"]"]` {
		t.Errorf("got %s", got)
	}
}
