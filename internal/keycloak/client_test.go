package keycloak

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
)

// fakeKeycloak records Admin REST calls and serves canned responses.
type fakeKeycloak struct {
	t *testing.T

	mu        sync.Mutex
	realms    map[string]RealmRepresentation
	clients   map[string][]ClientRepresentation // realm -> clients
	requests  []string
	tokenHits int
}

func newFakeKeycloak(t *testing.T) (*fakeKeycloak, *httptest.Server) {
	t.Helper()
	fk := &fakeKeycloak{
		t:       t,
		realms:  map[string]RealmRepresentation{},
		clients: map[string][]ClientRepresentation{},
	}
	srv := httptest.NewServer(http.HandlerFunc(fk.serve))
	t.Cleanup(srv.Close)
	return fk, srv
}

func (fk *fakeKeycloak) record(r *http.Request) {
	fk.mu.Lock()
	defer fk.mu.Unlock()
	fk.requests = append(fk.requests, r.Method+" "+r.URL.Path)
}

func (fk *fakeKeycloak) serve(w http.ResponseWriter, r *http.Request) {
	fk.record(r)
	path := strings.TrimPrefix(r.URL.Path, "/")
	switch {
	case strings.HasSuffix(path, "protocol/openid-connect/token"):
		if err := r.ParseForm(); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if r.Form.Get("grant_type") != "client_credentials" {
			http.Error(w, "unsupported grant", http.StatusBadRequest)
			return
		}
		fk.mu.Lock()
		fk.tokenHits++
		fk.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "tok", "expires_in": 300})
		return
	}

	if r.Header.Get("Authorization") != "Bearer tok" {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	switch {
	case path == "admin/realms" && r.Method == http.MethodPost:
		var rep RealmRepresentation
		if err := json.NewDecoder(r.Body).Decode(&rep); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		fk.mu.Lock()
		if _, exists := fk.realms[rep.Realm]; exists {
			fk.mu.Unlock()
			http.Error(w, "conflict", http.StatusConflict)
			return
		}
		fk.realms[rep.Realm] = rep
		fk.mu.Unlock()
		w.WriteHeader(http.StatusCreated)

	case strings.HasPrefix(path, "admin/realms/"):
		rest := strings.TrimPrefix(path, "admin/realms/")
		parts := strings.Split(rest, "/")
		realm := parts[0]
		if len(parts) == 1 {
			fk.handleRealm(w, r, realm)
			return
		}
		if len(parts) >= 2 && parts[1] == "clients" {
			fk.handleClients(w, r, realm, parts[2:])
			return
		}
		http.Error(w, "not found", http.StatusNotFound)

	default:
		http.Error(w, "not found: "+path, http.StatusNotFound)
	}
}

