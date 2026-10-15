package oidc

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

type fakeProvider struct {
	srv *httptest.Server
	key *ecdsa.PrivateKey
}

func newFakeProvider(t *testing.T) *fakeProvider {
	t.Helper()
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	fp := &fakeProvider{key: key}
	mux := http.NewServeMux()
	fp.srv = httptest.NewServer(mux)
	t.Cleanup(fp.srv.Close)
	iss := fp.srv.URL
	mux.HandleFunc("/.well-known/openid-configuration", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(Metadata{Issuer: iss, AuthorizationEndpoint: iss + "/auth", TokenEndpoint: iss + "/token", JWKSURI: iss + "/jwks"})
	})
	mux.HandleFunc("/jwks", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(JWKS{Keys: []JWK{NewJWK("k1", &key.PublicKey)}})
	})
	return fp
}

func (fp *fakeProvider) client(t *testing.T) *Client {
	t.Helper()
	c, err := Discover(context.Background(), Config{Issuer: fp.srv.URL, ClientID: "rp", ClientSecret: "s", RedirectURL: "http://rp/cb", Scopes: []string{"openid"}, UsePKCE: true})
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func (fp *fakeProvider) claims() map[string]any {
	now := time.Now()
	return map[string]any{"iss": fp.srv.URL, "sub": "user-1", "aud": "rp", "iat": now.Unix(), "exp": now.Add(5 * time.Minute).Unix(), "nonce": "n1", "pid": "0000000000019"}
}

func TestVerifyIDToken(t *testing.T) {
	fp := newFakeProvider(t)
	c := fp.client(t)
	otherKey, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)

	sign := func(key *ecdsa.PrivateKey, kid string, mod func(map[string]any)) string {
		cl := fp.claims()
		if mod != nil {
			mod(cl)
		}
		tok, err := SignES256(key, kid, cl)
		if err != nil {
			t.Fatal(err)
		}
		return tok
	}

	good := sign(fp.key, "k1", nil)
	cl, err := c.VerifyIDToken(context.Background(), good, "n1")
	if err != nil {
		t.Fatalf("valid token rejected: %v", err)
	}
	if cl.PID != "0000000000019" || cl.Subject != "user-1" {
		t.Fatalf("unexpected claims %+v", cl)
	}

	// Header {"alg":"none"} and {"alg":"HS256"} with the good payload.
	parts := strings.Split(good, ".")
	none := "eyJhbGciOiJub25lIn0." + parts[1] + "."
	hs := "eyJhbGciOiJIUzI1NiJ9." + parts[1] + "." + parts[2]

	cases := map[string]struct {
		token, nonce, want string
	}{
		"signed by another key": {sign(otherKey, "k1", nil), "n1", "signature"},
		"unknown key id":        {sign(fp.key, "k9", nil), "n1", "unknown signing key"},
		"alg none":              {none, "n1", "alg"},
		"alg HS256":             {hs, "n1", "alg"},
		"tampered payload":      {parts[0] + "." + b64.EncodeToString([]byte(`{"iss":"x"}`)) + "." + parts[2], "n1", "signature"},
		"wrong audience":        {sign(fp.key, "k1", func(m map[string]any) { m["aud"] = "other" }), "n1", "another client"},
		"audience array ok":     {sign(fp.key, "k1", func(m map[string]any) { m["aud"] = []string{"x", "rp"} }), "n1", ""},
		"wrong issuer":          {sign(fp.key, "k1", func(m map[string]any) { m["iss"] = "https://evil" }), "n1", "issuer"},
		"expired":               {sign(fp.key, "k1", func(m map[string]any) { m["exp"] = time.Now().Add(-5 * time.Minute).Unix() }), "n1", "expired"},
		"nonce mismatch":        {good, "other", "nonce"},
		"missing nonce":         {sign(fp.key, "k1", func(m map[string]any) { delete(m, "nonce") }), "n1", "no nonce"},
		"no subject":            {sign(fp.key, "k1", func(m map[string]any) { delete(m, "sub") }), "n1", "subject"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := c.VerifyIDToken(context.Background(), tc.token, tc.nonce)
			if tc.want == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("want error containing %q, got %v", tc.want, err)
			}
		})
	}
}

func TestAuthURL(t *testing.T) {
	c := newFakeProvider(t).client(t)
	u, st := c.AuthURL()
	for _, want := range []string{"response_type=code", "client_id=rp", "state=" + st.State, "nonce=" + st.Nonce, "code_challenge=" + PKCEChallenge(st.Verifier), "code_challenge_method=S256"} {
		if !strings.Contains(u, want) {
			t.Errorf("auth URL %s missing %s", u, want)
		}
	}
}

func TestDiscoverRejectsIssuerMismatch(t *testing.T) {
	fp := newFakeProvider(t)
	_, err := Discover(context.Background(), Config{Issuer: fp.srv.URL + "/", ClientID: "rp", RedirectURL: "http://rp/cb"})
	if err == nil {
		t.Fatal("expected issuer mismatch error")
	}
}

func TestJWKRejectsPointOffCurve(t *testing.T) {
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	j := NewJWK("k", &key.PublicKey)
	j.Y = j.X
	if _, err := j.PublicKey(); err == nil {
		t.Fatal("expected off-curve point to be rejected")
	}
}

func TestAllowMissingNonce(t *testing.T) {
	fp := newFakeProvider(t)
	c, err := Discover(context.Background(), Config{Issuer: fp.srv.URL, ClientID: "rp", RedirectURL: "http://rp/cb", AllowMissingNonce: true})
	if err != nil {
		t.Fatal(err)
	}
	cl := fp.claims()
	delete(cl, "nonce")
	tok, _ := SignES256(fp.key, "k1", cl)
	if _, err := c.VerifyIDToken(context.Background(), tok, "n1"); err != nil {
		t.Fatalf("missing nonce should be allowed: %v", err)
	}
	cl["nonce"] = "wrong"
	tok, _ = SignES256(fp.key, "k1", cl)
	if _, err := c.VerifyIDToken(context.Background(), tok, "n1"); err == nil {
		t.Fatal("a nonce that is present must still match")
	}
}
