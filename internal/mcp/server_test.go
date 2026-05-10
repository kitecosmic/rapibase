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
