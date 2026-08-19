// Package keycloak implements a minimal Keycloak Admin REST client used to
// provision tenant realms and clients for workload federation (plan §5.4).
// Direct Admin REST was chosen over Crossplane provider-keycloak; see
// docs/decisions/0001-keycloak-admin-rest.md.
package keycloak

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// RealmRepresentation is the subset of the Keycloak realm representation we manage.
type RealmRepresentation struct {
	Realm       string `json:"realm"`
	DisplayName string `json:"displayName,omitempty"`
	Enabled     bool   `json:"enabled"`
}

// ClientRepresentation is the subset of the Keycloak client representation we manage.
type ClientRepresentation struct {
	ID                     string   `json:"id,omitempty"`
	ClientID               string   `json:"clientId"`
	PublicClient           bool     `json:"publicClient"`
	ServiceAccountsEnabled bool     `json:"serviceAccountsEnabled"`
	StandardFlowEnabled    bool     `json:"standardFlowEnabled"`
	RedirectURIs           []string `json:"redirectUris,omitempty"`
}

// ClientResult describes the outcome of EnsureClient.
type ClientResult struct {
	// ID is the Keycloak-internal UUID of the client.
	ID string
	// Secret is populated for confidential clients.
	Secret string
	// Created reports whether the client was created by this call.
	Created bool
}

// Client is a Keycloak Admin REST client.
type Client struct {
	baseURL      string
	tokenRealm   string
	clientID     string
	clientSecret string
	httpClient   *http.Client
	tokenURL     string

	mu       sync.Mutex
	token    string
	tokenExp time.Time
	nowFunc  func() time.Time
}

// NewClient returns a Client authenticating against tokenRealm with client
// credentials. httpClient may be nil.
func NewClient(baseURL, tokenRealm, clientID, clientSecret string, httpClient *http.Client) *Client {
	base := strings.TrimSuffix(baseURL, "/")
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 30 * time.Second}
	}
	return &Client{
		baseURL:      base,
		tokenRealm:   tokenRealm,
		clientID:     clientID,
		clientSecret: clientSecret,
		httpClient:   httpClient,
		tokenURL:     base + "/realms/" + tokenRealm + "/protocol/openid-connect/token",
		nowFunc:      time.Now,
	}
}

// IssuerURL returns the OIDC issuer URL for a realm.
func (c *Client) IssuerURL(realm string) string {
	return c.baseURL + "/realms/" + realm
}

// EnsureRealm creates the realm if missing and updates it if it drifts.
// Returns whether the realm was created.
func (c *Client) EnsureRealm(ctx context.Context, rep RealmRepresentation) (bool, error) {
	var existing RealmRepresentation
	err := c.do(ctx, http.MethodGet, "/admin/realms/"+url.PathEscape(rep.Realm), nil, &existing)
	switch {
	case err == nil:
		if existing.DisplayName == rep.DisplayName && existing.Enabled == rep.Enabled {
			return false, nil
		}
		rep.Realm = existing.Realm
		if err := c.do(ctx, http.MethodPut, "/admin/realms/"+url.PathEscape(rep.Realm), rep, nil); err != nil {
			return false, fmt.Errorf("update realm %q: %w", rep.Realm, err)
		}
		return false, nil
	case errors.Is(err, errNotFound):
		if err := c.do(ctx, http.MethodPost, "/admin/realms", rep, nil); err != nil {
			return false, fmt.Errorf("create realm %q: %w", rep.Realm, err)
		}
		return true, nil
	default:
		return false, fmt.Errorf("get realm %q: %w", rep.Realm, err)
	}
}

// DeleteRealm removes the realm; missing realms are not an error.
func (c *Client) DeleteRealm(ctx context.Context, realm string) error {
	err := c.do(ctx, http.MethodDelete, "/admin/realms/"+url.PathEscape(realm), nil, nil)
	if err != nil && !errors.Is(err, errNotFound) {
		return fmt.Errorf("delete realm %q: %w", realm, err)
	}
	return nil
}

// EnsureClient creates the client if missing. For confidential clients the
// current secret is fetched and returned.
func (c *Client) EnsureClient(ctx context.Context, realm string, rep ClientRepresentation) (*ClientResult, error) {
	rep.StandardFlowEnabled = len(rep.RedirectURIs) > 0

	existing, err := c.findClient(ctx, realm, rep.ClientID)
	if err != nil {
		return nil, err
	}

	res := &ClientResult{}
	if existing == nil {
		if err := c.do(ctx, http.MethodPost, "/admin/realms/"+url.PathEscape(realm)+"/clients", rep, nil); err != nil {
			return nil, fmt.Errorf("create client %q: %w", rep.ClientID, err)
		}
		res.Created = true
		existing, err = c.findClient(ctx, realm, rep.ClientID)
		if err != nil {
			return nil, err
		}
		if existing == nil {
			return nil, fmt.Errorf("client %q not found after create", rep.ClientID)
		}
	} else if clientDrifted(existing, rep) {
		rep.ID = existing.ID
		path := "/admin/realms/" + url.PathEscape(realm) + "/clients/" + existing.ID
		if err := c.do(ctx, http.MethodPut, path, rep, nil); err != nil {
			return nil, fmt.Errorf("update client %q: %w", rep.ClientID, err)
		}
	}
	res.ID = existing.ID

	if !rep.PublicClient {
		secret, err := c.getClientSecret(ctx, realm, res.ID)
		if err != nil {
			return nil, err
		}
		res.Secret = secret
	}
	return res, nil
}

