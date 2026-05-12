// Realtime docs endpoint.
//
// Reads the markdown files in docs/realtime/ at request time so that
// updating docs does not require a rebuild — only restarting the
// container (or even just `git pull` if you bind-mount the docs dir).
//
// The handler is intentionally cheap: a single os.ReadFile per request.
// Markdown files are small (≤30 KB) and the dashboard fetches each one
// once per tab change. If this ever becomes a hot path, swap in a
// sync.Map cache invalidated on file mtime — but right now there is
// nothing to optimise.

package api

import (
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/gofiber/fiber/v2"
)

// realtimeDocsDir is resolved relative to the working directory of
// the binary. In the bundled Dockerfile that is /app/docs/realtime;
// in `go run` from the repo root it is ./docs/realtime.
const realtimeDocsDir = "docs/realtime"

// MountRealtimeDocs adds the docs endpoints to the supplied app.
// Called from SetupRealtime so the routes only exist when realtime is
// enabled.
func MountRealtimeDocs(app *fiber.App) {
	app.Get("/api/realtime/docs", listRealtimeDocs)
	app.Get("/api/realtime/docs/:slug", serveRealtimeDoc)
}

// docCategory groups docs by intent for the dashboard sidebar. Slugs
// not in any group fall under "Other" so accidentally dropping a new
// .md still surfaces it.
type docCategory struct {
	Title string   `json:"title"`
	Slugs []string `json:"slugs"`
}

// curatedOrder defines the sidebar grouping. Anything not listed
// here is appended under "Other" preserving alphabetical order.
var curatedOrder = []docCategory{
	{Title: "Getting started", Slugs: []string{"INDEX", "quickstart"}},
	{Title: "Features", Slugs: []string{"postgres-changes", "broadcast", "presence", "rpc"}},
	{Title: "Reference", Slugs: []string{"filters", "auth", "protocol", "sdk"}},
	{Title: "Operations", Slugs: []string{"troubleshooting"}},
}

// listRealtimeDocs returns the docs organised by category. Useful for
// the dashboard sidebar; clients that only want the flat list can
// flatten in 1 line.
func listRealtimeDocs(c *fiber.Ctx) error {
	entries, err := os.ReadDir(realtimeDocsDir)
	if err != nil {
		return fiber.NewError(http.StatusInternalServerError, err.Error())
	}
	available := make(map[string]bool, len(entries))
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if !strings.HasSuffix(name, ".md") {
			continue
		}
		available[strings.TrimSuffix(name, ".md")] = true
	}

	categories := make([]docCategory, 0, len(curatedOrder)+1)
	consumed := make(map[string]bool)
	for _, cat := range curatedOrder {
		group := docCategory{Title: cat.Title}
		for _, slug := range cat.Slugs {
			if available[slug] {
				group.Slugs = append(group.Slugs, slug)
				consumed[slug] = true
			}
		}
		if len(group.Slugs) > 0 {
			categories = append(categories, group)
		}
	}
	// "Other" — anything left over (new doc the operator just dropped
	// in, internal one we forgot to categorise).
	var other []string
	for slug := range available {
		if !consumed[slug] {
			other = append(other, slug)
		}
	}
	if len(other) > 0 {
		sort.Strings(other)
		categories = append(categories, docCategory{Title: "Other", Slugs: other})
	}

	// Flat list for clients that don't care about grouping.
	var flat []string
	for _, c := range categories {
		flat = append(flat, c.Slugs...)
	}
	return c.JSON(fiber.Map{
		"docs":       flat,
		"categories": categories,
	})
}

// serveRealtimeDoc returns the markdown body of one document.
// Slugs are validated against path traversal — only letters, digits,
// hyphens, dots and underscores allowed.
func serveRealtimeDoc(c *fiber.Ctx) error {
	slug := c.Params("slug")
	if !isSafeDocSlug(slug) {
		return fiber.NewError(http.StatusBadRequest, "invalid slug")
	}
	path := filepath.Join(realtimeDocsDir, slug+".md")
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return fiber.NewError(http.StatusNotFound, "doc not found")
		}
		return fiber.NewError(http.StatusInternalServerError, err.Error())
	}
	c.Set("Content-Type", "text/markdown; charset=utf-8")
	c.Set("Cache-Control", "no-cache")
	return c.Send(data)
}

// isSafeDocSlug accepts only characters that can appear in a real
// filename within docs/realtime/. Any `/`, `\`, `..`, or path-like
// construct is rejected. Empty slugs also rejected.
func isSafeDocSlug(s string) bool {
	if s == "" || len(s) > 100 {
		return false
	}
	for _, r := range s {
		ok := (r >= 'a' && r <= 'z') ||
			(r >= 'A' && r <= 'Z') ||
			(r >= '0' && r <= '9') ||
			r == '-' || r == '_' || r == '.'
		if !ok {
			return false
		}
	}
	// Reject leading dot to be safe (no hidden files).
	if s[0] == '.' {
		return false
	}
	return true
}
