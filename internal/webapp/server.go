// Package webapp is a website whose pages can be opened only after the visitor
// proves who they are with ThaID.
package webapp

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"html/template"
	"log"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"sync"
	"time"

	"thaidgate/internal/oidc"
)

const (
	sessionCookie = "tg_session"
	loginCookie   = "tg_login"
	loginTTL      = 10 * time.Minute
)

// Config configures the website.
type Config struct {
	OIDC          *oidc.Client
	AppName       string
	AllowedPIDs   []string      // empty: any person verified by ThaID may enter
	SecureCookies bool          // true when served over HTTPS (always in production)
	SessionTTL    time.Duration // absolute session lifetime
	CallbackPath  string        // path of the registered redirect URI, default /auth/thaid/callback
	// CallbackOrigin is the scheme and host of the redirect URI, such as
	// http://localhost:8081. A login started on another host name (for
	// example 127.0.0.1) is moved there first, so the login cookie is sent
	// back when ThaID returns the browser.
	CallbackOrigin string

	// PasteCallback lets a tester paste the callback URL by hand. Use it only
	// while testing on a machine that is not the host named in the registered
	// redirect URI: ThaID then sends the browser to that host, and the tester
	// copies the address bar back into this site.
	PasteCallback bool

	// ScanTTL is how long the QR shown on the /scan page is expected to stay
	// valid. The page starts a new login a few seconds before this elapses so
	// the visitor never sees a dead, expired QR. Default 5 minutes.
	ScanTTL time.Duration

	// DumpClaims, when true, writes the full verified ID-token claims, the
	// token endpoint response and the userinfo response to the log as JSON on
	// each successful login. Useful for debugging what ThaID actually returns.
	// Off by default because it logs personal data.
	DumpClaims bool
}

type session struct {
	claims      *oidc.Claims
	tokenResp   map[string]any // token endpoint response, without token strings
	userinfo    map[string]any
	userinfoErr string
	created     time.Time
	expires     time.Time
	welcome     bool // show the "login succeeded" window once
}

type pendingLogin struct {
	st      oidc.LoginState
	created time.Time
}

// Server is the website. It implements http.Handler.
type Server struct {
	cfg Config
	mux *http.ServeMux
	csp string

	mu       sync.Mutex
	sessions map[string]*session
	logins   map[string]*pendingLogin
}

// New creates the website handler.
func New(cfg Config) *Server {
	if cfg.SessionTTL == 0 {
		cfg.SessionTTL = time.Hour
	}
	if cfg.ScanTTL == 0 {
		cfg.ScanTTL = 5 * time.Minute
	}
	if cfg.CallbackPath == "" {
		cfg.CallbackPath = "/auth/thaid/callback"
	}
	if cfg.AppName == "" {
		cfg.AppName = "ระบบสารสนเทศ"
	}
	s := &Server{cfg: cfg, mux: http.NewServeMux(), sessions: map[string]*session{}, logins: map[string]*pendingLogin{}}
	csp := "default-src 'self'; style-src 'self' 'unsafe-inline'; frame-ancestors 'none'; form-action 'self'"
	if cfg.OIDC != nil {
		frame := "'self'"
		if o := cfg.OIDC.ProviderOrigin(); o != "" {
			frame = "'self' " + o
		}
		csp += "; frame-src " + frame
	}
	s.csp = csp
	s.mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, "/app", http.StatusFound) })
	s.mux.HandleFunc("GET /login", s.loginPage)
	s.mux.HandleFunc("GET /scan", s.scan)
	s.mux.HandleFunc("GET /auth/thaid/start", s.start)
	s.mux.HandleFunc("GET "+cfg.CallbackPath, s.callback)
	if cfg.PasteCallback {
		s.mux.HandleFunc("POST /auth/thaid/paste", s.paste)
	}
	s.mux.HandleFunc("POST /logout", s.logout)
	s.mux.Handle("GET /app", s.requireLogin(http.HandlerFunc(s.appPage)))
	// Catch-all: ServeMux routes to "/" only when nothing more specific matches.
	s.mux.HandleFunc("/", s.notFound)
	return s
}

// notFound renders the friendly 404 page for any unmatched route.
func (s *Server) notFound(w http.ResponseWriter, r *http.Request) {
	s.render(w, http.StatusNotFound, notFoundTmpl, map[string]any{"Path": r.URL.Path})
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h := w.Header()
	h.Set("Content-Security-Policy", s.csp)
	h.Set("X-Frame-Options", "DENY")
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Referrer-Policy", "no-referrer")
	h.Set("Cache-Control", "no-store")
	s.mux.ServeHTTP(w, r)
}

type ctxKey struct{}

// requireLogin is the gate: without a valid session the visitor is sent to
// the ThaID login page.
func (s *Server) requireLogin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sess := s.currentSession(r)
		if sess == nil {
			http.Redirect(w, r, "/login", http.StatusFound)
			return
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), ctxKey{}, sess)))
	})
}

