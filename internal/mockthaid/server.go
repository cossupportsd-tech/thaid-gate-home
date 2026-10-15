// Package mockthaid is a local stand-in for the ThaID (DOPA) OpenID Connect
// provider. It serves the same endpoint paths as imauth.bora.dopa.go.th, signs
// ID tokens with ES256 and shows a QR code that a phone can scan, so the whole
// "scan ThaID before entering the website" flow can be tested without real
// citizen data. It is for development only.
package mockthaid

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"sync"
	"time"

	"thaidgate/internal/oidc"
)

// TestPIN is the PIN the mock "ThaID app" accepts.
const TestPIN = "123456"

const (
	requestTTL = 5 * time.Minute
	codeTTL    = 60 * time.Second
	tokenTTL   = 10 * time.Minute
	maxPINTry  = 5
)

// Client is a registered relying party.
type Client struct {
	Name         string
	Secret       string
	RedirectURIs []string
}

// Persona is a fake person that can approve a login. The PIDs start with 0,
// which is never issued to a real Thai citizen, and have a valid checksum.
type Persona struct {
	Sub, PID, Title, GivenName, FamilyName, NameEN, Gender string
}

func (p Persona) Name() string { return p.Title + p.GivenName + " " + p.FamilyName }

// Personas are the test identities offered in the mock app.
var Personas = []Persona{
	{Sub: "mock-sub-7f3a1c", PID: "0000000000019", Title: "นาย", GivenName: "สมชาย", FamilyName: "ทดสอบ", NameEN: "Mr. Somchai Test", Gender: "male"},
	{Sub: "mock-sub-2b9e44", PID: "0000000000027", Title: "นางสาว", GivenName: "สมหญิง", FamilyName: "ตัวอย่าง", NameEN: "Ms. Somying Example", Gender: "female"},
	{Sub: "mock-sub-c41d08", PID: "0000000000035", Title: "นาย", GivenName: "บุคคล", FamilyName: "ภายนอก", NameEN: "Mr. Outsider Demo", Gender: "male"},
}

// Config configures the mock provider.
type Config struct {
	Issuer    string // base URL the website uses, e.g. http://localhost:9000
	PublicURL string // base URL encoded in the QR code; must be reachable from the phone
	Clients   map[string]Client
}

type authReq struct {
	id, clientID, redirectURI, scope, state, nonce, challenge string
	created                                                   time.Time
	status                                                    string // pending, approved, denied
	persona                                                   *Persona
	code                                                      string
	codeExpires                                               time.Time
	codeUsed                                                  bool
	pinTries                                                  int
}

// Server is the mock provider. It implements http.Handler.
type Server struct {
	cfg Config
	key *ecdsa.PrivateKey
	kid string
	mux *http.ServeMux

	mu     sync.Mutex
	reqs   map[string]*authReq
	codes  map[string]*authReq
	access map[string]map[string]any // access token -> userinfo claims
}

// New creates a mock provider with a fresh ES256 signing key.
func New(cfg Config) (*Server, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}
	cfg.Issuer = strings.TrimSuffix(cfg.Issuer, "/")
	if cfg.PublicURL == "" {
		cfg.PublicURL = cfg.Issuer
	}
	cfg.PublicURL = strings.TrimSuffix(cfg.PublicURL, "/")
	s := &Server{
		cfg: cfg, key: key, kid: "mock-" + oidc.RandomString(6),
		mux: http.NewServeMux(), reqs: map[string]*authReq{}, codes: map[string]*authReq{}, access: map[string]map[string]any{},
	}
	s.mux.HandleFunc("GET /{$}", s.home)
	s.mux.HandleFunc("GET /.well-known/openid-configuration", s.discovery)
	s.mux.HandleFunc("GET /jwks/", s.jwks)
	s.mux.HandleFunc("GET /api/v2/oauth2/auth/", s.authorize)
	s.mux.HandleFunc("GET /api/v2/oauth2/auth/status", s.status)
	s.mux.HandleFunc("POST /api/v2/oauth2/token/", s.token)
	s.mux.HandleFunc("GET /api/v2/oauth2/userinfo/", s.userinfo)
	s.mux.HandleFunc("GET /app/scan", s.scanForm)
	s.mux.HandleFunc("POST /app/scan", s.scanSubmit)
	return s, nil
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Referrer-Policy", "no-referrer")
	s.mux.ServeHTTP(w, r)
}

