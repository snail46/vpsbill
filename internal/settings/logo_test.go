package settings

import "testing"

func TestLogoContentType(t *testing.T) {
	png := []byte("\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR\x00\x00\x00\x01\x00\x00\x00\x01\x08\x06\x00\x00\x00")
	cases := map[string]struct {
		data []byte
		want string
	}{
		"png":             {png, "image/png"},
		"svg":             {[]byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 10 10"><rect width="10" height="10"/></svg>`), "image/svg+xml"},
		"svg with prolog": {[]byte("\xef\xbb\xbf<?xml version=\"1.0\"?>\n<!-- logo -->\n<!DOCTYPE svg>\n<svg xmlns=\"http://www.w3.org/2000/svg\"/>"), "image/svg+xml"},
		"html":            {[]byte(`<html><body><svg/></body></html>`), ""},
		"text":            {[]byte("hello"), ""},
	}
	for name, test := range cases {
		if got := LogoContentType(test.data); got != test.want {
			t.Errorf("%s: got %q, want %q", name, got, test.want)
		}
	}
}

func TestRuntimeLogoPrefersUpload(t *testing.T) {
	if got := (Runtime{LogoURL: "https://cdn.example.com/a.png"}).Logo(); got != "https://cdn.example.com/a.png" {
		t.Fatalf("external logo = %q", got)
	}
	if got := (Runtime{LogoVersion: "abc123"}).Logo(); got != "/api/v1/site/logo?v=abc123" {
		t.Fatalf("uploaded logo = %q", got)
	}
	if got := (Runtime{LogoDarkVersion: "def456"}).LogoDark(); got != "/api/v1/site/logo?variant=dark&v=def456" {
		t.Errorf("uploaded dark logo = %q", got)
	}
	if got := (Runtime{LogoVersion: "abc123"}).LogoDark(); got != "" {
		t.Errorf("dark logo without its own image = %q, want the light one to be used", got)
	}
	if got := (Runtime{LogoFaviconVersion: "ico1"}).Favicon(); got != "/api/v1/site/logo?variant=favicon&v=ico1" {
		t.Errorf("uploaded favicon = %q", got)
	}
	if logoColumns("favicon") != "logo_favicon_" || logoColumns("dark") != "logo_dark_" || logoColumns("") != "logo_" || logoColumns("x; DROP") != "" {
		t.Error("logo variant columns")
	}
	if got := (Runtime{}).Logo(); got != "" {
		t.Fatalf("default logo = %q", got)
	}
}