func (s *Server) currentSession(r *http.Request) *session {
	c, err := r.Cookie(sessionCookie)
	if err != nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	sess := s.sessions[c.Value]
	if sess == nil {
		return nil
	}
	if time.Now().After(sess.expires) {
		delete(s.sessions, c.Value)
		return nil
	}
	return sess
}

func (s *Server) loginPage(w http.ResponseWriter, r *http.Request) {
	if s.currentSession(r) != nil {
		http.Redirect(w, r, "/app", http.StatusFound)
		return
	}
	s.render(w, http.StatusOK, loginTmpl, s.loginData(r, ""))
}

// loginData fills the login page. In paste mode it also offers a bookmarklet
// that sends the callback from the registered host back to this site.
func (s *Server) loginData(r *http.Request, errMsg string) map[string]any {
	base := "http://" + r.Host
	if s.cfg.SecureCookies {
		base = "https://" + r.Host
	}
	return map[string]any{
		"Error":       errMsg,
		"Base":        base,
		"Bookmarklet": template.URL("javascript:location.href='" + base + "'+location.pathname+location.search"),
	}
}

// beginLogin prepares a ThaID login: it creates the state/nonce/PKCE (which
// stay on the server), records a pending login keyed by a random handle, and
// gives the browser only that handle in a short-lived cookie. It returns the
// provider URL to send the browser to. When the login was started on a host
// that differs from the registered callback host, it redirects there first and
// reports ok=false.
func (s *Server) beginLogin(w http.ResponseWriter, r *http.Request) (authURL string, ok bool) {
	// Outside paste mode, a login started on the wrong host name is moved to
	// the registered callback host first so ThaID's redirect returns the login
	// cookie. In paste mode the tester keeps using the current host (the
	// callback URL is brought back by hand), so we must not bounce away.
	if !s.cfg.PasteCallback {
		if o, err := url.Parse(s.cfg.CallbackOrigin); err == nil && o.Host != "" && !strings.EqualFold(o.Host, r.Host) {
			http.Redirect(w, r, o.Scheme+"://"+o.Host+r.URL.Path, http.StatusFound)
			return "", false
		}
	}
	authURL, st := s.cfg.OIDC.AuthURL()
	handle := oidc.RandomString(32)
	s.mu.Lock()
	for k, p := range s.logins {
		if time.Since(p.created) > loginTTL {
			delete(s.logins, k)
		}
	}
	s.logins[handle] = &pendingLogin{st: st, created: time.Now()}
	s.mu.Unlock()
	http.SetCookie(w, &http.Cookie{
		Name: loginCookie, Value: handle, Path: "/", MaxAge: int(loginTTL.Seconds()),
		HttpOnly: true, Secure: s.cfg.SecureCookies, SameSite: http.SameSiteLaxMode,
	})
	return authURL, true
}

// start sends the browser to ThaID (the classic full-page redirect flow).
func (s *Server) start(w http.ResponseWriter, r *http.Request) {
	authURL, ok := s.beginLogin(w, r)
	if !ok {
		return
	}
	http.Redirect(w, r, authURL, http.StatusFound)
}

// scan is kept for compatibility but, in this direct-to-ThaID build, it just
// forwards to the full-page ThaID authorization (the provider blocks embedding
// its QR in an iframe).
func (s *Server) scan(w http.ResponseWriter, r *http.Request) {
	if s.currentSession(r) != nil {
		http.Redirect(w, r, "/app", http.StatusFound)
		return
	}
	http.Redirect(w, r, "/auth/thaid/start", http.StatusFound)
}

// callback receives the browser back from ThaID with an authorization code.
func (s *Server) callback(w http.ResponseWriter, r *http.Request) {
	s.finish(w, r, r.URL.Query())
}

// paste accepts the callback URL copied from the address bar (test mode).
func (s *Server) paste(w http.ResponseWriter, r *http.Request) {
	u, err := url.Parse(strings.TrimSpace(r.PostFormValue("url")))
	if err != nil || u.Query().Get("state") == "" {
		s.render(w, http.StatusBadRequest, loginTmpl, s.loginData(r, "URL ไม่ถูกต้อง ต้องเป็นที่อยู่เต็มที่มี code และ state"))
		return
	}
	s.finish(w, r, u.Query())
}