func (s *Server) home(w http.ResponseWriter, r *http.Request) {
	render(w, http.StatusOK, homeTmpl, map[string]any{"Issuer": s.cfg.Issuer, "Personas": Personas, "PIN": TestPIN})
}

func (s *Server) discovery(w http.ResponseWriter, r *http.Request) {
	iss := s.cfg.Issuer
	writeJSON(w, http.StatusOK, map[string]any{
		"issuer":                                iss,
		"authorization_endpoint":                iss + "/api/v2/oauth2/auth/",
		"token_endpoint":                        iss + "/api/v2/oauth2/token/",
		"jwks_uri":                              iss + "/jwks/",
		"userinfo_endpoint":                     iss + "/api/v2/oauth2/userinfo/",
		"response_types_supported":              []string{"code"},
		"id_token_signing_alg_values_supported": []string{"ES256"},
		"scopes_supported":                      []string{"openid", "pid", "name", "given_name", "family_name", "name_en", "title", "gender", "ial"},
		"grant_types_supported":                 []string{"authorization_code"},
		"subject_types_supported":               []string{"public"},
		"token_endpoint_auth_methods_supported": []string{"client_secret_basic", "client_secret_post"},
		"code_challenge_methods_supported":      []string{"S256"},
	})
}

func (s *Server) jwks(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, oidc.JWKS{Keys: []oidc.JWK{oidc.NewJWK(s.kid, &s.key.PublicKey)}})
}

// authorize is the page the website redirects to. On a computer it shows a QR
// code for the phone to scan, then waits for the phone to approve.
func (s *Server) authorize(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	clientID, redirectURI := q.Get("client_id"), q.Get("redirect_uri")
	cl, ok := s.cfg.Clients[clientID]
	// Never redirect to an unregistered URI: show the error here instead.
	if !ok || !slices.Contains(cl.RedirectURIs, redirectURI) {
		render(w, http.StatusBadRequest, errorTmpl, "client_id หรือ redirect_uri ไม่ตรงกับที่ลงทะเบียนไว้")
		return
	}
	fail := func(code, desc string) {
		http.Redirect(w, r, withParams(redirectURI, url.Values{"error": {code}, "error_description": {desc}, "state": {q.Get("state")}}), http.StatusFound)
	}
	switch {
	case q.Get("response_type") != "code":
		fail("unsupported_response_type", "only code is supported")
		return
	case !slices.Contains(strings.Fields(q.Get("scope")), "openid"):
		fail("invalid_scope", "openid scope is required")
		return
	case q.Get("code_challenge") != "" && q.Get("code_challenge_method") != "S256":
		fail("invalid_request", "code_challenge_method must be S256")
		return
	}

	req := &authReq{
		id: oidc.RandomString(24), clientID: clientID, redirectURI: redirectURI,
		scope: q.Get("scope"), state: q.Get("state"), nonce: q.Get("nonce"),
		challenge: q.Get("code_challenge"), created: time.Now(), status: "pending",
	}
	s.mu.Lock()
	s.sweep()
	s.reqs[req.id] = req
	s.mu.Unlock()

	render(w, http.StatusOK, qrTmpl, map[string]any{
		"ID":      req.id,
		"Client":  cl.Name,
		"ScanURL": s.cfg.PublicURL + "/app/scan?req=" + url.QueryEscape(req.id),
		"Scopes":  strings.Fields(req.scope),
		"TTL":     int(requestTTL.Seconds()),
	})
}

// status is polled by the QR page until the phone approves or denies.
func (s *Server) status(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	req := s.reqs[r.URL.Query().Get("id")]
	if req == nil || (req.status == "pending" && time.Since(req.created) > requestTTL) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "expired"})
		return
	}
	switch req.status {
	case "approved":
		writeJSON(w, http.StatusOK, map[string]string{"status": "approved",
			"redirect": withParams(req.redirectURI, url.Values{"code": {req.code}, "state": {req.state}})})
	case "denied":
		writeJSON(w, http.StatusOK, map[string]string{"status": "denied",
			"redirect": withParams(req.redirectURI, url.Values{"error": {"access_denied"}, "error_description": {"user denied the request"}, "state": {req.state}})})
	default:
		writeJSON(w, http.StatusOK, map[string]string{"status": "pending"})
	}
}

