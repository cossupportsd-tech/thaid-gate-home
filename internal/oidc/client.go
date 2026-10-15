package oidc

import (
	"context"
	"crypto/ecdsa"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"
)

// Config holds the values that ThaID (DOPA) gives you when you register a
// relying party. For local testing, point Issuer at the mock ThaID.
type Config struct {
	Issuer       string // e.g. https://imauthsbx.bora.dopa.go.th
	ClientID     string
	ClientSecret string
	RedirectURL  string   // must match the redirect URI registered with ThaID
	Scopes       []string // e.g. openid pid name
	AuthMethod   string   // "client_secret_basic" (default) or "client_secret_post"
	UsePKCE      bool
	// AllowMissingNonce accepts an ID token without a nonce claim. A nonce
	// that is present must still match. Leave false unless the provider is
	// known not to echo the nonce.
	AllowMissingNonce bool
	HTTPClient        *http.Client
	Now               func() time.Time
}

// Metadata is the subset of the discovery document the client needs.
type Metadata struct {
	Issuer                string `json:"issuer"`
	AuthorizationEndpoint string `json:"authorization_endpoint"`
	TokenEndpoint         string `json:"token_endpoint"`
	JWKSURI               string `json:"jwks_uri"`
	UserInfoEndpoint      string `json:"userinfo_endpoint"`
}

// Client performs the ThaID login flow for one registered relying party.
type Client struct {
	cfg  Config
	meta Metadata

	mu          sync.Mutex
	keys        map[string]*ecdsa.PublicKey
	keysFetched time.Time
}

// LoginState is what the website must remember between sending the user to
// ThaID and receiving the callback. Keep it server-side, never in the URL.
type LoginState struct {
	State    string
	Nonce    string
	Verifier string
}

// Claims are the verified ID token claims. ThaID returns personal data such as
// pid and name only for the scopes your registration was approved for.
type Claims struct {
	Issuer     string   `json:"iss"`
	Subject    string   `json:"sub"`
	Audience   audience `json:"aud"`
	Expiry     int64    `json:"exp"`
	IssuedAt   int64    `json:"iat"`
	Nonce      string   `json:"nonce"`
	PID        string   `json:"pid"`
	Name       string   `json:"name"`
	Title      string   `json:"title"`
	GivenName  string   `json:"given_name"`
	FamilyName string   `json:"family_name"`
	NameEN     string   `json:"name_en"`
	IAL        any      `json:"ial"`

	// Keys lists the claim names in the token, useful to see what the
	// provider actually returned without logging the values.
	Keys []string `json:"-"`
	// Raw holds every claim in the ID token.
	Raw map[string]any `json:"-"`
}

// Tokens is the result of a successful code exchange.
type Tokens struct {
	Claims      *Claims
	AccessToken string
	// Response is the token endpoint's JSON without the token strings
	// themselves. ThaID may return personal data here as well.
	Response map[string]any
}

// DisplayName is the full name, built from title, given and family name when
// the provider does not send the name claim.
func (c *Claims) DisplayName() string {
	if c.Name != "" {
		return c.Name
	}
	return strings.TrimSpace(c.Title + c.GivenName + " " + c.FamilyName)
}

type audience []string

func (a *audience) UnmarshalJSON(b []byte) error {
	var one string
	if err := json.Unmarshal(b, &one); err == nil {
		*a = audience{one}
		return nil
	}
	var many []string
	if err := json.Unmarshal(b, &many); err != nil {
		return errors.New("aud must be a string or an array of strings")
	}
	*a = many
	return nil
}

const clockSkew = 60 * time.Second

