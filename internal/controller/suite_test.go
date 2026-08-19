package controller

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/record"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"

	platformv1alpha1 "github.com/7k-inari/inari-operator/api/v1alpha1"
	"github.com/7k-inari/inari-operator/internal/keycloak"
)

var (
	testEnv   *envtest.Environment
	testCfg   *rest.Config
	testClient client.Client
	testScheme *runtime.Scheme
)

func TestMain(m *testing.M) {
	testScheme = runtime.NewScheme()
	utilruntime.Must(clientgoscheme.AddToScheme(testScheme))
	utilruntime.Must(platformv1alpha1.AddToScheme(testScheme))

	repoRoot, err := filepath.Abs("../..")
	if err != nil {
		panic(err)
	}
	testEnv = &envtest.Environment{
		CRDDirectoryPaths: []string{
			filepath.Join(repoRoot, "config", "crd", "bases"),
			filepath.Join("testdata", "crds"),
		},
	}
	testCfg, err = testEnv.Start()
	if err != nil {
		panic(fmt.Sprintf("start envtest: %v", err))
	}
	testClient, err = client.New(testCfg, client.Options{Scheme: testScheme})
	if err != nil {
		panic(err)
	}

	code := m.Run()
	_ = testEnv.Stop()
	os.Exit(code)
}

// --- fake Keycloak Admin REST server ---------------------------------------

type fakeKC struct {
	mu      sync.Mutex
	realms  map[string]map[string]any
	clients map[string][]map[string]any
}

func newFakeKC(t *testing.T) (*fakeKC, *keycloak.Client) {
	t.Helper()
	fk := &fakeKC{realms: map[string]map[string]any{}, clients: map[string][]map[string]any{}}
	srv := httptest.NewServer(http.HandlerFunc(fk.serve))
	t.Cleanup(srv.Close)
	return fk, keycloak.NewClient(srv.URL, "master", "admin", "admin-secret", nil)
}

func (fk *fakeKC) serve(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/")
	w.Header().Set("Content-Type", "application/json")
	if strings.HasSuffix(path, "protocol/openid-connect/token") {
		_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "tok", "expires_in": 300})
		return
	}
	fk.mu.Lock()
	defer fk.mu.Unlock()
	switch {
	case path == "admin/realms" && r.Method == http.MethodPost:
		var rep map[string]any
		_ = json.NewDecoder(r.Body).Decode(&rep)
		name, _ := rep["realm"].(string)
		if _, ok := fk.realms[name]; ok {
			http.Error(w, "conflict", http.StatusConflict)
			return
		}
		fk.realms[name] = rep
		w.WriteHeader(http.StatusCreated)
	case strings.HasPrefix(path, "admin/realms/"):
		rest := strings.TrimPrefix(path, "admin/realms/")
		parts := strings.SplitN(rest, "/", 2)
		realm := parts[0]
		if len(parts) == 1 {
			fk.realm(w, r, realm)
			return
		}
		fk.clientsOp(w, r, realm, parts[1])
	default:
		http.Error(w, "nf", http.StatusNotFound)
	}
}

func (fk *fakeKC) realm(w http.ResponseWriter, r *http.Request, realm string) {
	switch r.Method {
	case http.MethodGet:
		rep, ok := fk.realms[realm]
		if !ok {
			http.Error(w, "nf", http.StatusNotFound)
			return
		}
		_ = json.NewEncoder(w).Encode(rep)
	case http.MethodPut:
		var rep map[string]any
		_ = json.NewDecoder(r.Body).Decode(&rep)
		rep["realm"] = realm
		fk.realms[realm] = rep
		w.WriteHeader(http.StatusNoContent)
	case http.MethodDelete:
		if _, ok := fk.realms[realm]; !ok {
			http.Error(w, "nf", http.StatusNotFound)
			return
		}
		delete(fk.realms, realm)
		delete(fk.clients, realm)
		w.WriteHeader(http.StatusNoContent)
	}
}

func (fk *fakeKC) clientsOp(w http.ResponseWriter, r *http.Request, realm, rest string) {
	if _, ok := fk.realms[realm]; !ok {
		http.Error(w, "nf", http.StatusNotFound)
		return
	}
	switch {
	case rest == "clients" && r.Method == http.MethodGet:
		id := r.URL.Query().Get("clientId")
		out := []map[string]any{}
		for _, c := range fk.clients[realm] {
			if id == "" || c["clientId"] == id {
				out = append(out, c)
			}
		}
		_ = json.NewEncoder(w).Encode(out)
	case rest == "clients" && r.Method == http.MethodPost:
		var rep map[string]any
		_ = json.NewDecoder(r.Body).Decode(&rep)
		cid, _ := rep["clientId"].(string)
		for _, c := range fk.clients[realm] {
			if c["clientId"] == cid {
				http.Error(w, "conflict", http.StatusConflict)
				return
			}
		}
		rep["id"] = "id-" + cid
		fk.clients[realm] = append(fk.clients[realm], rep)
		w.WriteHeader(http.StatusCreated)
	case strings.HasPrefix(rest, "clients/"):
		tail := strings.TrimPrefix(rest, "clients/")
		if strings.HasSuffix(tail, "/client-secret") {
			id := strings.TrimSuffix(tail, "/client-secret")
			_ = json.NewEncoder(w).Encode(map[string]any{"value": "secret-" + id})
			return
		}
		if r.Method == http.MethodDelete {
			id := tail
			for i, c := range fk.clients[realm] {
				if c["id"] == id {
					fk.clients[realm] = append(fk.clients[realm][:i], fk.clients[realm][i+1:]...)
					w.WriteHeader(http.StatusNoContent)
					return
				}
			}
		}
		http.Error(w, "nf", http.StatusNotFound)
	default:
		http.Error(w, "nf", http.StatusNotFound)
	}
}

func (fk *fakeKC) hasRealm(name string) bool {
	fk.mu.Lock()
	defer fk.mu.Unlock()
	_, ok := fk.realms[name]
	return ok
}

func (fk *fakeKC) hasClient(realm, clientID string) bool {
	fk.mu.Lock()
	defer fk.mu.Unlock()
	for _, c := range fk.clients[realm] {
		if c["clientId"] == clientID {
			return true
		}
	}
	return false
}

func newRecorder() *record.FakeRecorder {
	return record.NewFakeRecorder(64)
}

func ns(t *testing.T) string {
	t.Helper()
	return "default"
}

var _ = context.Background
