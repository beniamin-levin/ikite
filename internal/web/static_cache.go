package web

import (
	"io/fs"
	"net/http"
	"path"
	"strings"
)

// withStaticCache adds long-lived cache headers for versioned /static/ assets.
func withStaticCache(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/static/") {
			// Query busting (?v=) means content URLs are immutable once deployed.
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		}
		next.ServeHTTP(w, r)
	})
}

func staticFileServer(root fs.FS) http.Handler {
	files := http.FileServer(http.FS(root))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Ensure CSS/JS get a compressible content type before gzip wrapper runs.
		switch strings.ToLower(path.Ext(r.URL.Path)) {
		case ".css":
			w.Header().Set("Content-Type", "text/css; charset=utf-8")
		case ".js":
			w.Header().Set("Content-Type", "application/javascript; charset=utf-8")
		case ".svg":
			w.Header().Set("Content-Type", "image/svg+xml")
		case ".webp":
			w.Header().Set("Content-Type", "image/webp")
		case ".png":
			w.Header().Set("Content-Type", "image/png")
		case ".woff2":
			w.Header().Set("Content-Type", "font/woff2")
		}
		files.ServeHTTP(w, r)
	})
}
