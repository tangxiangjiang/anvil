// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

package mcp

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

const (
	callbackPath = "/callback"
	// flowLifetime is how long a person has between being shown the browser and
	// the callback arriving. One request, then the listener is gone.
	flowLifetime = 5 * time.Minute
	// refreshSkew refreshes before the token actually expires, so a request in
	// flight at the boundary does not fail.
	refreshSkew = 60 * time.Second
)

// OAuthConfig describes one server's sign-in.
type OAuthConfig struct {
	Server       string
	ServerURL    string
	TokenDir     string
	ClientID     string
	ClientSecret string
	Scope        string
	HTTPClient   *http.Client
	// Now is a test hook; nil means time.Now.
	Now func() time.Time
}

// OAuth runs the authorization-code + PKCE flow for one remote server and
// owns that server's token file. The runtime holds one per entry; the session
// gets Token as its per-request bearer-token source.
type OAuth struct {
	cfg  OAuthConfig
	http *http.Client
	now  func() time.Time

	mu      sync.Mutex
	pending *pendingFlow
}

// Authorization is what a caller shows the browser.
type Authorization struct {
	URL       string
	ExpiresAt time.Time
}

type asEndpoints struct {
	Issuer                string
	AuthorizationEndpoint string
	TokenEndpoint         string
	RegistrationEndpoint  string
	CodeChallengeMethods  []string
	ScopesSupported       []string
}

type pendingFlow struct {
	url          string
	expiresAt    time.Time
	verifier     string
	state        string
	redirectURI  string
	clientID     string
	clientSecret string
	endpoints    asEndpoints
	listener     net.Listener
	server       *http.Server
	result       chan flowResult
	done         chan struct{}
	stopOnce     sync.Once
}

// stop closes the listener exactly once and wakes Wait.
func (p *pendingFlow) stop() {
	p.stopOnce.Do(func() {
		close(p.done)
		if p.server != nil {
			_ = p.server.Close()
		} else if p.listener != nil {
			_ = p.listener.Close()
		}
	})
}

type flowResult struct {
	code string
	err  error
}

// NewOAuth prepares the helper; no discovery happens until Begin or Token.
func NewOAuth(cfg OAuthConfig) *OAuth {
	client := cfg.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	now := cfg.Now
	if now == nil {
		now = time.Now
	}
	cfg.ServerURL = normalizeURL(cfg.ServerURL)
	return &OAuth{cfg: cfg, http: client, now: now}
}

// HasToken reports whether a token file exists for this server. It is a
// cheap question with no secret in the answer.
func (o *OAuth) HasToken() bool {
	rec, err := loadTokenRecord(o.cfg.TokenDir, o.cfg.Server, o.cfg.ServerURL)
	return err == nil && rec != nil && rec.AccessToken != ""
}

// Token returns the bearer token for one request, refreshing it when it is
// within refreshSkew of expiry. No stored token is not an error: the request
// goes out without one and the server's 401 starts the sign-in conversation.
func (o *OAuth) Token(ctx context.Context) (string, error) {
	rec, err := loadTokenRecord(o.cfg.TokenDir, o.cfg.Server, o.cfg.ServerURL)
	if err != nil || rec == nil || rec.AccessToken == "" {
		return "", err
	}
	if rec.ExpiresAt.IsZero() || o.now().Before(rec.ExpiresAt.Add(-refreshSkew)) {
		return rec.AccessToken, nil
	}
	if rec.RefreshToken == "" {
		// Nothing to refresh with; let the server answer 401 or keep working.
		return rec.AccessToken, nil
	}
	updated, err := o.refresh(ctx, rec)
	if err != nil {
		var oauthErr *oauthResponseError
		if errors.As(err, &oauthErr) && oauthErr.code == "invalid_grant" {
			// The grant is gone. Keep the registration (client id and secret),
			// drop the tokens, and ask for a new sign-in.
			rec.AccessToken = ""
			rec.RefreshToken = ""
			rec.ExpiresAt = time.Time{}
			if saveErr := saveTokenRecord(o.cfg.TokenDir, rec); saveErr != nil {
				return "", saveErr
			}
			return "", &AuthRequiredError{
				Server:              o.cfg.Server,
				ResourceMetadataURL: resourceMetadataURL(o.cfg.ServerURL),
			}
		}
		return "", err
	}
	return updated.AccessToken, nil
}

