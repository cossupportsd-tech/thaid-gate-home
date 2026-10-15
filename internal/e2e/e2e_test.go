// Package e2e drives the whole "scan ThaID before entering the website" flow
// against the mock ThaID: browser → website → ThaID QR page → phone approves →
// browser returns → protected page.
package e2e

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"

	"thaidgate/internal/mockthaid"
	"thaidgate/internal/oidc"
	"thaidgate/internal/webapp"
)

type harness struct {
	t       *testing.T
	mock    *httptest.Server
	site    *httptest.Server
	browser *http.Client // keeps cookies, does not follow redirects
	phone   *http.Client
}

func newHarness(t *testing.T, allowed ...string) *harness {
	return newHarnessWith(t, false, allowed...)
}

func newHarnessWith(t *testing.T, paste bool, allowed ...string) *harness {
	t.Helper()
	ml, _ := net.Listen("tcp", "127.0.0.1:0")
	sl, _ := net.Listen("tcp", "127.0.0.1:0")
	mockURL, siteURL := "http://"+ml.Addr().String(), "http://"+sl.Addr().String()
	callback := siteURL + "/auth/thaid/callback"

	m, err := mockthaid.New(mockthaid.Config{Issuer: mockURL, Clients: map[string]mockthaid.Client{
		"demo": {Name: "Test Site", Secret: "s3cret", RedirectURIs: []string{callback}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	h := &harness{t: t}
	h.mock = &httptest.Server{Listener: ml, Config: &http.Server{Handler: m}}
	h.mock.Start()
	t.Cleanup(h.mock.Close)

	oc, err := oidc.Discover(context.Background(), oidc.Config{
		Issuer: mockURL, ClientID: "demo", ClientSecret: "s3cret", RedirectURL: callback,
		Scopes: []string{"openid", "pid", "name"}, UsePKCE: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	h.site = &httptest.Server{Listener: sl, Config: &http.Server{Handler: webapp.New(webapp.Config{OIDC: oc, AppName: "Test Site", AllowedPIDs: allowed, PasteCallback: paste})}}
	h.site.Start()
	t.Cleanup(h.site.Close)

	h.browser = newBrowser()
	h.phone = &http.Client{}
	return h
}

func newBrowser() *http.Client {
	jar, _ := cookiejar.New(nil)
	return &http.Client{Jar: jar, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
}

func (h *harness) get(c *http.Client, u string) (*http.Response, string) {
	h.t.Helper()
	resp, err := c.Get(u)
	if err != nil {
		h.t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp, string(b)
}

var reqIDRe = regexp.MustCompile(`data-req="([^"]+)"`)

// startLogin clicks "login with ThaID" and returns the request id shown in
// the QR code.
func (h *harness) startLogin() string {
	h.t.Helper()
	resp, _ := h.get(h.browser, h.site.URL+"/auth/thaid/start")
	if resp.StatusCode != http.StatusFound {
		h.t.Fatalf("start: status %d", resp.StatusCode)
	}
	loc := resp.Header.Get("Location")
	if !strings.HasPrefix(loc, h.mock.URL+"/api/v2/oauth2/auth/?") {
		h.t.Fatalf("start redirected to %s", loc)
	}
	resp, body := h.get(h.browser, loc)
	m := reqIDRe.FindStringSubmatch(body)
	if resp.StatusCode != http.StatusOK || m == nil {
		h.t.Fatalf("QR page: status %d", resp.StatusCode)
	}
	if !strings.Contains(body, "/app/scan?req=") {
		h.t.Fatal("QR page does not contain the scan URL")
	}
	return m[1]
}

// scan is the phone submitting the mock ThaID app form.
func (h *harness) scan(id, pid, pin, action string) int {
	h.t.Helper()
	resp, err := h.phone.PostForm(h.mock.URL+"/app/scan", url.Values{"req": {id}, "persona": {pid}, "pin": {pin}, "action": {action}})
	if err != nil {
		h.t.Fatal(err)
	}
	resp.Body.Close()
	return resp.StatusCode
}

func (h *harness) status(id string) (status, redirect string) {
	h.t.Helper()
	_, body := h.get(h.browser, h.mock.URL+"/api/v2/oauth2/auth/status?id="+url.QueryEscape(id))
	var s struct{ Status, Redirect string }
	if err := json.Unmarshal([]byte(body), &s); err != nil {
		h.t.Fatalf("status JSON: %v", err)
	}
	return s.Status, s.Redirect
}

const (
	somchai  = "0000000000019"
	outsider = "0000000000035"
)

func TestScanThaIDThenEnterSite(t *testing.T) {
	h := newHarness(t)

	// Without logging in, the protected page sends the visitor to /login.
	resp, _ := h.get(h.browser, h.site.URL+"/app")
	if resp.StatusCode != http.StatusFound || resp.Header.Get("Location") != "/login" {
		t.Fatalf("unauthenticated /app: %d %s", resp.StatusCode, resp.Header.Get("Location"))
	}

	id := h.startLogin()
	if st, _ := h.status(id); st != "pending" {
		t.Fatalf("status before scan = %s", st)
	}
	if code := h.scan(id, somchai, mockthaid.TestPIN, "approve"); code != http.StatusOK {
		t.Fatalf("phone approve: %d", code)
	}
	st, redirect := h.status(id)
	if st != "approved" || !strings.HasPrefix(redirect, h.site.URL+"/auth/thaid/callback?") {
		t.Fatalf("status after scan = %s %s", st, redirect)
	}

	resp, body := h.get(h.browser, redirect)
	if resp.StatusCode != http.StatusFound || resp.Header.Get("Location") != "/app" {
		t.Fatalf("callback: %d %s %s", resp.StatusCode, resp.Header.Get("Location"), body)
	}
	resp, body = h.get(h.browser, h.site.URL+"/app")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("protected page: %d", resp.StatusCode)
	}
	// First visit: the "login succeeded" window and every piece of data
	// received, including what came from the userinfo endpoint.
	for _, want := range []string{"เข้าสู่ระบบสำเร็จ", "สมชาย ทดสอบ", "0-0000-00000-01-9", "เลขประจำตัวประชาชน", "นามสกุล", "UserInfo", "ID Token", "mock-sub-7f3a1c"} {
		if !strings.Contains(body, want) {
			t.Errorf("protected page is missing %q", want)
		}
	}
	if strings.Contains(body, "เรียก UserInfo ไม่สำเร็จ") {
		t.Error("userinfo call failed")
	}
	// The success window shows once only.
	if _, again := h.get(h.browser, h.site.URL+"/app"); strings.Contains(again, `class="modal"`) || !strings.Contains(again, "สมชาย ทดสอบ") {
		t.Error("success window should appear on the first visit only")
	}

	// The callback URL cannot be replayed, and the code cannot be redeemed twice.
	resp, _ = h.get(h.browser, redirect)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("replayed callback: %d", resp.StatusCode)
	}
	u, _ := url.Parse(redirect)
	req, _ := http.NewRequest(http.MethodPost, h.mock.URL+"/api/v2/oauth2/token/", strings.NewReader(url.Values{
		"grant_type": {"authorization_code"}, "code": {u.Query().Get("code")}, "redirect_uri": {h.site.URL + "/auth/thaid/callback"},
	}.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.SetBasicAuth("demo", "s3cret")
	tr, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	tb, _ := io.ReadAll(tr.Body)
	tr.Body.Close()
	if tr.StatusCode != http.StatusBadRequest || !strings.Contains(string(tb), "already used") {
		t.Fatalf("reused code: %d %s", tr.StatusCode, tb)
	}

	// Logging out closes the session.
	lr, err := h.browser.PostForm(h.site.URL+"/logout", nil)
	if err != nil {
		t.Fatal(err)
	}
	lr.Body.Close()
	resp, _ = h.get(h.browser, h.site.URL+"/app")
	if resp.StatusCode != http.StatusFound {
		t.Fatalf("/app after logout: %d", resp.StatusCode)
	}
}

// In this direct-to-ThaID build, /scan forwards to the full-page ThaID start
// instead of embedding the provider QR in an iframe (which ThaID blocks).
func TestScanRedirectsToThaID(t *testing.T) {
	h := newHarness(t)
	resp, _ := h.get(h.browser, h.site.URL+"/scan")
	if resp.StatusCode != http.StatusFound || resp.Header.Get("Location") != "/auth/thaid/start" {
		t.Fatalf("scan redirect: %d %q", resp.StatusCode, resp.Header.Get("Location"))
	}
}

func TestWrongPINKeepsWaiting(t *testing.T) {
	h := newHarness(t)
	id := h.startLogin()
	if code := h.scan(id, somchai, "000000", "approve"); code != http.StatusUnauthorized {
		t.Fatalf("wrong PIN: %d", code)
	}
	if st, _ := h.status(id); st != "pending" {
		t.Fatalf("status after wrong PIN = %s", st)
	}
}

func TestUserDenies(t *testing.T) {
	h := newHarness(t)
	id := h.startLogin()
	h.scan(id, "", "", "deny")
	st, redirect := h.status(id)
	if st != "denied" {
		t.Fatalf("status = %s", st)
	}
	resp, body := h.get(h.browser, redirect)
	if resp.StatusCode != http.StatusForbidden || !strings.Contains(body, "ปฏิเสธ") {
		t.Fatalf("denied callback: %d", resp.StatusCode)
	}
	if resp, _ := h.get(h.browser, h.site.URL+"/app"); resp.StatusCode != http.StatusFound {
		t.Fatal("denied user must not get a session")
	}
}

func TestTamperedStateIsRejected(t *testing.T) {
	h := newHarness(t)
	id := h.startLogin()
	h.scan(id, somchai, mockthaid.TestPIN, "approve")
	_, redirect := h.status(id)
	u, _ := url.Parse(redirect)
	q := u.Query()
	q.Set("state", "forged")
	u.RawQuery = q.Encode()
	if resp, _ := h.get(h.browser, u.String()); resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("forged state: %d", resp.StatusCode)
	}
	if resp, _ := h.get(h.browser, h.site.URL+"/app"); resp.StatusCode != http.StatusFound {
		t.Fatal("forged state must not create a session")
	}
}

// An attacker who approves a login on their own phone must not be able to push
// the resulting callback link into someone else's browser.
func TestCallbackFromAnotherBrowserIsRejected(t *testing.T) {
	h := newHarness(t)
	id := h.startLogin()
	h.scan(id, somchai, mockthaid.TestPIN, "approve")
	_, redirect := h.status(id)
	victim := newBrowser()
	if resp, _ := h.get(victim, redirect); resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("callback in another browser: %d", resp.StatusCode)
	}
}

func TestAllowlist(t *testing.T) {
	h := newHarness(t, somchai)
	id := h.startLogin()
	h.scan(id, outsider, mockthaid.TestPIN, "approve")
	_, redirect := h.status(id)
	resp, body := h.get(h.browser, redirect)
	if resp.StatusCode != http.StatusForbidden || !strings.Contains(body, "ไม่มีสิทธิ์") {
		t.Fatalf("outsider: %d", resp.StatusCode)
	}
	if resp, _ := h.get(h.browser, h.site.URL+"/app"); resp.StatusCode != http.StatusFound {
		t.Fatal("outsider must not get a session")
	}

	// Someone on the allowlist gets in.
	id = h.startLogin()
	h.scan(id, somchai, mockthaid.TestPIN, "approve")
	_, redirect = h.status(id)
	h.get(h.browser, redirect)
	if resp, _ := h.get(h.browser, h.site.URL+"/app"); resp.StatusCode != http.StatusOK {
		t.Fatalf("allowed user: %d", resp.StatusCode)
	}
}

func TestUnregisteredRedirectURIIsNotFollowed(t *testing.T) {
	h := newHarness(t)
	resp, _ := h.get(h.browser, h.mock.URL+"/api/v2/oauth2/auth/?response_type=code&scope=openid&client_id=demo&redirect_uri=https://evil.example/cb")
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("unregistered redirect: %d %s", resp.StatusCode, resp.Header.Get("Location"))
	}
}

// Paste mode: the tester copies the callback URL from the address bar into
// the login page instead of the browser landing on this site.
func TestPasteCallbackURL(t *testing.T) {
	h := newHarnessWith(t, true)
	id := h.startLogin()
	h.scan(id, somchai, mockthaid.TestPIN, "approve")
	_, redirect := h.status(id)

	// Another browser cannot use the pasted URL.
	if resp, err := newBrowser().PostForm(h.site.URL+"/auth/thaid/paste", url.Values{"url": {redirect}}); err != nil || resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("paste from another browser: %v %v", resp.StatusCode, err)
	}

	resp, err := h.browser.PostForm(h.site.URL+"/auth/thaid/paste", url.Values{"url": {"  " + redirect + " "}})
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusFound || resp.Header.Get("Location") != "/app" {
		t.Fatalf("paste: %d %s", resp.StatusCode, resp.Header.Get("Location"))
	}
	if resp, _ := h.get(h.browser, h.site.URL+"/app"); resp.StatusCode != http.StatusOK {
		t.Fatalf("/app after paste: %d", resp.StatusCode)
	}
}

func TestPasteDisabledByDefault(t *testing.T) {
	h := newHarness(t)
	resp, err := h.browser.PostForm(h.site.URL+"/auth/thaid/paste", url.Values{"url": {"http://x/cb?code=a&state=b"}})
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode == http.StatusFound {
		t.Fatal("paste endpoint must be off unless enabled")
	}
}
