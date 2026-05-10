package mcp

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestMCPHandlerListsTools verifies the streamable-http endpoint accepts
// JSON-RPC and exposes every tool we registered. No DB is touched —
// `tools/list` is a pure metadata call against the in-memory registry, so
// passing nil for db/storage/dispatcher is safe.
func TestMCPHandlerListsTools(t *testing.T) {
	h := NewHandler(nil, nil, nil)

	// Single batched payload: initialize → notifications/initialized → tools/list.
	// The streamable-http transport in stateless mode happily processes each
	// message standalone, so for the smoke test we send tools/list directly.
	body := `{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{}}`
	req := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	rr := httptest.NewRecorder()

	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rr.Code, rr.Body.String())
	}

	raw, _ := io.ReadAll(rr.Body)
	var resp struct {
		Result struct {
			Tools []struct {
				Name string `json:"name"`
			} `json:"tools"`
		} `json:"result"`
		Error any `json:"error"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		t.Fatalf("invalid JSON-RPC response: %v\nbody=%s", err, raw)
	}
	if resp.Error != nil {
		t.Fatalf("JSON-RPC error: %v", resp.Error)
	}

	got := map[string]bool{}
	for _, tool := range resp.Result.Tools {
		got[tool.Name] = true
	}

	// Every tool we register should appear. Storage tools require a non-nil
	// storage client, so they're absent in this test — that's expected.
	want := []string{
		"list_tables", "describe_table",
		"query_rows", "insert_row", "update_row", "delete_row",
		"execute_sql",
		"create_table", "drop_table",
	}
	for _, name := range want {
		if !got[name] {
			t.Errorf("missing tool %q in tools/list response (got %d tools)", name, len(got))
		}
	}

	// Sanity: storage tools must NOT appear when storage is nil.
	for _, name := range []string{"list_buckets", "upload_object"} {
		if got[name] {
			t.Errorf("tool %q registered without a storage client", name)
		}
	}
}

// TestMCPHandlerToolAnnotations checks every registered tool has the right
// readOnly/destructive/idempotent hints, so well-behaved clients stop asking
// for confirmation on pure reads.
func TestMCPHandlerToolAnnotations(t *testing.T) {
	h := NewHandler(nil, nil, nil)

	body := `{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{}}`
	req := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	raw, _ := io.ReadAll(rr.Body)
	var resp struct {
		Result struct {
			Tools []struct {
				Name        string `json:"name"`
				Annotations struct {
					Title           string `json:"title"`
					ReadOnlyHint    *bool  `json:"readOnlyHint"`
					DestructiveHint *bool  `json:"destructiveHint"`
					IdempotentHint  *bool  `json:"idempotentHint"`
				} `json:"annotations"`
			} `json:"tools"`
		} `json:"result"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		t.Fatalf("invalid JSON-RPC response: %v\nbody=%s", err, raw)
	}

	type expect struct {
		readOnly, destructive, idempotent bool
		title                             string
	}
	want := map[string]expect{
		"list_tables":     {readOnly: true, destructive: false, idempotent: true, title: "List tables"},
		"describe_table":  {readOnly: true, destructive: false, idempotent: true, title: "Describe table"},
		"query_rows":      {readOnly: true, destructive: false, idempotent: true, title: "Query rows"},
		"insert_row":      {readOnly: false, destructive: false, idempotent: false, title: "Insert row"},
		"update_row":      {readOnly: false, destructive: false, idempotent: true, title: "Update row"},
		"delete_row":      {readOnly: false, destructive: true, idempotent: true, title: "Delete row"},
		"execute_sql":     {readOnly: false, destructive: true, idempotent: false, title: "Execute SQL"},
		"create_table":    {readOnly: false, destructive: false, idempotent: false, title: "Create table"},
		"drop_table":      {readOnly: false, destructive: true, idempotent: true, title: "Drop table"},
	}

	got := map[string]struct {
		readOnly, destructive, idempotent bool
		title                             string
	}{}
	for _, tool := range resp.Result.Tools {
		got[tool.Name] = struct {
			readOnly, destructive, idempotent bool
			title                             string
		}{
			readOnly:    tool.Annotations.ReadOnlyHint != nil && *tool.Annotations.ReadOnlyHint,
			destructive: tool.Annotations.DestructiveHint != nil && *tool.Annotations.DestructiveHint,
			idempotent:  tool.Annotations.IdempotentHint != nil && *tool.Annotations.IdempotentHint,
			title:       tool.Annotations.Title,
		}
	}
	for name, w := range want {
		g, ok := got[name]
		if !ok {
			t.Errorf("tool %q missing from tools/list", name)
			continue
		}
		if g.readOnly != w.readOnly || g.destructive != w.destructive || g.idempotent != w.idempotent {
			t.Errorf("tool %q hints: got readOnly=%v destructive=%v idempotent=%v, want readOnly=%v destructive=%v idempotent=%v",
				name, g.readOnly, g.destructive, g.idempotent, w.readOnly, w.destructive, w.idempotent)
		}
		if g.title != w.title {
			t.Errorf("tool %q title: got %q, want %q", name, g.title, w.title)
		}
	}
}

// TestMCPHandlerListsResources confirms the rapibase://tables resource and
// the rapibase://tables/{name} template are advertised.
func TestMCPHandlerListsResources(t *testing.T) {
	h := NewHandler(nil, nil, nil)

	post := func(method string) []byte {
		body := `{"jsonrpc":"2.0","id":1,"method":"` + method + `","params":{}}`
		req := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json, text/event-stream")
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, req)
		if rr.Code != http.StatusOK {
			t.Fatalf("status = %d for %s; body=%s", rr.Code, method, rr.Body.String())
		}
		b, _ := io.ReadAll(rr.Body)
		return b
	}

	var resources struct {
		Result struct {
			Resources []struct {
				URI string `json:"uri"`
			} `json:"resources"`
		} `json:"result"`
	}
	if err := json.Unmarshal(post("resources/list"), &resources); err != nil {
		t.Fatalf("resources/list parse: %v", err)
	}
	if len(resources.Result.Resources) == 0 || resources.Result.Resources[0].URI != "rapibase://tables" {
		t.Errorf("rapibase://tables not exposed; got %+v", resources.Result.Resources)
	}

	var templates struct {
		Result struct {
			ResourceTemplates []struct {
				URITemplate string `json:"uriTemplate"`
			} `json:"resourceTemplates"`
		} `json:"result"`
	}
	if err := json.Unmarshal(post("resources/templates/list"), &templates); err != nil {
		t.Fatalf("resources/templates/list parse: %v", err)
	}
	found := false
	for _, tpl := range templates.Result.ResourceTemplates {
		if tpl.URITemplate == "rapibase://tables/{name}" {
			found = true
		}
	}
	if !found {
		t.Errorf("rapibase://tables/{name} template not exposed; got %+v", templates.Result.ResourceTemplates)
	}
}