// DeleteClient removes the client identified by clientID; missing clients are
// not an error.
func (c *Client) DeleteClient(ctx context.Context, realm, clientID string) error {
	existing, err := c.findClient(ctx, realm, clientID)
	if err != nil {
		return err
	}
	if existing == nil {
		return nil
	}
	if err := c.do(ctx, http.MethodDelete, "/admin/realms/"+url.PathEscape(realm)+"/clients/"+existing.ID, nil, nil); err != nil {
		return fmt.Errorf("delete client %q: %w", clientID, err)
	}
	return nil
}

// clientDrifted reports whether the stored client differs from the desired
// representation on the fields we manage.
func clientDrifted(existing *ClientRepresentation, rep ClientRepresentation) bool {
	if existing.PublicClient != rep.PublicClient ||
		existing.ServiceAccountsEnabled != rep.ServiceAccountsEnabled ||
		existing.StandardFlowEnabled != rep.StandardFlowEnabled {
		return true
	}
	if len(existing.RedirectURIs) != len(rep.RedirectURIs) {
		return true
	}
	for i := range rep.RedirectURIs {
		if existing.RedirectURIs[i] != rep.RedirectURIs[i] {
			return true
		}
	}
	return false
}

func (c *Client) findClient(ctx context.Context, realm, clientID string) (*ClientRepresentation, error) {
	var clients []ClientRepresentation
	q := url.Values{"clientId": []string{clientID}}
	path := "/admin/realms/" + url.PathEscape(realm) + "/clients?" + q.Encode()
	if err := c.do(ctx, http.MethodGet, path, nil, &clients); err != nil {
		return nil, fmt.Errorf("list clients in realm %q: %w", realm, err)
	}
	for i := range clients {
		if clients[i].ClientID == clientID {
			return &clients[i], nil
		}
	}
	return nil, nil
}

func (c *Client) getClientSecret(ctx context.Context, realm, id string) (string, error) {
	var out struct {
		Value string `json:"value"`
	}
	path := "/admin/realms/" + url.PathEscape(realm) + "/clients/" + id + "/client-secret"
	if err := c.do(ctx, http.MethodGet, path, nil, &out); err != nil {
		return "", fmt.Errorf("get client secret: %w", err)
	}
	return out.Value, nil
}

var errNotFound = errors.New("keycloak: not found")

// httpError is a non-2xx Admin REST response.
type httpError struct {
	Status int
	Body   string
}

func (e *httpError) Error() string {
	return fmt.Sprintf("keycloak: HTTP %d: %s", e.Status, e.Body)
}

func (c *Client) getToken(ctx context.Context) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.token != "" && c.nowFunc().Before(c.tokenExp.Add(-10*time.Second)) {
		return c.token, nil
	}
	form := url.Values{
		"grant_type":    {"client_credentials"},
		"client_id":     {c.clientID},
		"client_secret": {c.clientSecret},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.tokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("token request: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusOK {
		return "", &httpError{Status: resp.StatusCode, Body: string(body)}
	}
	var tok struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int    `json:"expires_in"`
	}
	if err := json.Unmarshal(body, &tok); err != nil {
		return "", fmt.Errorf("decode token response: %w", err)
	}
	if tok.AccessToken == "" {
		return "", errors.New("keycloak: empty access token")
	}
	c.token = tok.AccessToken
	exp := tok.ExpiresIn
	if exp <= 0 {
		exp = 60
	}
	c.tokenExp = c.nowFunc().Add(time.Duration(exp) * time.Second)
	return c.token, nil
}

func (c *Client) do(ctx context.Context, method, path string, in, out any) error {
	// Retry once on 401: the cached token may have been invalidated early
	// (e.g. Keycloak restart); drop it and re-authenticate.
	for attempt := 0; ; attempt++ {
		status, raw, err := c.doOnce(ctx, method, path, in, out)
		if err != nil {
			return err
		}
		if status == http.StatusUnauthorized && attempt == 0 {
			c.invalidateToken()
			continue
		}
		if status == http.StatusNotFound {
			return errNotFound
		}
		if status < 200 || status >= 300 {
			return &httpError{Status: status, Body: string(raw)}
		}
		return nil
	}
}

// doOnce performs a single authenticated request. Transport/encode/decode
// failures are returned as errors; any received HTTP response yields its
// status and body with a nil error, and 2xx bodies are decoded into out.
func (c *Client) doOnce(ctx context.Context, method, path string, in, out any) (int, []byte, error) {
	var body io.Reader
	if in != nil {
		raw, err := json.Marshal(in)
		if err != nil {
			return 0, nil, err
		}
		body = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, body)
	if err != nil {
		return 0, nil, err
	}
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	tok, err := c.getToken(ctx)
	if err != nil {
		return 0, nil, err
	}
	req.Header.Set("Authorization", "Bearer "+tok)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))

	if resp.StatusCode >= 200 && resp.StatusCode < 300 && out != nil && len(raw) > 0 {
		if err := json.Unmarshal(raw, out); err != nil {
			return resp.StatusCode, nil, fmt.Errorf("decode response: %w", err)
		}
	}
	return resp.StatusCode, raw, nil
}

// invalidateToken drops the cached token so the next request re-authenticates.
func (c *Client) invalidateToken() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.token = ""
	c.tokenExp = time.Time{}
}
