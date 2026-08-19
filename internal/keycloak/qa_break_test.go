package keycloak

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// QA: EnsureClient must correct drift — if the desired representation changes
// (e.g. redirect URIs), the existing Keycloak client must be updated, not
// silently left stale (plan principle: desired state, eventually reconciled).
func TestEnsureClientCorrectsDrift(t *testing.T) {
	var mu sync.Mutex
	var stored ClientRepresentation
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		path := strings.TrimPrefix(r.URL.Path, "/")
		switch {
		case strings.HasSuffix(path, "protocol/openid-connect/token"):
			_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "tok", "expires_in": 300})
		case path == "admin/realms/r/clients" && r.Method == http.MethodGet:
			out := []ClientRepresentation{}
			if stored.ClientID != "" {
				out = append(out, stored)
			}
			_ = json.NewEncoder(w).Encode(out)
		case path == "admin/realms/r/clients" && r.Method == http.MethodPost:
			var rep ClientRepresentation
			_ = json.NewDecoder(r.Body).Decode(&rep)
			rep.ID = "id-" + rep.ClientID
			stored = rep
			w.WriteHeader(http.StatusCreated)
		case path == "admin/realms/r/clients/id-app" && r.Method == http.MethodPut:
			var rep ClientRepresentation
			_ = json.NewDecoder(r.Body).Decode(&rep)
			rep.ID = stored.ID
			stored = rep
			w.WriteHeader(http.StatusNoContent)
		case path == "admin/realms/r/clients/id-app/client-secret":
			_ = json.NewEncoder(w).Encode(map[string]any{"value": "s3cret"})
		default:
			http.Error(w, "nf: "+r.Method+" "+path, http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)

	c := NewClient(srv.URL, "master", "admin", "admin-secret", nil)
	ctx := context.Background()

	if _, err := c.EnsureClient(ctx, "r", ClientRepresentation{
		ClientID: "app", PublicClient: true, RedirectURIs: []string{"https://a.example.com/cb"},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := c.EnsureClient(ctx, "r", ClientRepresentation{
		ClientID: "app", PublicClient: true, RedirectURIs: []string{"https://b.example.com/cb"},
	}); err != nil {
		t.Fatal(err)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(stored.RedirectURIs) != 1 || stored.RedirectURIs[0] != "https://b.example.com/cb" {
		t.Fatalf("BUG: drift not corrected, stored redirectUris = %v", stored.RedirectURIs)
	}
}
