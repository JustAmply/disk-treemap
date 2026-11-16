package api

import (
	"crypto/sha256"
	"crypto/subtle"
	"net/http"
)

// RequireAuth rejects cross-origin browser mutations and protects the UI and API
// when credentials are configured.
// Deploy behind HTTPS when clients connect over an untrusted network.
func RequireAuth(next http.Handler, username, password string) http.Handler {
	next = http.NewCrossOriginProtection().Handler(next)
	if username == "" && password == "" {
		return next
	}
	wantUser := sha256.Sum256([]byte(username))
	wantPassword := sha256.Sum256([]byte(password))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, pass, ok := r.BasicAuth()
		gotUser := sha256.Sum256([]byte(user))
		gotPassword := sha256.Sum256([]byte(pass))
		validUser := subtle.ConstantTimeCompare(gotUser[:], wantUser[:])
		validPassword := subtle.ConstantTimeCompare(gotPassword[:], wantPassword[:])
		if !ok || validUser&validPassword != 1 {
			w.Header().Set("WWW-Authenticate", `Basic realm="Disk Treemap", charset="UTF-8"`)
			w.Header().Set("Cache-Control", "no-store")
			http.Error(w, "authentication required", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}
