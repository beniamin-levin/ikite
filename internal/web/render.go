package web

import (
	"html/template"
	"net/http"

	"github.com/ben/ikite-go/internal/i18n"
	"github.com/ben/ikite-go/internal/theme"
)

func (s *Server) render(w http.ResponseWriter, r *http.Request, name string, data any) error {
	tr := i18n.FromRequest(r)
	th := theme.Resolve(r)
	tmpl, err := s.tmpl.Clone()
	if err != nil {
		return err
	}
	tmpl.Funcs(template.FuncMap{
		"T":        tr.T,
		"lang":     func() string { return tr.Lang },
		"dir":      func() string { return tr.Dir() },
		"langURL":  func(tag string) string { return i18n.SwitchURL(r, tag) },
		"theme":    func() string { return th },
		"themeURL": func(tag string) string { return theme.SwitchURL(r, tag) },
		"i18nJS":   func() template.JS { return template.JS(tr.JSON()) },
	})
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	return tmpl.ExecuteTemplate(w, name, data)
}