// scanForm is what the phone opens after scanning the QR code. It plays the
// role of the ThaID mobile app.
func (s *Server) scanForm(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	req := s.reqs[r.URL.Query().Get("req")]
	s.mu.Unlock()
	if req == nil || req.status != "pending" || time.Since(req.created) > requestTTL {
		render(w, http.StatusGone, errorTmpl, "QR Code นี้หมดอายุหรือถูกใช้ไปแล้ว กรุณาเริ่มเข้าสู่ระบบใหม่จากหน้าเว็บ")
		return
	}
	render(w, http.StatusOK, scanTmpl, map[string]any{
		"ID": req.id, "Client": s.cfg.Clients[req.clientID].Name,
		"Scopes": strings.Fields(req.scope), "Personas": Personas, "PIN": TestPIN, "Selected": "",
	})
}

func (s *Server) scanSubmit(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		render(w, http.StatusBadRequest, errorTmpl, "คำขอไม่ถูกต้อง")
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	req := s.reqs[r.PostForm.Get("req")]
	if req == nil || req.status != "pending" || time.Since(req.created) > requestTTL {
		render(w, http.StatusGone, errorTmpl, "คำขอนี้หมดอายุหรือถูกใช้ไปแล้ว")
		return
	}
	if r.PostForm.Get("action") == "deny" {
		req.status = "denied"
		render(w, http.StatusOK, doneTmpl, map[string]any{"Approved": false})
		return
	}
	idx := slices.IndexFunc(Personas, func(p Persona) bool { return p.PID == r.PostForm.Get("persona") })
	if idx < 0 {
		render(w, http.StatusBadRequest, errorTmpl, "กรุณาเลือกบุคคลทดสอบ")
		return
	}
	if subtle.ConstantTimeCompare([]byte(r.PostForm.Get("pin")), []byte(TestPIN)) != 1 {
		req.pinTries++
		if req.pinTries >= maxPINTry {
			req.status = "denied"
			render(w, http.StatusForbidden, errorTmpl, "กรอก PIN ผิดเกินจำนวนครั้งที่กำหนด คำขอถูกยกเลิก")
			return
		}
		render(w, http.StatusUnauthorized, scanTmpl, map[string]any{
			"ID": req.id, "Client": s.cfg.Clients[req.clientID].Name, "Scopes": strings.Fields(req.scope),
			"Personas": Personas, "PIN": TestPIN, "Error": "PIN ไม่ถูกต้อง", "Selected": Personas[idx].PID,
		})
		return
	}
	p := Personas[idx]
	req.status, req.persona = "approved", &p
	req.code, req.codeExpires = oidc.RandomString(32), time.Now().Add(codeTTL)
	s.codes[req.code] = req
	render(w, http.StatusOK, doneTmpl, map[string]any{"Approved": true, "Name": p.Name()})
}

