package auth

import (
	"net/http"
	"strings"
)

func Middleware(username, password, realm string, next http.Handler) http.Handler {
	if realm == "" {
		realm = "noitsdav"
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, pass, ok := r.BasicAuth()
		if !ok || user != username || pass != password {
			w.Header().Set("WWW-Authenticate", `Basic realm="`+escapeRealm(realm)+`"`)
			http.Error(w, "authentication required", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func escapeRealm(realm string) string {
	return strings.ReplaceAll(realm, `"`, `'`)
}