func (s *Server) finish(w http.ResponseWriter, r *http.Request, q url.Values) {
	http.SetCookie(w, &http.Cookie{Name: loginCookie, Path: "/", MaxAge: -1, HttpOnly: true, Secure: s.cfg.SecureCookies, SameSite: http.SameSiteLaxMode})

	var pending *pendingLogin
	if c, err := r.Cookie(loginCookie); err == nil {
		s.mu.Lock()
		pending = s.logins[c.Value]
		delete(s.logins, c.Value) // one attempt per login
		s.mu.Unlock()
	}
	fail := func(code int, msg, logMsg string) {
		log.Printf("login failed: %s", logMsg)
		s.render(w, code, loginTmpl, s.loginData(r, msg))
	}
	switch {
	case pending == nil || time.Since(pending.created) > loginTTL:
		fail(http.StatusBadRequest, "ไม่พบรายการเข้าสู่ระบบหรือหมดเวลาแล้ว กรุณาเริ่มใหม่", "no pending login")
		return
	case subtle.ConstantTimeCompare([]byte(q.Get("state")), []byte(pending.st.State)) != 1:
		fail(http.StatusBadRequest, "คำขอไม่ถูกต้อง (state ไม่ตรงกัน) กรุณาเริ่มใหม่", "state mismatch")
		return
	case q.Get("error") == "access_denied":
		fail(http.StatusForbidden, "คุณปฏิเสธการยืนยันตัวตนในแอป ThaID", "user denied")
		return
	case q.Get("error") != "":
		fail(http.StatusBadGateway, "ThaID แจ้งข้อผิดพลาด: "+q.Get("error"), "provider error "+q.Get("error")+" "+q.Get("error_description"))
		return
	case q.Get("code") == "":
		fail(http.StatusBadRequest, "ไม่ได้รับรหัสยืนยันจาก ThaID", "missing code")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	tokens, err := s.cfg.OIDC.ExchangeTokens(ctx, q.Get("code"), pending.st)
	if err != nil {
		fail(http.StatusBadGateway, "ยืนยันตัวตนกับ ThaID ไม่สำเร็จ: "+err.Error(), err.Error())
		return
	}
	claims := tokens.Claims
	if len(s.cfg.AllowedPIDs) > 0 && !slices.Contains(s.cfg.AllowedPIDs, claims.PID) {
		log.Printf("login refused: sub=%s pid=%s not in allowlist", MaskID(claims.Subject), MaskPID(claims.PID))
		s.render(w, http.StatusForbidden, deniedTmpl, map[string]any{"Name": claims.DisplayName(), "PID": MaskPID(claims.PID)})
		return
	}

	// Userinfo may hold more of the approved data than the ID token. A failure
	// here does not block the login; the page shows the error instead.
	sess := &session{claims: claims, tokenResp: tokens.Response, welcome: true}
	if info, err := s.cfg.OIDC.UserInfo(ctx, tokens.AccessToken); err != nil {
		sess.userinfoErr = err.Error()
		log.Printf("userinfo: %v", err)
	} else {
		sess.userinfo = info
	}

	id := oidc.RandomString(32)
	now := time.Now()
	sess.created, sess.expires = now, now.Add(s.cfg.SessionTTL)
	s.mu.Lock()
	for k, v := range s.sessions {
		if now.After(v.expires) {
			delete(s.sessions, k)
		}
	}
	s.sessions[id] = sess
	s.mu.Unlock()
	http.SetCookie(w, &http.Cookie{
		Name: sessionCookie, Value: id, Path: "/", MaxAge: int(s.cfg.SessionTTL.Seconds()),
		HttpOnly: true, Secure: s.cfg.SecureCookies, SameSite: http.SameSiteLaxMode,
	})
	log.Printf("login ok: sub=%s pid=%s claims=%v", MaskID(claims.Subject), MaskPID(claims.PID), claims.Keys)
	if s.cfg.DumpClaims {
		if b, err := json.MarshalIndent(map[string]any{
			"id_token_claims": claims.Raw,
			"token_response":  tokens.Response,
			"userinfo":        sess.userinfo,
		}, "", "  "); err == nil {
			log.Printf("THAID_CLAIMS_DUMP\n%s", b)
		}
	}
	http.Redirect(w, r, "/app", http.StatusFound)
}

func (s *Server) logout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(sessionCookie); err == nil {
		s.mu.Lock()
		delete(s.sessions, c.Value)
		s.mu.Unlock()
	}
	http.SetCookie(w, &http.Cookie{Name: sessionCookie, Path: "/", MaxAge: -1, HttpOnly: true, Secure: s.cfg.SecureCookies, SameSite: http.SameSiteLaxMode})
	http.Redirect(w, r, "/login", http.StatusSeeOther)
}

func (s *Server) appPage(w http.ResponseWriter, r *http.Request) {
	sess := r.Context().Value(ctxKey{}).(*session)
	s.mu.Lock()
	welcome := sess.welcome
	sess.welcome = false
	s.mu.Unlock()

	c := sess.claims
	person, technical := buildRows(
		source{"ID Token", c.Raw},
		source{"Token response", sess.tokenResp},
		source{"UserInfo", sess.userinfo},
	)
	s.render(w, http.StatusOK, appTmpl, map[string]any{
		"Welcome": welcome, "Name": c.DisplayName(), "PID": FormatPID(c.PID),
		"Person": person, "Technical": technical, "UserinfoErr": sess.userinfoErr,
		"LoginAt": sess.created.Format("02/01/2006 15:04:05"), "Expires": sess.expires.Format("15:04"),
	})
}

// MaskPID hides all but the last three digits of a citizen ID for display and logs.
func MaskPID(pid string) string {
	if len(pid) != 13 {
		return "-"
	}
	return "x-xxxx-xxxxx-" + pid[10:12] + "-" + pid[12:]
}

// MaskID keeps only the last three characters of an identifier for logs.
// ThaID's sub can be the citizen ID itself, so it must not be logged in full.
func MaskID(id string) string {
	if len(id) <= 3 {
		return "***"
	}
	return "***" + id[len(id)-3:]
}
