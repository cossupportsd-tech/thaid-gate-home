// Command mockthaid runs a local stand-in for ThaID so the login flow can be
// tested end to end, including scanning the QR code with a real phone.
package main

import (
	"log"
	"net"
	"net/http"
	"os"
	"time"

	"thaidgate/internal/mockthaid"
)

func main() {
	addr := env("LISTEN", ":9000")
	_, port, _ := net.SplitHostPort(addr)
	public := env("MOCK_PUBLIC_URL", "http://"+lanIP()+":"+port)

	srv, err := mockthaid.New(mockthaid.Config{
		Issuer:    env("MOCK_ISSUER", "http://localhost:"+port),
		PublicURL: public,
		Clients: map[string]mockthaid.Client{
			env("MOCK_CLIENT_ID", "demo-web"): {
				Name:         env("MOCK_CLIENT_NAME", "ระบบสารสนเทศ (ทดสอบ)"),
				Secret:       env("MOCK_CLIENT_SECRET", "demo-secret"),
				RedirectURIs: []string{env("MOCK_REDIRECT_URI", "http://localhost:8081/callback")},
			},
		},
	})
	if err != nil {
		log.Fatal(err)
	}
	log.Printf("mock ThaID on %s; phones open QR links at %s (PIN %s)", addr, public, mockthaid.TestPIN)
	hs := &http.Server{Addr: addr, Handler: srv, ReadHeaderTimeout: 10 * time.Second}
	log.Fatal(hs.ListenAndServe())
}

// lanIP finds the address other devices on the network use to reach this
// machine. No packet is sent; UDP "dial" only picks the outgoing interface.
func lanIP() string {
	c, err := net.Dial("udp", "8.8.8.8:80")
	if err != nil {
		return "localhost"
	}
	defer c.Close()
	return c.LocalAddr().(*net.UDPAddr).IP.String()
}

func env(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}