// Discover reads the provider's discovery document and returns a ready client.
func Discover(ctx context.Context, cfg Config) (*Client, error) {
	if cfg.Issuer == "" || cfg.ClientID == "" || cfg.RedirectURL == "" {
		return nil, errors.New("issuer, client ID and redirect URL are required")
	}
	if cfg.HTTPClient == nil {
		cfg.HTTPClient = &http.Client{Timeout: 15 * time.Second}
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	if cfg.AuthMethod == "" {
		cfg.AuthMethod = "client_secret_basic"
	}
	c := &Client{cfg: cfg, keys: map[string]*ecdsa.PublicKey{}}
	wellKnown := strings.TrimSuffix(cfg.Issuer, "/") + "/.well-known/openid-configuration"
	if err := c.getJSON(ctx, wellKnown, &c.meta); err != nil {
		return nil, fmt.Errorf("discovery: %w", err)
	}
	if c.meta.Issuer != cfg.Issuer {
		return nil, fmt.Errorf("discovery issuer %q does not match configured issuer %q", c.meta.Issuer, cfg.Issuer)
	}
	if c.meta.AuthorizationEndpoint == "" || c.meta.TokenEndpoint == "" || c.meta.JWKSURI == "" {
		return nil, errors.New("discovery document is missing endpoints")
	}
	return c, nil
}

// AuthURL starts a login: it returns the ThaID URL to redirect the browser to
// and the state that must be stored for the callback.
func (c *Client) AuthURL() (string, LoginState) {
	st := LoginState{State: RandomString(32), Nonce: RandomString(32)}
	q := url.Values{
		"response_type": {"code"},
		"client_id":     {c.cfg.ClientID},
		"redirect_uri":  {c.cfg.RedirectURL},
		"scope":         {strings.Join(c.cfg.Scopes, " ")},
		"state":         {st.State},
		"nonce":         {st.Nonce},
	}
	if c.cfg.UsePKCE {
		st.Verifier = RandomString(32)
		q.Set("code_challenge", PKCEChallenge(st.Verifier))
		q.Set("code_challenge_method", "S256")
	}
	sep := "?"
	if strings.Contains(c.meta.AuthorizationEndpoint, "?") {
		sep = "&"
	}
	return c.meta.AuthorizationEndpoint + sep + q.Encode(), st
}

// ProviderOrigin returns the scheme://host of the authorization endpoint (for
// example https://imauthsbx.bora.dopa.go.th). The website uses it to allow the
// provider's QR page to be shown inside an <iframe> via CSP frame-src.
func (c *Client) ProviderOrigin() string {
	u, err := url.Parse(c.meta.AuthorizationEndpoint)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return ""
	}
	return u.Scheme + "://" + u.Host
}

// Exchange trades the authorization code for tokens and returns the verified
// ID token claims.
func (c *Client) Exchange(ctx context.Context, code string, st LoginState) (*Claims, error) {
	t, err := c.ExchangeTokens(ctx, code, st)
	if err != nil {
		return nil, err
	}
	return t.Claims, nil
}

