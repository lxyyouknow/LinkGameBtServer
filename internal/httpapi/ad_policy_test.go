package httpapi

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAdPolicyPublicHotReload(t *testing.T) {
	path := filepath.Join(t.TempDir(), "policy.json")
	h := NewHandler(slog.New(slog.NewTextHandler(io.Discard, nil)), Dependencies{AdPolicyPath: path})
	for _, valid := range []bool{false, true, false} {
		body := []byte("{}")
		if valid {
			var err error
			body, err = os.ReadFile("../../deploy/config/ad-policy.json")
			if err != nil {
				t.Fatal(err)
			}
		}
		if err := os.WriteFile(path, body, 0600); err != nil {
			t.Fatal(err)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/v1/ads/policy", nil))
		if w.Code != 200 || !strings.Contains(w.Header().Get("Cache-Control"), "no-store") || strings.Contains(w.Body.String(), `"enabled":true`) != valid {
			t.Fatalf("policy response: %d %s", w.Code, w.Body.String())
		}
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/v1/ads/policy", nil))
	if w.Code != http.StatusMethodNotAllowed {
		t.Fatal(w.Code)
	}
}
