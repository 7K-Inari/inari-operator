package keycloak

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// A cached-but-invalidated token (401) must trigger re-authentication and a
// single retry, not a hard failure.
func TestRetryOnUnauthorizedRefreshesToken(t *testing.T) {
	var mu sync.Mutex
	tokenCalls := 0
	realmGets := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		if strings.HasSuffix(r.URL.Path, "protocol/openid-connect/token") {
			tokenCalls++
			_ = json.NewEncoder(w).Encode(map[string]any{
				"access_token": "tok-" + strings.Repeat("x", tokenCalls), // distinct per call
				"expires_in":   300,
			})
			return
		}
		if r.URL.Path == "/admin/realms/r" && r.Method == http.MethodGet {
			realmGets++
			if realmGets == 1 {
				// First attempt uses the (server-side invalidated) first token.
				http.Error(w, "expired", http.StatusUnauthorized)
				return
			}
			if !strings.HasPrefix(r.Header.Get("Authorization"), "Bearer tok-") {
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
			_ = json.NewEncoder(w).Encode(RealmRepresentation{Realm: "r", Enabled: true})
			return
		}
		http.Error(w, "nf", http.StatusNotFound)
	}))
	t.Cleanup(srv.Close)

	c := NewClient(srv.URL, "master", "admin", "admin-secret", nil)
	created, err := c.EnsureRealm(context.Background(), RealmRepresentation{Realm: "r", Enabled: true})
	if err != nil {
		t.Fatalf("expected 401 retry to succeed, got: %v", err)
	}
	if created {
		t.Fatal("realm exists; created should be false")
	}
	mu.Lock()
	defer mu.Unlock()
	if tokenCalls != 2 {
		t.Fatalf("expected 2 token requests (initial + refresh), got %d", tokenCalls)
	}
	if realmGets != 2 {
		t.Fatalf("expected 2 realm GETs (401 + retry), got %d", realmGets)
	}
}

// A persistent 401 must surface as an error after the single retry.
func TestRetryOnUnauthorizedGivesUpAfterOneRetry(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if strings.HasSuffix(r.URL.Path, "protocol/openid-connect/token") {
			_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "tok", "expires_in": 300})
			return
		}
		http.Error(w, "denied", http.StatusUnauthorized)
	}))
	t.Cleanup(srv.Close)

	c := NewClient(srv.URL, "master", "admin", "admin-secret", nil)
	_, err := c.EnsureRealm(context.Background(), RealmRepresentation{Realm: "r", Enabled: true})
	if err == nil {
		t.Fatal("expected error on persistent 401")
	}
	var he *httpError
	if !errors.As(err, &he) {
		t.Fatalf("expected httpError, got %T: %v", err, err)
	}
	if he.Status != http.StatusUnauthorized {
		t.Fatalf("status = %d", he.Status)
	}
}