// Begin discovers the authorization server, registers a client if there is no
// usable one, and opens a loopback listener. The returned URL is what the
// extension opens; Wait completes the flow.
func (o *OAuth) Begin(ctx context.Context, challenge string) (Authorization, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if p := o.pending; p != nil {
		if o.now().Before(p.expiresAt) {
			return Authorization{URL: p.url, ExpiresAt: p.expiresAt}, nil
		}
		p.stop()
		o.pending = nil
	}

	record, err := loadTokenRecord(o.cfg.TokenDir, o.cfg.Server, o.cfg.ServerURL)
	if err != nil {
		return Authorization{}, err
	}
	endpoints, err := o.discover(ctx, challenge)
	if err != nil {
		return Authorization{}, err
	}
	if len(endpoints.CodeChallengeMethods) > 0 && !hasS256(endpoints.CodeChallengeMethods) {
		return Authorization{}, fmt.Errorf("mcp %s: the authorization server does not support PKCE S256", o.cfg.Server)
	}

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return Authorization{}, fmt.Errorf("mcp %s: open loopback listener: %w", o.cfg.Server, err)
	}
	redirectURI := "http://" + listener.Addr().String() + callbackPath

	clientID, clientSecret := o.cfg.ClientID, o.cfg.ClientSecret
	if clientID == "" && record != nil {
		clientID, clientSecret = record.ClientID, record.ClientSecret
	}
	if clientID == "" {
		clientID, clientSecret, err = o.register(ctx, endpoints, redirectURI)
		if err != nil {
			_ = listener.Close()
			return Authorization{}, err
		}
	}

	verifier, err := randomString()
	if err != nil {
		_ = listener.Close()
		return Authorization{}, err
	}
	state, err := randomString()
	if err != nil {
		_ = listener.Close()
		return Authorization{}, err
	}
	challengeParam := pkceChallenge(verifier)

	scope := o.cfg.Scope
	if scope == "" && record != nil {
		scope = record.Scope
	}

	expiresAt := o.now().Add(flowLifetime)
	authURL, err := buildAuthorizationURL(endpoints.AuthorizationEndpoint, url.Values{
		"response_type":         {"code"},
		"client_id":             {clientID},
		"redirect_uri":          {redirectURI},
		"state":                 {state},
		"code_challenge":        {challengeParam},
		"code_challenge_method": {"S256"},
		"resource":              {o.cfg.ServerURL},
		"scope":                 {scope},
	})
	if err != nil {
		_ = listener.Close()
		return Authorization{}, err
	}

	p := &pendingFlow{
		url:          authURL,
		expiresAt:    expiresAt,
		verifier:     verifier,
		state:        state,
		redirectURI:  redirectURI,
		clientID:     clientID,
		clientSecret: clientSecret,
		endpoints:    endpoints,
		listener:     listener,
		result:       make(chan flowResult, 1),
		done:         make(chan struct{}),
	}
	mux := http.NewServeMux()
	mux.HandleFunc(callbackPath, func(w http.ResponseWriter, r *http.Request) {
		o.handleCallback(p, w, r)
	})
	p.server = &http.Server{Handler: mux, ReadHeaderTimeout: 10 * time.Second}
	go func() { _ = p.server.Serve(listener) }()

	o.pending = p
	return Authorization{URL: authURL, ExpiresAt: expiresAt}, nil
}

