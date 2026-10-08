package web

import (
	"bytes"
	"compress/gzip"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestGzipCompressesHTML(t *testing.T) {
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte("<html><body>" + string(bytes.Repeat([]byte("wind "), 200)) + "</body></html>"))
	})
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Accept-Encoding", "gzip")
	rr := httptest.NewRecorder()
	withGzip(inner).ServeHTTP(rr, req)

	if rr.Header().Get("Content-Encoding") != "gzip" {
		t.Fatalf("Content-Encoding: %q", rr.Header().Get("Content-Encoding"))
	}
	gr, err := gzip.NewReader(rr.Body)
	if err != nil {
		t.Fatal(err)
	}
	defer gr.Close()
	body, err := io.ReadAll(gr)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(body, []byte("<html>")) {
		t.Fatalf("decoded body missing html: %q", body[:64])
	}
}

func TestGzipSkipsImages(t *testing.T) {
	payload := []byte{0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a}
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write(payload)
	})
	req := httptest.NewRequest(http.MethodGet, "/logo.png", nil)
	req.Header.Set("Accept-Encoding", "gzip")
	rr := httptest.NewRecorder()
	withGzip(inner).ServeHTTP(rr, req)

	if rr.Header().Get("Content-Encoding") != "" {
		t.Fatalf("unexpected Content-Encoding: %q", rr.Header().Get("Content-Encoding"))
	}
	if !bytes.Equal(rr.Body.Bytes(), payload) {
		t.Fatalf("image payload corrupted")
	}
}
