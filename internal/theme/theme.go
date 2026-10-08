package theme

import (
	"net/http"
	"strings"
)

const (
	Current = "current"
	Light   = "light"

	cookieName = "ikite_theme"
)

// Resolve returns current or light from cookie (default current).
func Resolve(r *http.Request) string {
	if c, err := r.Cookie(cookieName); err == nil {
		if c.Value == Light {
			return Light
		}
	}
	return Current
}

// SwitchURL returns the current path with ?theme=tag.
func SwitchURL(r *http.Request, tag string) string {
	u := *r.URL
	q := u.Query()
	q.Set("theme", tag)
	u.RawQuery = q.Encode()
	if u.Path == "" {
		u.Path = "/"
	}
	return u.RequestURI()
}

// Middleware handles ?theme=current|light by setting a cookie and redirecting.
func Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tag := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("theme")))
		if tag == Current || tag == Light {
			http.SetCookie(w, &http.Cookie{
				Name:     cookieName,
				Value:    tag,
				Path:     "/",
				MaxAge:   365 * 24 * 3600,
				HttpOnly: false,
				SameSite: http.SameSiteLaxMode,
			})
			u := *r.URL
			q := u.Query()
			q.Del("theme")
			u.RawQuery = q.Encode()
			uri := u.RequestURI()
			if uri == "" {
				uri = "/"
			}
			http.Redirect(w, r, uri, http.StatusFound)
			return
		}
		next.ServeHTTP(w, r)
	})
}
