package i18n

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
)

const (
	LangEN = "en"
	LangHE = "he"

	cookieName = "ikite_lang"
)

// Translator resolves message keys for a locale.
type Translator struct {
	Lang string
}

// FromRequest returns the translator for the request locale.
func FromRequest(r *http.Request) *Translator {
	return &Translator{Lang: Resolve(r)}
}

// Resolve picks en or he from cookie (default en).
func Resolve(r *http.Request) string {
	if c, err := r.Cookie(cookieName); err == nil {
		if c.Value == LangHE {
			return LangHE
		}
	}
	return LangEN
}

// Dir returns document direction for the locale.
func (t *Translator) Dir() string {
	if t.Lang == LangHE {
		return "rtl"
	}
	return "ltr"
}

// T returns the translated string for key, falling back to English then the key.
func (t *Translator) T(key string, args ...any) string {
	s := lookup(t.Lang, key)
	if s == "" {
		s = lookup(LangEN, key)
	}
	if s == "" {
		return key
	}
	if len(args) > 0 {
		return fmt.Sprintf(s, args...)
	}
	return s
}

// JSON returns a JSON object of client-side strings for inline scripts.
func (t *Translator) JSON() string {
	m := clientMessages(t.Lang)
	b, err := json.Marshal(m)
	if err != nil {
		return "{}"
	}
	return string(b)
}

// SwitchURL returns the current path with ?lang=tag to set locale.
func SwitchURL(r *http.Request, tag string) string {
	u := *r.URL
	q := u.Query()
	q.Set("lang", tag)
	u.RawQuery = q.Encode()
	if u.Path == "" {
		u.Path = "/"
	}
	return u.RequestURI()
}

// Middleware handles ?lang= en|he by setting a cookie and redirecting.
func Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		lang := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("lang")))
		if lang == LangEN || lang == LangHE {
			http.SetCookie(w, &http.Cookie{
				Name:     cookieName,
				Value:    lang,
				Path:     "/",
				MaxAge:   365 * 24 * 3600,
				HttpOnly: false,
				SameSite: http.SameSiteLaxMode,
			})
			u := *r.URL
			q := u.Query()
			q.Del("lang")
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

// AppendLangQuery adds lang to a URL if not default English.
func AppendLangQuery(raw string, lang string) string {
	if lang == "" || lang == LangEN {
		return raw
	}
	u, err := url.Parse(raw)
	if err != nil {
		return raw
	}
	q := u.Query()
	q.Set("lang", lang)
	u.RawQuery = q.Encode()
	return u.String()
}

func lookup(lang, key string) string {
	if m, ok := messages[lang]; ok {
		return m[key]
	}
	return ""
}
