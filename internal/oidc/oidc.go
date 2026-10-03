// Package oidc is a small OpenID Connect relying party for occulited's login page (task 6):
// discovery, the authorization-code flow with PKCE, claims from the userinfo endpoint. No JWT
// library: the ID token is only read for its nonce, the claims that matter are fetched from the
// issuer over TLS with the access token, which is what the userinfo endpoint is for.
package oidc

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
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

// Config is what the client needs; see config.OIDCConfig for the meaning of each field.
type Config struct {
	Issuer        string
	ClientID      string
	ClientSecret  string
	UsernameClaim string
	Scopes        string
}

// Identity is the outcome of a login. Username is the value of the configured claim exactly as
// the provider sent it (task 19, D-53): the account it names is matched by that string and nothing
// else - no case folding, no characters replaced, no fall-back to the e-mail address - so what
// the provider calls the user is what the administrator has to create here.
type Identity struct {
	ReturnTo string // the local path Start was given
	// Purpose and Started are StartOptions' purpose and when the round trip began; AuthTime is
	// the ID token's auth_time, zero when it has none
	Purpose  string
	Started  time.Time
	AuthTime time.Time
	Subject  string
	Username string
	Email    string
	Claims   map[string]any
}

type discovery struct {
	AuthorizationEndpoint string `json:"authorization_endpoint"`
	TokenEndpoint         string `json:"token_endpoint"`
	UserinfoEndpoint      string `json:"userinfo_endpoint"`
	Issuer                string `json:"issuer"`
}

type pending struct {
	verifier string
	nonce    string
	redirect string
	returnTo string
	purpose  string
	created  time.Time
}

// StartOptions: ForceLogin asks the provider for a fresh login (prompt=login, max_age=0) - a
// confirmation, not a sign-in (task 154, D-104); Purpose comes back in the Identity.
type StartOptions struct {
	ForceLogin bool
	Purpose    string
}

// Client is one provider.
type Client struct {
	Config Config
	HTTP   *http.Client

	mu      sync.Mutex
	disc    *discovery
	discAt  time.Time
	pending map[string]pending
}

// httpFor is a 20 s client that trusts roots besides nothing else; nil roots are the system's.
func httpFor(roots *x509.CertPool) *http.Client {
	if roots == nil {
		return &http.Client{Timeout: 20 * time.Second}
	}
	tr := http.DefaultTransport.(*http.Transport).Clone()
	tr.TLSClientConfig = &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12}
	return &http.Client{Timeout: 20 * time.Second, Transport: tr}
}

// SetRootCAs makes the client trust roots for the provider's TLS (openccu-lite task 230: the
// system's pool plus the anchors an administrator added for OIDC); nil is the system's pool. The
// discovery is fetched again with it.
func (c *Client) SetRootCAs(roots *x509.CertPool) {
	c.SetHTTP(httpFor(roots))
}

// SetHTTP hands the client the HTTP client its provider calls go through (openccu-lite task 232:
// the trust store's, which verifies with the OIDC pool and the pins and records a lost handshake
// for the Status page). The discovery is fetched again with it.
func (c *Client) SetHTTP(h *http.Client) {
	if h == nil {
		h = &http.Client{Timeout: 20 * time.Second}
	}
	c.mu.Lock()
	c.HTTP, c.disc = h, nil
	c.mu.Unlock()
}

func (c *Client) client() *http.Client {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.HTTP
}

// New returns a client; HTTP defaults to a 20 s client.
func New(c Config) *Client {
	if c.UsernameClaim == "" {
		c.UsernameClaim = "preferred_username"
	}
	if c.Scopes == "" {
		c.Scopes = "openid profile email"
	}
	return &Client{Config: c, HTTP: &http.Client{Timeout: 20 * time.Second}, pending: map[string]pending{}}
}

func (c *Client) discover(ctx context.Context) (*discovery, error) {
	c.mu.Lock()
	if c.disc != nil && time.Since(c.discAt) < time.Hour {
		d := c.disc
		c.mu.Unlock()
		return d, nil
	}
	c.mu.Unlock()
	u := strings.TrimSuffix(c.Config.Issuer, "/") + "/.well-known/openid-configuration"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	res, err := c.client().Do(req)
	if err != nil {
		return nil, fmt.Errorf("discovery: %w", err)
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		return nil, fmt.Errorf("discovery: HTTP %d", res.StatusCode)
	}
	var d discovery
	if err := json.NewDecoder(io.LimitReader(res.Body, 1<<20)).Decode(&d); err != nil {
		return nil, fmt.Errorf("discovery: %w", err)
	}
	if d.AuthorizationEndpoint == "" || d.TokenEndpoint == "" || d.UserinfoEndpoint == "" {
		return nil, errors.New("discovery: the provider does not announce authorization, token and userinfo endpoints")
	}
	c.mu.Lock()
	c.disc, c.discAt = &d, time.Now()
	c.mu.Unlock()
	return &d, nil
}