func (fk *fakeKeycloak) handleRealm(w http.ResponseWriter, r *http.Request, realm string) {
	fk.mu.Lock()
	defer fk.mu.Unlock()
	switch r.Method {
	case http.MethodGet:
		rep, ok := fk.realms[realm]
		if !ok {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		_ = json.NewEncoder(w).Encode(rep)
	case http.MethodPut:
		var rep RealmRepresentation
		if err := json.NewDecoder(r.Body).Decode(&rep); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if _, ok := fk.realms[realm]; !ok {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		rep.Realm = realm
		fk.realms[realm] = rep
		w.WriteHeader(http.StatusNoContent)
	case http.MethodDelete:
		if _, ok := fk.realms[realm]; !ok {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		delete(fk.realms, realm)
		delete(fk.clients, realm)
		w.WriteHeader(http.StatusNoContent)
	default:
		http.Error(w, "method", http.StatusMethodNotAllowed)
	}
}

func (fk *fakeKeycloak) handleClients(w http.ResponseWriter, r *http.Request, realm string, rest []string) {
	fk.mu.Lock()
	defer fk.mu.Unlock()
	if len(rest) == 0 {
		switch r.Method {
		case http.MethodGet:
			_ = json.NewEncoder(w).Encode(fk.clients[realm])
		case http.MethodPost:
			var rep ClientRepresentation
			if err := json.NewDecoder(r.Body).Decode(&rep); err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			for _, c := range fk.clients[realm] {
				if c.ClientID == rep.ClientID {
					http.Error(w, "conflict", http.StatusConflict)
					return
				}
			}
			rep.ID = fmt.Sprintf("id-%s", rep.ClientID)
			fk.clients[realm] = append(fk.clients[realm], rep)
			w.WriteHeader(http.StatusCreated)
		default:
			http.Error(w, "method", http.StatusMethodNotAllowed)
		}
		return
	}
	id := rest[0]
	idx := -1
	for i, c := range fk.clients[realm] {
		if c.ID == id {
			idx = i
			break
		}
	}
	if idx < 0 {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	if len(rest) == 1 && r.Method == http.MethodDelete {
		fk.clients[realm] = append(fk.clients[realm][:idx], fk.clients[realm][idx+1:]...)
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if len(rest) == 2 && rest[1] == "client-secret" && r.Method == http.MethodGet {
		_ = json.NewEncoder(w).Encode(map[string]any{"type": "secret", "value": "secret-" + id})
		return
	}
	http.Error(w, "not found", http.StatusNotFound)
}

func testClient(t *testing.T, baseURL string) *Client {
	t.Helper()
	return NewClient(baseURL, "master", "admin-client", "admin-secret", nil)
}

func TestEnsureRealmCreates(t *testing.T) {
	fk, srv := newFakeKeycloak(t)
	c := testClient(t, srv.URL)

	created, err := c.EnsureRealm(context.Background(), RealmRepresentation{Realm: "tenant-a", Enabled: true})
	if err != nil {
		t.Fatalf("EnsureRealm: %v", err)
	}
	if !created {
		t.Fatal("expected created=true")
	}
	if fk.realms["tenant-a"].Realm != "tenant-a" {
		t.Fatal("realm not created on server")
	}
}

func TestEnsureRealmIdempotent(t *testing.T) {
	fk, srv := newFakeKeycloak(t)
	c := testClient(t, srv.URL)

	if _, err := c.EnsureRealm(context.Background(), RealmRepresentation{Realm: "tenant-a", Enabled: true}); err != nil {
		t.Fatalf("first EnsureRealm: %v", err)
	}
	created, err := c.EnsureRealm(context.Background(), RealmRepresentation{Realm: "tenant-a", Enabled: true})
	if err != nil {
		t.Fatalf("second EnsureRealm: %v", err)
	}
	if created {
		t.Fatal("expected created=false on second call")
	}
	var posts int
	for _, r := range fk.requests {
		if r == "POST /admin/realms" {
			posts++
		}
	}
	if posts != 1 {
		t.Fatalf("expected exactly 1 POST /admin/realms, got %d (%v)", posts, fk.requests)
	}
}

func TestDeleteRealm(t *testing.T) {
	fk, srv := newFakeKeycloak(t)
	c := testClient(t, srv.URL)

	if _, err := c.EnsureRealm(context.Background(), RealmRepresentation{Realm: "tenant-a", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	if err := c.DeleteRealm(context.Background(), "tenant-a"); err != nil {
		t.Fatalf("DeleteRealm: %v", err)
	}
	if _, ok := fk.realms["tenant-a"]; ok {
		t.Fatal("realm still present")
	}
	// Deleting a missing realm must not error (idempotent teardown).
	if err := c.DeleteRealm(context.Background(), "tenant-a"); err != nil {
		t.Fatalf("DeleteRealm missing realm: %v", err)
	}
}

func TestEnsureClientCreatesWithSecret(t *testing.T) {
	_, srv := newFakeKeycloak(t)
	c := testClient(t, srv.URL)

	if _, err := c.EnsureRealm(context.Background(), RealmRepresentation{Realm: "tenant-a", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	res, err := c.EnsureClient(context.Background(), "tenant-a", ClientRepresentation{
		ClientID:               "app",
		PublicClient:           false,
		ServiceAccountsEnabled: true,
		RedirectURIs:           []string{"https://app.example.com/cb"},
	})
	if err != nil {
		t.Fatalf("EnsureClient: %v", err)
	}
	if !res.Created {
		t.Fatal("expected created")
	}
	if res.Secret != "secret-id-app" {
		t.Fatalf("expected secret from server, got %q", res.Secret)
	}
}

func TestEnsureClientIdempotent(t *testing.T) {
	_, srv := newFakeKeycloak(t)
	c := testClient(t, srv.URL)

	if _, err := c.EnsureRealm(context.Background(), RealmRepresentation{Realm: "tenant-a", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	rep := ClientRepresentation{ClientID: "app", ServiceAccountsEnabled: true}
	if _, err := c.EnsureClient(context.Background(), "tenant-a", rep); err != nil {
		t.Fatal(err)
	}
	res, err := c.EnsureClient(context.Background(), "tenant-a", rep)
	if err != nil {
		t.Fatal(err)
	}
	if res.Created {
		t.Fatal("expected created=false")
	}
}

func TestDeleteClient(t *testing.T) {
	fk, srv := newFakeKeycloak(t)
	c := testClient(t, srv.URL)

	if _, err := c.EnsureRealm(context.Background(), RealmRepresentation{Realm: "tenant-a", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := c.EnsureClient(context.Background(), "tenant-a", ClientRepresentation{ClientID: "app"}); err != nil {
		t.Fatal(err)
	}
	if err := c.DeleteClient(context.Background(), "tenant-a", "app"); err != nil {
		t.Fatalf("DeleteClient: %v", err)
	}
	if len(fk.clients["tenant-a"]) != 0 {
		t.Fatal("client still present")
	}
	if err := c.DeleteClient(context.Background(), "tenant-a", "app"); err != nil {
		t.Fatalf("DeleteClient missing client: %v", err)
	}
}

func TestTokenIsCached(t *testing.T) {
	fk, srv := newFakeKeycloak(t)
	c := testClient(t, srv.URL)

	for i := 0; i < 3; i++ {
		if _, err := c.EnsureRealm(context.Background(), RealmRepresentation{Realm: fmt.Sprintf("r%d", i), Enabled: true}); err != nil {
			t.Fatal(err)
		}
	}
	if fk.tokenHits != 1 {
		t.Fatalf("expected token to be fetched once, got %d", fk.tokenHits)
	}
}

func TestBaseURLTrailingSlash(t *testing.T) {
	_, srv := newFakeKeycloak(t)
	c := NewClient(srv.URL+"/", "master", "admin-client", "admin-secret", nil)
	if _, err := c.EnsureRealm(context.Background(), RealmRepresentation{Realm: "tenant-a", Enabled: true}); err != nil {
		t.Fatalf("EnsureRealm with trailing slash base URL: %v", err)
	}
}

func TestTokenRequestUsesConfiguredRealm(t *testing.T) {
	fk, srv := newFakeKeycloak(t)
	c := testClient(t, srv.URL)
	if _, err := c.EnsureRealm(context.Background(), RealmRepresentation{Realm: "x", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	want := "POST /realms/master/protocol/openid-connect/token"
	for _, r := range fk.requests {
		if r == want {
			return
		}
	}
	t.Fatalf("expected %q in requests %v", want, fk.requests)
}

var _ = url.Values{} // keep url import if needed later