// token exchanges an authorization code for an ES256-signed ID token.
func (s *Server) token(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		tokenError(w, http.StatusBadRequest, "invalid_request", "bad form")
		return
	}
	clientID, secret := clientCredentials(r)
	cl, ok := s.cfg.Clients[clientID]
	if !ok || subtle.ConstantTimeCompare([]byte(secret), []byte(cl.Secret)) != 1 {
		w.Header().Set("WWW-Authenticate", `Basic realm="mock-thaid"`)
		tokenError(w, http.StatusUnauthorized, "invalid_client", "client authentication failed")
		return
	}
	if r.PostForm.Get("grant_type") != "authorization_code" {
		tokenError(w, http.StatusBadRequest, "unsupported_grant_type", "")
		return
	}

	s.mu.Lock()
	req := s.codes[r.PostForm.Get("code")]
	var reason string
	switch {
	case req == nil:
		reason = "unknown code"
	case req.codeUsed:
		reason = "code already used"
	case time.Now().After(req.codeExpires):
		reason = "code expired"
	case req.clientID != clientID:
		reason = "code was issued to another client"
	case req.redirectURI != r.PostForm.Get("redirect_uri"):
		reason = "redirect_uri mismatch"
	case req.challenge != "" && oidc.PKCEChallenge(r.PostForm.Get("code_verifier")) != req.challenge:
		reason = "PKCE verification failed"
	}
	if req != nil {
		req.codeUsed = true // a code is good for one attempt only
	}
	s.mu.Unlock()
	if reason != "" {
		tokenError(w, http.StatusBadRequest, "invalid_grant", reason)
		return
	}

	now := time.Now()
	p := req.persona
	claims := map[string]any{
		"iss": s.cfg.Issuer, "sub": p.Sub, "aud": clientID,
		"iat": now.Unix(), "exp": now.Add(tokenTTL).Unix(), "auth_time": now.Unix(),
	}
	if req.nonce != "" {
		claims["nonce"] = req.nonce
	}
	for _, sc := range strings.Fields(req.scope) {
		switch sc {
		case "pid":
			claims["pid"] = p.PID
		case "name":
			claims["name"] = p.Name()
		case "given_name":
			claims["given_name"] = p.GivenName
		case "family_name":
			claims["family_name"] = p.FamilyName
		case "name_en":
			claims["name_en"] = p.NameEN
		case "title":
			claims["title"] = p.Title
		case "gender":
			claims["gender"] = p.Gender
		case "ial":
			claims["ial"] = 2.3
		}
	}
	idToken, err := oidc.SignES256(s.key, s.kid, claims)
	if err != nil {
		tokenError(w, http.StatusInternalServerError, "server_error", "")
		return
	}
	info := map[string]any{"sub": p.Sub}
	for k, v := range claims {
		switch k {
		case "iss", "aud", "iat", "exp", "auth_time", "nonce":
		default:
			info[k] = v
		}
	}
	accessToken := oidc.RandomString(32)
	s.mu.Lock()
	s.access[accessToken] = info
	s.mu.Unlock()
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, map[string]any{
		"access_token": accessToken, "token_type": "Bearer",
		"expires_in": int(tokenTTL.Seconds()), "scope": req.scope, "id_token": idToken,
	})
}

// userinfo returns the approved claims for a Bearer access token.
func (s *Server) userinfo(w http.ResponseWriter, r *http.Request) {
	tok, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	s.mu.Lock()
	info := s.access[tok]
	s.mu.Unlock()
	if !ok || info == nil {
		w.Header().Set("WWW-Authenticate", `Bearer error="invalid_token"`)
		tokenError(w, http.StatusUnauthorized, "invalid_token", "")
		return
	}
	writeJSON(w, http.StatusOK, info)
}

// sweep drops old requests. The caller holds s.mu.
func (s *Server) sweep() {
	for id, req := range s.reqs {
		if time.Since(req.created) > 2*requestTTL {
			delete(s.reqs, id)
			delete(s.codes, req.code)
		}
	}
}

func clientCredentials(r *http.Request) (id, secret string) {
	if h := r.Header.Get("Authorization"); strings.HasPrefix(h, "Basic ") {
		raw, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(h, "Basic "))
		if err == nil {
			if u, p, ok := strings.Cut(string(raw), ":"); ok {
				u, _ = url.QueryUnescape(u)
				p, _ = url.QueryUnescape(p)
				return u, p
			}
		}
		return "", ""
	}
	return r.PostForm.Get("client_id"), r.PostForm.Get("client_secret")
}

func withParams(base string, v url.Values) string {
	u, err := url.Parse(base)
	if err != nil {
		return base
	}
	q := u.Query()
	for k, vals := range v {
		if len(vals) > 0 && vals[0] != "" {
			q.Set(k, vals[0])
		}
	}
	u.RawQuery = q.Encode()
	return u.String()
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func tokenError(w http.ResponseWriter, code int, errCode, desc string) {
	writeJSON(w, code, map[string]string{"error": errCode, "error_description": desc})
}