func random(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}

// Start returns the URL to send the browser to. redirectURI is this box's callback, returnTo
// the local path to land on afterwards; state is kept for ten minutes.
func (c *Client) Start(ctx context.Context, redirectURI, returnTo string) (string, error) {
	return c.StartWith(ctx, redirectURI, returnTo, StartOptions{})
}

// StartWith is Start with options.
func (c *Client) StartWith(ctx context.Context, redirectURI, returnTo string, opt StartOptions) (string, error) {
	d, err := c.discover(ctx)
	if err != nil {
		return "", err
	}
	state, nonce, verifier := random(24), random(24), random(48)
	sum := sha256.Sum256([]byte(verifier))
	challenge := base64.RawURLEncoding.EncodeToString(sum[:])
	c.mu.Lock()
	for k, p := range c.pending {
		if time.Since(p.created) > 10*time.Minute {
			delete(c.pending, k)
		}
	}
	if len(c.pending) >= 1000 {
		// the start route is open: whoever floods it loses the oldest states, nobody else
		oldest, at := "", time.Now()
		for k, p := range c.pending {
			if p.created.Before(at) {
				oldest, at = k, p.created
			}
		}
		delete(c.pending, oldest)
	}
	c.pending[state] = pending{verifier: verifier, nonce: nonce, redirect: redirectURI, returnTo: returnTo, purpose: opt.Purpose, created: time.Now()}
	c.mu.Unlock()
	q := url.Values{
		"response_type":         {"code"},
		"client_id":             {c.Config.ClientID},
		"redirect_uri":          {redirectURI},
		"scope":                 {c.Config.Scopes},
		"state":                 {state},
		"nonce":                 {nonce},
		"code_challenge":        {challenge},
		"code_challenge_method": {"S256"},
	}
	if opt.ForceLogin {
		q.Set("prompt", "login")
		q.Set("max_age", "0")
	}
	sep := "?"
	if strings.Contains(d.AuthorizationEndpoint, "?") {
		sep = "&"
	}
	return d.AuthorizationEndpoint + sep + q.Encode(), nil
}

// Finish exchanges the code and fetches the claims. state must be one Start handed out.
func (c *Client) Finish(ctx context.Context, state, code string) (*Identity, error) {
	c.mu.Lock()
	p, ok := c.pending[state]
	delete(c.pending, state)
	c.mu.Unlock()
	if !ok || time.Since(p.created) > 10*time.Minute {
		return nil, errors.New("unknown or expired login state")
	}
	d, err := c.discover(ctx)
	if err != nil {
		return nil, err
	}
	form := url.Values{
		"grant_type":    {"authorization_code"},
		"code":          {code},
		"redirect_uri":  {p.redirect},
		"client_id":     {c.Config.ClientID},
		"code_verifier": {p.verifier},
	}
	if c.Config.ClientSecret != "" {
		form.Set("client_secret", c.Config.ClientSecret)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, d.TokenEndpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	res, err := c.client().Do(req)
	if err != nil {
		return nil, fmt.Errorf("token: %w", err)
	}
	defer res.Body.Close()
	var tok struct {
		AccessToken string `json:"access_token"`
		IDToken     string `json:"id_token"`
		TokenType   string `json:"token_type"`
		Error       string `json:"error"`
		ErrorDesc   string `json:"error_description"`
	}
	if err := json.NewDecoder(io.LimitReader(res.Body, 1<<20)).Decode(&tok); err != nil {
		return nil, fmt.Errorf("token: %w", err)
	}
	if res.StatusCode != 200 || tok.AccessToken == "" {
		return nil, fmt.Errorf("token: HTTP %d %s %s", res.StatusCode, tok.Error, tok.ErrorDesc)
	}
	// the nonce ties the ID token to this login; the payload is read, not verified - the claims
	// used below come from userinfo over TLS, and the token itself came from the token endpoint
	// over TLS, which is what auth_time is taken on trust from
	var authTime time.Time
	if tok.IDToken != "" {
		if n, _ := jwtClaim(tok.IDToken, "nonce").(string); n != "" && n != p.nonce {
			return nil, errors.New("id_token nonce mismatch")
		}
		if at, ok := jwtClaim(tok.IDToken, "auth_time").(float64); ok && at > 0 {
			authTime = time.Unix(int64(at), 0)
		}
	}
	req, err = http.NewRequestWithContext(ctx, http.MethodGet, d.UserinfoEndpoint, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+tok.AccessToken)
	res2, err := c.client().Do(req)
	if err != nil {
		return nil, fmt.Errorf("userinfo: %w", err)
	}
	defer res2.Body.Close()
	if res2.StatusCode != 200 {
		return nil, fmt.Errorf("userinfo: HTTP %d", res2.StatusCode)
	}
	var claims map[string]any
	if err := json.NewDecoder(io.LimitReader(res2.Body, 1<<20)).Decode(&claims); err != nil {
		return nil, fmt.Errorf("userinfo: %w", err)
	}
	id := &Identity{Claims: claims, ReturnTo: p.returnTo, Purpose: p.purpose, Started: p.created, AuthTime: authTime}
	id.Subject, _ = claims["sub"].(string)
	id.Email, _ = claims["email"].(string)
	id.Username, _ = claims[c.Config.UsernameClaim].(string)
	if id.Subject == "" {
		return nil, errors.New("userinfo: no subject in the claims")
	}
	if id.Username == "" {
		return nil, fmt.Errorf("userinfo: the claim %q is missing or not a string", c.Config.UsernameClaim)
	}
	return id, nil
}

// jwtClaim reads one claim from a JWT payload without verifying it.
func jwtClaim(token, name string) any {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return nil
	}
	b, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return nil
	}
	var m map[string]any
	if json.Unmarshal(b, &m) != nil {
		return nil
	}
	return m[name]
}

