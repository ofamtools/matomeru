package mcpui

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestHTTPDeleteEndsSession(t *testing.T) {
	f := setup(t)
	h := NewHTTPHandler(f.store, f.r)
	for _, p := range []string{"/mcp", "/mcp/pane-1"} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodDelete, p, nil))
		if rec.Code != http.StatusNoContent {
			t.Fatalf("DELETE %s status = %d, want 204", p, rec.Code)
		}
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/mcp", nil))
	if got := rec.Header().Get("Allow"); got != "POST, DELETE" {
		t.Fatalf("GET /mcp Allow = %q, want %q", got, "POST, DELETE")
	}
}
