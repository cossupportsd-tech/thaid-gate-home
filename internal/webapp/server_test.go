package webapp

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// A login started on 127.0.0.1 must move to the callback's host name first,
// otherwise the login cookie would not come back with ThaID's redirect.
func TestStartMovesToCallbackHost(t *testing.T) {
	s := New(Config{CallbackPath: "/callback", CallbackOrigin: "http://localhost:8081"})
	req := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:8081/auth/thaid/start", nil)
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, req)
	if rec.Code != http.StatusFound || rec.Header().Get("Location") != "http://localhost:8081/auth/thaid/start" {
		t.Fatalf("got %d %s", rec.Code, rec.Header().Get("Location"))
	}
	if len(rec.Result().Cookies()) != 0 {
		t.Fatal("no login cookie should be set on the wrong host")
	}
}

func TestFormatValue(t *testing.T) {
	cases := map[string]struct {
		key  string
		v    any
		want string
	}{
		"pid":     {"pid", "1234567890123", "1-2345-67890-12-3"},
		"integer": {"expires_in", float64(600), "600"},
		"list":    {"aud", []any{"a", "b"}, "a, b"},
		"empty":   {"title", "", "-"},
	}
	for name, c := range cases {
		if got := formatValue(c.key, c.v); got != c.want {
			t.Errorf("%s: got %q want %q", name, got, c.want)
		}
	}
}