// Wait blocks until the callback arrives, the flow times out, or ctx is
// canceled, and on success exchanges the code and stores the tokens.
func (o *OAuth) Wait(ctx context.Context) error {
	o.mu.Lock()
	p := o.pending
	o.mu.Unlock()
	if p == nil {
		return fmt.Errorf("mcp %s: no sign-in in progress", o.cfg.Server)
	}
	timer := time.NewTimer(time.Until(p.expiresAt))
	defer timer.Stop()
	select {
	case res := <-p.result:
		o.finish(p)
		if res.err != nil {
			return res.err
		}
		return o.complete(ctx, p, res.code)
	case <-p.done:
		o.finish(p)
		return fmt.Errorf("mcp %s: sign-in canceled", o.cfg.Server)
	case <-ctx.Done():
		o.finish(p)
		return ctx.Err()
	case <-timer.C:
		o.finish(p)
		return fmt.Errorf("mcp %s: sign-in timed out", o.cfg.Server)
	}
}

// Cancel closes a pending flow without completing it. Wait returns promptly.
func (o *OAuth) Cancel() {
	o.mu.Lock()
	p := o.pending
	o.pending = nil
	o.mu.Unlock()
	if p != nil {
		p.stop()
	}
}

// SignOut deletes the token file. The client registration is not stored
// anywhere else, so the next sign-in registers again.
func (o *OAuth) SignOut() error {
	o.Cancel()
	return deleteTokenRecord(o.cfg.TokenDir, o.cfg.Server, o.cfg.ServerURL)
}

func (o *OAuth) finish(p *pendingFlow) {
	o.mu.Lock()
	if o.pending == p {
		o.pending = nil
	}
	o.mu.Unlock()
	p.stop()
}

