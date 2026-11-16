package api

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestRequireAuth(t *testing.T) {
	for _, tc := range []struct {
		name, user, password string
		configured, send     bool
		want                 int
	}{
		{"disabled", "", "", false, false, 204},
		{"missing", "", "", true, false, 401},
		{"wrong user", "other", "test-password", true, true, 401},
		{"wrong password", "reader", "wrong", true, true, 401},
		{"valid", "reader", "test-password", true, true, 204},
	} {
		t.Run(tc.name, func(t *testing.T) {
			user, password := "", ""
			if tc.configured {
				user, password = "reader", "test-password"
			}
			h := RequireAuth(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(204) }), user, password)
			for _, path := range []string{"/", "/api/v1/config", "/api/v1/scans"} {
				r := httptest.NewRequest(http.MethodPost, path, nil)
				if tc.send {
					r.SetBasicAuth(tc.user, tc.password)
				}
				w := httptest.NewRecorder()
				h.ServeHTTP(w, r)
				if w.Code != tc.want {
					t.Fatalf("%s: got %d, want %d", path, w.Code, tc.want)
				}
				if w.Code == 401 && w.Header().Get("WWW-Authenticate") == "" {
					t.Fatal("missing challenge")
				}
			}
		})
	}
}
