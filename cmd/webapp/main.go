// Command webapp runs the website that requires a ThaID login before any page
// can be opened. It reads settings from environment variables (see README.md)
// and, so you can just run it without flags, first loads defaults from a local
// config file (config.local.env by default) that is not committed to source.
package main

import (
	"bufio"
	"context"
	"log"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"thaidgate/internal/oidc"
	"thaidgate/internal/webapp"
)

func main() {
	loadLocalConfig()
	redirect := env("THAID_REDIRECT_URL", "http://localhost:8081/callback")
	cfg := oidc.Config{
		Issuer:       env("THAID_ISSUER", "http://localhost:9000"),
		ClientID:     env("THAID_CLIENT_ID", "demo-web"),
		ClientSecret: env("THAID_CLIENT_SECRET", "demo-secret"),
		RedirectURL:  redirect,
		Scopes:       strings.Fields(env("THAID_SCOPES", "openid pid name name_en ial")),
		AuthMethod:   env("THAID_AUTH_METHOD", "client_secret_basic"),
		UsePKCE:      env("THAID_USE_PKCE", "true") == "true",

		AllowMissingNonce: env("THAID_ALLOW_MISSING_NONCE", "false") == "true",
	}

	// The provider may still be starting (for example the mock in another
	// terminal), so retry discovery for a short while.
	var client *oidc.Client
	var err error
	for i := 0; i < 15; i++ {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		client, err = oidc.Discover(ctx, cfg)
		cancel()
		if err == nil {
			break
		}
		log.Printf("waiting for ThaID provider at %s: %v", cfg.Issuer, err)
		time.Sleep(time.Second)
	}
	if err != nil {
		log.Fatalf("cannot reach ThaID provider: %v", err)
	}

	var allowed []string
	for _, p := range strings.Split(os.Getenv("ALLOWED_PIDS"), ",") {
		if p = strings.TrimSpace(p); p != "" {
			allowed = append(allowed, p)
		}
	}
	ru, err := url.Parse(redirect)
	if err != nil || ru.Path == "" {
		log.Fatalf("THAID_REDIRECT_URL is not a valid URL: %q", redirect)
	}
	site := webapp.New(webapp.Config{
		CallbackPath:   ru.Path,
		CallbackOrigin: ru.Scheme + "://" + ru.Host,
		PasteCallback:  env("THAID_PASTE_CALLBACK", "false") == "true",
		OIDC:           client,
		AppName:        env("APP_NAME", "ระบบสารสนเทศ (ทดสอบ)"),
		AllowedPIDs:    allowed,
		SecureCookies:  strings.HasPrefix(redirect, "https://"),
		ScanTTL:        secondsEnv("THAID_SCAN_TTL", 300),
		DumpClaims:     env("THAID_DUMP_CLAIMS", "false") == "true",
	})

	addr := env("LISTEN", "127.0.0.1:8081")
	srv := &http.Server{Addr: addr, Handler: site, ReadHeaderTimeout: 10 * time.Second}
	log.Printf("website on %s, ThaID issuer %s, allowlist: %d PID(s)", addr, cfg.Issuer, len(allowed))
	if cert, key := os.Getenv("TLS_CERT"), os.Getenv("TLS_KEY"); cert != "" && key != "" {
		log.Fatal(srv.ListenAndServeTLS(cert, key))
	}
	log.Fatal(srv.ListenAndServe())
}

func env(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

// secondsEnv reads an integer number of seconds from the environment, using def
// when it is unset, not a number, or negative.
func secondsEnv(k string, def int) time.Duration {
	if v := os.Getenv(k); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 15 {
			return time.Duration(n) * time.Second
		}
	}
	return time.Duration(def) * time.Second
}

// loadLocalConfig applies a KEY=VALUE config file to the environment so the app
// runs with no flags. Variables already set in the real environment win, so this
// only fills in defaults. The path comes from THAID_CONFIG_FILE ("-" to skip);
// by default it uses config.local.env next to where the app runs. When no file
// is present the built-in (mock) defaults apply unchanged.
func loadLocalConfig() {
	path := os.Getenv("THAID_CONFIG_FILE")
	if path == "-" {
		return
	}
	if path == "" {
		path = "config.local.env"
	}
	f, err := os.Open(path)
	if err != nil {
		return
	}
	defer f.Close()

	loaded := 0
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		k, v = strings.TrimSpace(k), strings.TrimSpace(v)
		if len(v) >= 2 && (v[0] == '"' && v[len(v)-1] == '"' || v[0] == '\'' && v[len(v)-1] == '\'') {
			v = v[1 : len(v)-1]
		}
		if k == "" {
			continue
		}
		if _, set := os.LookupEnv(k); set {
			continue // explicit environment takes precedence
		}
		os.Setenv(k, v)
		loaded++
	}
	if err := sc.Err(); err != nil {
		log.Printf("config file %s: %v", path, err)
		return
	}
	log.Printf("loaded %d setting(s) from %s (values not shown)", loaded, path)
}
