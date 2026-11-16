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

func TestCrossOriginMutationsRejectedWithAndWithoutAuthentication(t *testing.T) {
	for _, authenticated := range []bool{false, true} {
		for _, tc := range []struct {
			name, method, origin, site string
			want                       int
		}{
			{"cross-site fetch", http.MethodPost, "https://attacker.example", "cross-site", http.StatusForbidden},
			{"cross-site form", http.MethodPost, "https://attacker.example", "", http.StatusForbidden},
			{"same-site subdomain", http.MethodPost, "https://other.example.com", "same-site", http.StatusForbidden},
			{"opaque origin", http.MethodPost, "null", "", http.StatusForbidden},
			{"same-origin fetch", http.MethodPost, "https://disk.example.com", "same-origin", http.StatusNoContent},
			{"same-origin legacy", http.MethodPost, "https://disk.example.com", "", http.StatusNoContent},
			{"API client", http.MethodPost, "", "", http.StatusNoContent},
			{"safe read", http.MethodGet, "https://attacker.example", "cross-site", http.StatusNoContent},
		} {
			t.Run(tc.name+map[bool]string{false: "/public", true: "/authenticated"}[authenticated], func(t *testing.T) {
				user, password := "", ""
				if authenticated {
					user, password = "reader", "test-password"
				}
				called := false
				h := RequireAuth(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { called = true; w.WriteHeader(http.StatusNoContent) }), user, password)
				req := httptest.NewRequest(tc.method, "https://disk.example.com/api/v1/scans", nil)
				if authenticated {
					req.SetBasicAuth(user, password)
				}
				if tc.origin != "" {
					req.Header.Set("Origin", tc.origin)
				}
				if tc.site != "" {
					req.Header.Set("Sec-Fetch-Site", tc.site)
				}
				rec := httptest.NewRecorder()
				h.ServeHTTP(rec, req)
				if rec.Code != tc.want {
					t.Fatalf("status=%d want=%d", rec.Code, tc.want)
				}
				if tc.want == http.StatusForbidden && called {
					t.Fatal("cross-origin request reached mutation handler")
				}
			})
		}
	}
}
