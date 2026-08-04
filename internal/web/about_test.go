package web

import (
	"strings"
	"testing"
)

func TestRenderCVMarkdown(t *testing.T) {
	out, err := renderCVMarkdown([]byte("**BENIAMIN LEVIN**\n\n- item one\n- [LinkedIn](https://example.com)"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "<strong>BENIAMIN LEVIN</strong>") {
		t.Fatalf("expected strong name: %s", out)
	}
	if !strings.Contains(out, "<li>item one</li>") {
		t.Fatalf("expected list item: %s", out)
	}
	if !strings.Contains(out, "href=\"https://example.com\"") {
		t.Fatalf("expected link: %s", out)
	}
}
