package web

import (
	"crypto/subtle"
	"net/http"
	"net/url"
)

const secretCookie = "ikite_secret"

func (s *Server) authorizeSettings(w http.ResponseWriter, r *http.Request) bool {
	return s.authorizeSecret(w, r)
}

func settingsPassFromRequest(r *http.Request) string {
	if p := r.URL.Query().Get("pass"); p != "" {
		return p
	}
	if err := r.ParseForm(); err == nil {
		if p := r.FormValue("pass"); p != "" {
			return p
		}
	}
	if c, err := r.Cookie(secretCookie); err == nil {
		return c.Value
	}
	return ""
}

func settingsPathWithPass(path, pass string) string {
	if pass == "" {
		return path
	}
	u, err := url.Parse(path)
	if err != nil {
		return path + "?pass=" + url.QueryEscape(pass)
	}
	q := u.Query()
	q.Set("pass", pass)
	u.RawQuery = q.Encode()
	return u.String()
}

func (s *Server) authorizeSecret(w http.ResponseWriter, r *http.Request) bool {
	pass := s.Cfg.SettingsPass
	if pass == "" {
		http.NotFound(w, r)
		return false
	}

	provided := settingsPassFromRequest(r)
	if subtle.ConstantTimeCompare([]byte(provided), []byte(pass)) == 1 {
		http.SetCookie(w, &http.Cookie{
			Name:     secretCookie,
			Value:    pass,
			Path:     "/",
			HttpOnly: true,
			SameSite: http.SameSiteLaxMode,
			MaxAge:   365 * 24 * 3600,
			Secure:   r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https",
		})
		return true
	}

	http.NotFound(w, r)
	return false
}