func (o *OAuth) handleCallback(p *pendingFlow, w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	// The state check is what makes a stranger's callback useless.
	if subtle.ConstantTimeCompare([]byte(q.Get("state")), []byte(p.state)) != 1 {
		http.Error(w, "state mismatch", http.StatusBadRequest)
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	if e := q.Get("error"); e != "" {
		desc := truncate(q.Get("error_description"), 200)
		message := "The authorization server said " + e + "."
		if desc != "" {
			message += " " + desc
		}
		select {
		case p.result <- flowResult{err: fmt.Errorf("mcp %s: %s", o.cfg.Server, message)}:
		default:
		}
		fmt.Fprint(w, "Sign-in failed. You can close this window.")
		return
	}
	code := q.Get("code")
	if code == "" {
		http.Error(w, "missing code", http.StatusBadRequest)
		return
	}
	select {
	case p.result <- flowResult{code: code}:
	default:
	}
	fmt.Fprint(w, "Signed in. You can close this window.")
}

func (o *OAuth) complete(ctx context.Context, p *pendingFlow, code string) error {
	form := url.Values{
		"grant_type":    {"authorization_code"},
		"code":          {code},
		"redirect_uri":  {p.redirectURI},
		"client_id":     {p.clientID},
		"code_verifier": {p.verifier},
		"resource":      {o.cfg.ServerURL},
	}
	if p.clientSecret != "" {
		form.Set("client_secret", p.clientSecret)
	}
	tok, err := o.tokenRequest(ctx, p.endpoints.TokenEndpoint, form)
	if err != nil {
		return err
	}
	record := &TokenRecord{
		Server:         o.cfg.Server,
		URL:            o.cfg.ServerURL,
		Issuer:         p.endpoints.Issuer,
		Resource:       o.cfg.ServerURL,
		ClientID:       p.clientID,
		ClientSecret:   p.clientSecret,
		Scope:          tok.Scope,
		TokenEndpoint:  p.endpoints.TokenEndpoint,
		RegisteredWith: p.redirectURI,
		TokenType:      tok.TokenType,
		AccessToken:    tok.AccessToken,
		RefreshToken:   tok.RefreshToken,
		ExpiresAt:      o.expiry(tok),
	}
	if record.Scope == "" {
		record.Scope = o.cfg.Scope
	}
	return saveTokenRecord(o.cfg.TokenDir, record)
}

func (o *OAuth) refresh(ctx context.Context, rec *TokenRecord) (*TokenRecord, error) {
	endpoint := rec.TokenEndpoint
	if endpoint == "" {
		endpoints, err := o.discover(ctx, "")
		if err != nil {
			return nil, err
		}
		endpoint = endpoints.TokenEndpoint
		rec.TokenEndpoint = endpoint
	}
	form := url.Values{
		"grant_type":    {"refresh_token"},
		"refresh_token": {rec.RefreshToken},
		"client_id":     {rec.ClientID},
		"resource":      {normalizeURL(rec.Resource)},
	}
	if rec.ClientSecret != "" {
		form.Set("client_secret", rec.ClientSecret)
	}
	tok, err := o.tokenRequest(ctx, endpoint, form)
	if err != nil {
		return nil, err
	}
	rec.AccessToken = tok.AccessToken
	rec.TokenType = tok.TokenType
	if tok.RefreshToken != "" {
		rec.RefreshToken = tok.RefreshToken
	}
	rec.ExpiresAt = o.expiry(tok)
	if tok.Scope != "" {
		rec.Scope = tok.Scope
	}
	if err := saveTokenRecord(o.cfg.TokenDir, rec); err != nil {
		return nil, err
	}
	return rec, nil
}

func (o *OAuth) expiry(tok *tokenResponse) time.Time {
	if tok.ExpiresIn <= 0 {
		return time.Time{}
	}
	return o.now().Add(time.Duration(tok.ExpiresIn) * time.Second)
}

// discover follows the chain the design describes: resource metadata (from the
// challenge when it names one, otherwise the RFC 9728 well-known path), then
// the authorization server's metadata. A resource value that names a different
// server is refused rather than followed.
func (o *OAuth) discover(ctx context.Context, challenge string) (asEndpoints, error) {
	metadataURL := ""
	if _, named := parseWWWAuthenticate(challenge); named != "" {
		metadataURL = named
	}
	if metadataURL == "" {
		metadataURL = resourceMetadataURL(o.cfg.ServerURL)
	}
	var resource struct {
		Resource             string   `json:"resource"`
		AuthorizationServers []string `json:"authorization_servers"`
	}
	if err := o.getJSON(ctx, metadataURL, &resource); err != nil {
		return asEndpoints{}, fmt.Errorf("mcp %s: protected resource metadata: %w", o.cfg.Server, err)
	}
	if resource.Resource != "" && normalizeURL(resource.Resource) != o.cfg.ServerURL {
		return asEndpoints{}, fmt.Errorf("mcp %s: the resource metadata describes %s, not the configured server",
			o.cfg.Server, redactURL(resource.Resource))
	}
	if len(resource.AuthorizationServers) == 0 || strings.TrimSpace(resource.AuthorizationServers[0]) == "" {
		return asEndpoints{}, fmt.Errorf("mcp %s: the protected resource metadata names no authorization server", o.cfg.Server)
	}
	issuer := strings.TrimRight(resource.AuthorizationServers[0], "/")

	var lastErr error
	for _, metadata := range authorizationServerMetadataURLs(issuer) {
		var doc struct {
			Issuer                string   `json:"issuer"`
			AuthorizationEndpoint string   `json:"authorization_endpoint"`
			TokenEndpoint         string   `json:"token_endpoint"`
			RegistrationEndpoint  string   `json:"registration_endpoint"`
			CodeChallengeMethods  []string `json:"code_challenge_methods_supported"`
			ScopesSupported       []string `json:"scopes_supported"`
		}
		if err := o.getJSON(ctx, metadata, &doc); err != nil {
			lastErr = err
			continue
		}
		if doc.AuthorizationEndpoint == "" || doc.TokenEndpoint == "" {
			lastErr = fmt.Errorf("mcp %s: %s lacks an authorization or token endpoint", o.cfg.Server, redactURL(metadata))
			continue
		}
		return asEndpoints{
			Issuer:                issuer,
			AuthorizationEndpoint: doc.AuthorizationEndpoint,
			TokenEndpoint:         doc.TokenEndpoint,
			RegistrationEndpoint:  doc.RegistrationEndpoint,
			CodeChallengeMethods:  doc.CodeChallengeMethods,
			ScopesSupported:       doc.ScopesSupported,
		}, nil
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("mcp %s: no authorization server metadata", o.cfg.Server)
	}
	return asEndpoints{}, lastErr
}

func (o *OAuth) register(ctx context.Context, endpoints asEndpoints, redirectURI string) (string, string, error) {
	if endpoints.RegistrationEndpoint == "" {
		return "", "", fmt.Errorf("mcp %s: the authorization server has no dynamic registration; set oauth.client_id in mcp.json", o.cfg.Server)
	}
	body, err := json.Marshal(map[string]any{
		"client_name":                "Anvil",
		"redirect_uris":              []string{redirectURI},
		"grant_types":                []string{"authorization_code", "refresh_token"},
		"response_types":             []string{"code"},
		"token_endpoint_auth_method": "none",
	})
	if err != nil {
		return "", "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoints.RegistrationEndpoint, strings.NewReader(string(body)))
	if err != nil {
		return "", "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	resp, err := o.http.Do(req)
	if err != nil {
		return "", "", fmt.Errorf("mcp %s: register client: %w", o.cfg.Server, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", "", fmt.Errorf("mcp %s: register client: status %d", o.cfg.Server, resp.StatusCode)
	}
	var out struct {
		ClientID     string `json:"client_id"`
		ClientSecret string `json:"client_secret"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&out); err != nil {
		return "", "", fmt.Errorf("mcp %s: register client: %w", o.cfg.Server, err)
	}
	if out.ClientID == "" {
		return "", "", fmt.Errorf("mcp %s: the registration answer had no client_id", o.cfg.Server)
	}
	return out.ClientID, out.ClientSecret, nil
}

type tokenResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	TokenType    string `json:"token_type"`
	Scope        string `json:"scope"`
	ExpiresIn    int64  `json:"expires_in"`
}

// oauthResponseError carries what the authorization server said, and nothing
// else: no request parameters, no codes, no tokens.
type oauthResponseError struct {
	code        string
	description string
}

func (e *oauthResponseError) Error() string {
	if e.description != "" {
		return fmt.Sprintf("mcp oauth: %s: %s", e.code, truncate(e.description, 200))
	}
	return fmt.Sprintf("mcp oauth: %s", e.code)
}

func (o *OAuth) tokenRequest(ctx context.Context, endpoint string, form url.Values) (*tokenResponse, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	resp, err := o.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("mcp %s: token request: %w", o.cfg.Server, err)
	}
	defer resp.Body.Close()
	var out struct {
		tokenResponse
		Error            string `json:"error"`
		ErrorDescription string `json:"error_description"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&out); err != nil {
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			return nil, fmt.Errorf("mcp %s: token request: status %d", o.cfg.Server, resp.StatusCode)
		}
		return nil, fmt.Errorf("mcp %s: token request: unreadable answer", o.cfg.Server)
	}
	if out.Error != "" {
		return nil, &oauthResponseError{code: out.Error, description: out.ErrorDescription}
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("mcp %s: token request: status %d", o.cfg.Server, resp.StatusCode)
	}
	if out.AccessToken == "" {
		return nil, fmt.Errorf("mcp %s: the token answer had no access_token", o.cfg.Server)
	}
	return &out.tokenResponse, nil
}

func (o *OAuth) getJSON(ctx context.Context, rawURL string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	resp, err := o.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("status %d from %s", resp.StatusCode, redactURL(rawURL))
	}
	return json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(out)
}

func hasS256(methods []string) bool {
	for _, m := range methods {
		if strings.EqualFold(m, "S256") {
			return true
		}
	}
	return false
}

func randomString() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("generate oauth randomness: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

func pkceChallenge(verifier string) string {
	sum := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

// buildAuthorizationURL adds the parameters to the endpoint's own query, so an
// authorization endpoint that already carries something keeps it.
func buildAuthorizationURL(endpoint string, params url.Values) (string, error) {
	u, err := url.Parse(endpoint)
	if err != nil {
		return "", fmt.Errorf("parse authorization endpoint: %w", err)
	}
	q := u.Query()
	for k, values := range params {
		if len(values) == 0 || values[0] == "" {
			continue
		}
		q.Set(k, values[0])
	}
	u.RawQuery = q.Encode()
	return u.String(), nil
}
