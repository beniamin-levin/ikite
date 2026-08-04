package web

import (
	"bytes"
	"context"
	"fmt"
	"html/template"
	"io"
	"net/http"
	"sync"
	"time"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/parser"
	"github.com/yuin/goldmark/renderer/html"
)

const cvREADMEURL = "https://raw.githubusercontent.com/bugaudin/Beniamin-Levin-CV-public/refs/heads/main/README.md"

type cvCache struct {
	mu      sync.RWMutex
	html    template.HTML
	err     error
	fetched time.Time
}

var cvREADMECache cvCache

const cvCacheTTL = 1 * time.Hour

func (s *Server) handleAbout(w http.ResponseWriter, r *http.Request) {
	content, err := s.cvHTML(r.Context())
	data := map[string]any{
		"Active":  "about",
		"Content": content,
		"Error":   "",
		"Source":  cvREADMEURL,
	}
	if err != nil {
		s.Log.Error("about cv", "err", err)
		data["Error"] = "Could not load CV content. Try again later or view the source on GitHub."
	}
	if err := s.tmpl.ExecuteTemplate(w, "about.html", data); err != nil {
		s.Log.Error("render about", "err", err)
	}
}

func (s *Server) cvHTML(ctx context.Context) (template.HTML, error) {
	cvREADMECache.mu.RLock()
	if cvREADMECache.fetched.After(time.Now().Add(-cvCacheTTL)) {
		html, err := cvREADMECache.html, cvREADMECache.err
		cvREADMECache.mu.RUnlock()
		return html, err
	}
	cvREADMECache.mu.RUnlock()

	cvREADMECache.mu.Lock()
	defer cvREADMECache.mu.Unlock()

	if cvREADMECache.fetched.After(time.Now().Add(-cvCacheTTL)) {
		return cvREADMECache.html, cvREADMECache.err
	}

	md, err := fetchCVREADME(ctx)
	if err != nil {
		cvREADMECache.err = err
		cvREADMECache.fetched = time.Now()
		return "", err
	}

	out, err := renderCVMarkdown(md)
	if err != nil {
		cvREADMECache.err = err
		cvREADMECache.fetched = time.Now()
		return "", err
	}

	cvREADMECache.html = template.HTML(out)
	cvREADMECache.err = nil
	cvREADMECache.fetched = time.Now()
	return cvREADMECache.html, nil
}

func fetchCVREADME(ctx context.Context) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, cvREADMEURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "ikite-go/1.0")

	client := &http.Client{Timeout: 20 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("cv readme: HTTP %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 512*1024))
	if err != nil {
		return nil, err
	}
	return body, nil
}

func renderCVMarkdown(src []byte) (string, error) {
	md := goldmark.New(
		goldmark.WithExtensions(extension.GFM),
		goldmark.WithParserOptions(parser.WithAutoHeadingID()),
		goldmark.WithRendererOptions(html.WithUnsafe()),
	)
	var buf bytes.Buffer
	if err := md.Convert(src, &buf); err != nil {
		return "", err
	}
	return buf.String(), nil
}