// ExchangeTokens is Exchange that also returns the access token and the rest
// of the token response.
func (c *Client) ExchangeTokens(ctx context.Context, code string, st LoginState) (*Tokens, error) {
	form := url.Values{
		"grant_type":   {"authorization_code"},
		"code":         {code},
		"redirect_uri": {c.cfg.RedirectURL},
	}
	if st.Verifier != "" {
		form.Set("code_verifier", st.Verifier)
	}
	if c.cfg.AuthMethod == "client_secret_post" {
		form.Set("client_id", c.cfg.ClientID)
		form.Set("client_secret", c.cfg.ClientSecret)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.meta.TokenEndpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	if c.cfg.AuthMethod != "client_secret_post" {
		req.SetBasicAuth(url.QueryEscape(c.cfg.ClientID), url.QueryEscape(c.cfg.ClientSecret))
	}
	resp, err := c.cfg.HTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("token request: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, err
	}
	var tok struct {
		IDToken     string `json:"id_token"`
		AccessToken string `json:"access_token"`
		Error       string `json:"error"`
		ErrorDesc   string `json:"error_description"`
	}
	var all map[string]any
	if err := json.Unmarshal(body, &tok); err != nil || json.Unmarshal(body, &all) != nil {
		return nil, fmt.Errorf("token response is not JSON (HTTP %d)", resp.StatusCode)
	}
	if resp.StatusCode != http.StatusOK || tok.Error != "" {
		return nil, fmt.Errorf("token endpoint refused: %s %s (HTTP %d)", tok.Error, tok.ErrorDesc, resp.StatusCode)
	}
	if tok.IDToken == "" {
		return nil, errors.New("token response has no id_token; is the openid scope approved?")
	}
	cl, err := c.VerifyIDToken(ctx, tok.IDToken, st.Nonce)
	if err != nil {
		return nil, err
	}
	for _, k := range []string{"id_token", "access_token", "refresh_token"} {
		delete(all, k)
	}
	return &Tokens{Claims: cl, AccessToken: tok.AccessToken, Response: all}, nil
}

// UserInfo calls the provider's userinfo endpoint with the access token.
func (c *Client) UserInfo(ctx context.Context, accessToken string) (map[string]any, error) {
	if c.meta.UserInfoEndpoint == "" {
		return nil, errors.New("provider has no userinfo endpoint")
	}
	if accessToken == "" {
		return nil, errors.New("no access token")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.meta.UserInfoEndpoint, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("Accept", "application/json")
	resp, err := c.cfg.HTTPClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("userinfo: HTTP %d %s", resp.StatusCode, strings.TrimSpace(string(body[:min(len(body), 200)])))
	}
	var info map[string]any
	if err := json.Unmarshal(body, &info); err != nil {
		return nil, errors.New("userinfo response is not JSON")
	}
	return info, nil
}

// VerifyIDToken checks the signature and the standard claims of an ID token.
func (c *Client) VerifyIDToken(ctx context.Context, raw, nonce string) (*Claims, error) {
	hdr, payload, signed, sig, err := parseES256(raw)
	if err != nil {
		return nil, err
	}
	key, err := c.key(ctx, hdr.Kid)
	if err != nil {
		return nil, err
	}
	if !verifyES256(key, signed, sig) {
		return nil, errors.New("ID token signature is invalid")
	}
	var cl Claims
	if json.Unmarshal(payload, &cl) != nil || json.Unmarshal(payload, &cl.Raw) != nil {
		return nil, errors.New("ID token claims are not valid JSON")
	}
	for k := range cl.Raw {
		cl.Keys = append(cl.Keys, k)
	}
	sort.Strings(cl.Keys)
	now := c.cfg.Now()
	switch {
	case cl.Issuer != c.meta.Issuer:
		return nil, fmt.Errorf("ID token issuer %q is not trusted", cl.Issuer)
	case !contains(cl.Audience, c.cfg.ClientID):
		return nil, errors.New("ID token was issued for another client")
	case cl.Expiry == 0 || now.After(time.Unix(cl.Expiry, 0).Add(clockSkew)):
		return nil, errors.New("ID token has expired")
	case cl.IssuedAt != 0 && time.Unix(cl.IssuedAt, 0).After(now.Add(clockSkew)):
		return nil, errors.New("ID token is issued in the future")
	case nonce != "" && cl.Nonce == "" && !c.cfg.AllowMissingNonce:
		return nil, errors.New("ID token has no nonce (set AllowMissingNonce if the provider does not send one)")
	case nonce != "" && cl.Nonce != "" && cl.Nonce != nonce:
		return nil, errors.New("ID token nonce does not match this login")
	case cl.Subject == "":
		return nil, errors.New("ID token has no subject")
	}
	return &cl, nil
}

// key returns the signing key for kid, refetching the JWKS at most once a
// minute when an unknown key id appears (ThaID rotates keys).
func (c *Client) key(ctx context.Context, kid string) (*ecdsa.PublicKey, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if k, ok := c.keys[kid]; ok {
		return k, nil
	}
	if time.Since(c.keysFetched) < time.Minute && len(c.keys) > 0 {
		return nil, fmt.Errorf("unknown signing key %q", kid)
	}
	var set JWKS
	if err := c.getJSON(ctx, c.meta.JWKSURI, &set); err != nil {
		return nil, fmt.Errorf("fetch JWKS: %w", err)
	}
	keys := map[string]*ecdsa.PublicKey{}
	for _, j := range set.Keys {
		if j.Use != "" && j.Use != "sig" {
			continue
		}
		if pub, err := j.PublicKey(); err == nil {
			keys[j.Kid] = pub
		}
	}
	c.keys, c.keysFetched = keys, time.Now()
	if k, ok := keys[kid]; ok {
		return k, nil
	}
	return nil, fmt.Errorf("unknown signing key %q", kid)
}

func (c *Client) getJSON(ctx context.Context, u string, v any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	resp, err := c.cfg.HTTPClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("GET %s: HTTP %d", u, resp.StatusCode)
	}
	return json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(v)
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}