// CheckResult is what a test of a provider found (openccu-lite task 230's Test button).
type CheckResult struct {
	// Discovery: the metadata was read and names the three endpoints
	OK    bool   `json:"ok"`
	Error string `json:"error,omitempty"`
	// TLS: the chain the connection was verified with, the leaf first and the trusted root last;
	// empty for plain http or when the handshake failed - and when the client verified by itself
	// (task 232's pins), which leaves Peer, the chain the server presented, for the caller to judge
	Verified []*x509.Certificate `json:"-"`
	Peer     []*x509.Certificate `json:"-"`
	// the metadata's own values
	Issuer                string `json:"issuer,omitempty"`
	AuthorizationEndpoint string `json:"authorization_endpoint,omitempty"`
	TokenEndpoint         string `json:"token_endpoint,omitempty"`
	UserinfoEndpoint      string `json:"userinfo_endpoint,omitempty"`
	// IssuerMismatch: the metadata's issuer is not the one configured (trailing slashes aside)
	IssuerMismatch bool `json:"issuer_mismatch,omitempty"`
}

// Check reads issuer's discovery document through client (nil: a 20 s client on the system's
// pool), as the login would, and says what it found - without caching anything or touching a
// running client. CheckWith is Check with a pool.
func Check(ctx context.Context, issuer string, client *http.Client) CheckResult {
	var r CheckResult
	if client == nil {
		client = httpFor(nil)
	}
	u := strings.TrimSuffix(issuer, "/") + "/.well-known/openid-configuration"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		r.Error = err.Error()
		return r
	}
	res, err := client.Do(req)
	if err != nil {
		r.Error = err.Error()
		return r
	}
	defer res.Body.Close()
	if res.TLS != nil {
		r.Peer = res.TLS.PeerCertificates
		if len(res.TLS.VerifiedChains) > 0 {
			r.Verified = res.TLS.VerifiedChains[0]
		}
	}
	if res.StatusCode != 200 {
		r.Error = fmt.Sprintf("the discovery document answered HTTP %d", res.StatusCode)
		return r
	}
	var d discovery
	if err := json.NewDecoder(io.LimitReader(res.Body, 1<<20)).Decode(&d); err != nil {
		r.Error = "the discovery document is not JSON: " + err.Error()
		return r
	}
	r.Issuer, r.AuthorizationEndpoint, r.TokenEndpoint, r.UserinfoEndpoint = d.Issuer, d.AuthorizationEndpoint, d.TokenEndpoint, d.UserinfoEndpoint
	r.IssuerMismatch = d.Issuer != "" && strings.TrimSuffix(d.Issuer, "/") != strings.TrimSuffix(issuer, "/")
	if d.AuthorizationEndpoint == "" || d.TokenEndpoint == "" || d.UserinfoEndpoint == "" {
		r.Error = "the provider does not announce authorization, token and userinfo endpoints"
		return r
	}
	r.OK = true
	return r
}

// CheckWith is Check trusting roots (nil: the system's pool).
func CheckWith(ctx context.Context, issuer string, roots *x509.CertPool) CheckResult {
	return Check(ctx, issuer, httpFor(roots))
}
