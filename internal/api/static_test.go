package api_test

import (
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"runova/internal/api"
	"runova/internal/store"
)

const wantCSP = "default-src 'self'; img-src 'self' data:; style-src 'self'; script-src 'self'; connect-src 'self'; frame-ancestors 'none'"

func staticFS(mainJS string) fstest.MapFS {
	return fstest.MapFS{
		"index.html":     {Data: []byte(`<script type="module" src="{{asset "js/main.js"}}"></script>`)},
		"js/main.js":     {Data: []byte(mainJS)},
		"js/dom.js":      {Data: []byte(`export const h = 1`)},
		"css/base.css":   {Data: []byte(`@font-face{src:url(/assets/vendor/f.woff2)}`)},
		"vendor/f.woff2": {Data: []byte("font")},
	}
}

func staticServer(t *testing.T, fsys fstest.MapFS) *httptest.Server {
	t.Helper()
	s, err := store.Open(filepath.Join(t.TempDir(), "runova.db"))
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(api.New(api.Config{Store: s, Now: time.Now, DataDir: t.TempDir(), Static: fsys}))
	t.Cleanup(func() { srv.Close(); s.Close() })
	return srv
}

func get(t *testing.T, url string) (*http.Response, string) {
	t.Helper()
	res, err := http.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(res.Body)
	return res, string(b)
}

var mainRE = regexp.MustCompile(`/assets/js/main\.([0-9a-f]{8})\.js`)

func TestIndexHasHashedAssets(t *testing.T) {
	srv := staticServer(t, staticFS(`import {h} from "/assets/js/dom.js"`))
	res, body := get(t, srv.URL+"/")
	if res.StatusCode != 200 {
		t.Fatalf("status %d", res.StatusCode)
	}
	if !mainRE.MatchString(body) {
		t.Fatalf("index does not reference a hashed main.js: %s", body)
	}
	if got := res.Header.Get("Cache-Control"); got != "no-store" {
		t.Errorf("Cache-Control = %q", got)
	}
	if got := res.Header.Get("Content-Security-Policy"); got != wantCSP {
		t.Errorf("CSP = %q", got)
	}
	if got := res.Header.Get("X-Content-Type-Options"); got != "nosniff" {
		t.Errorf("X-Content-Type-Options = %q", got)
	}
	if got := res.Header.Get("Referrer-Policy"); got != "same-origin" {
		t.Errorf("Referrer-Policy = %q", got)
	}
	if ct := res.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
		t.Errorf("Content-Type = %q", ct)
	}
}

func TestHashedAssetCached(t *testing.T) {
	srv := staticServer(t, staticFS(`import {h} from "/assets/js/dom.js"`))
	_, index := get(t, srv.URL+"/")
	url := mainRE.FindString(index)

	res, body := get(t, srv.URL+url)
	if res.StatusCode != 200 {
		t.Fatalf("status %d", res.StatusCode)
	}
	if got := res.Header.Get("Cache-Control"); got != "public, max-age=31536000, immutable" {
		t.Errorf("Cache-Control = %q", got)
	}
	if ct := res.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/javascript") {
		t.Errorf("Content-Type = %q", ct)
	}
	if got := res.Header.Get("Content-Security-Policy"); got != wantCSP {
		t.Errorf("CSP = %q", got)
	}
	dom := regexp.MustCompile(`/assets/js/dom\.[0-9a-f]{8}\.js`).FindString(body)
	if dom == "" {
		t.Fatalf("import not rewritten to a hashed URL: %s", body)
	}
	if res, _ := get(t, srv.URL+dom); res.StatusCode != 200 {
		t.Errorf("rewritten import %s: status %d", dom, res.StatusCode)
	}

	if res, _ := get(t, srv.URL+"/assets/js/main.js"); res.StatusCode != 404 {
		t.Errorf("unhashed path: status %d, want 404", res.StatusCode)
	}
	if res, _ := get(t, srv.URL+"/assets/js/main.00000000.js"); res.StatusCode != 404 {
		t.Errorf("wrong hash: status %d, want 404", res.StatusCode)
	}
}

func TestCSSUrlsRewritten(t *testing.T) {
	srv := staticServer(t, staticFS(`x`))
	m := regexp.MustCompile(`\.([0-9a-f]{8})\.js`).FindStringSubmatch(func() string { _, b := get(t, srv.URL+"/"); return b }())
	_, css := get(t, srv.URL+"/assets/css/base."+m[1]+".css")
	font := regexp.MustCompile(`/assets/vendor/f\.[0-9a-f]{8}\.woff2`).FindString(css)
	if font == "" {
		t.Fatalf("css url not rewritten: %s", css)
	}
	if res, body := get(t, srv.URL+font); res.StatusCode != 200 || body != "font" {
		t.Errorf("font: status %d body %q", res.StatusCode, body)
	}
}

func TestAssetURLChangesWithContent(t *testing.T) {
	a := staticServer(t, staticFS(`one`))
	b := staticServer(t, staticFS(`two`))
	_, ia := get(t, a.URL+"/")
	_, ib := get(t, b.URL+"/")
	if mainRE.FindString(ia) == mainRE.FindString(ib) {
		t.Fatalf("different content, same URL %s", mainRE.FindString(ia))
	}
}

func TestSPAFallback(t *testing.T) {
	srv := staticServer(t, staticFS(`x`))
	_, index := get(t, srv.URL+"/")
	for _, path := range []string{"/apps/3", "/login"} {
		res, body := get(t, srv.URL+path)
		if res.StatusCode != 200 || body != index {
			t.Errorf("%s: status %d, index served = %v", path, res.StatusCode, body == index)
		}
	}
	res, body := get(t, srv.URL+"/api/unknown")
	if res.StatusCode != 404 || !strings.HasPrefix(res.Header.Get("Content-Type"), "application/json") || !strings.Contains(body, `"error"`) {
		t.Errorf("/api/unknown: status %d type %q body %q", res.StatusCode, res.Header.Get("Content-Type"), body)
	}
}

func TestStaticRejectsDanglingAssetReference(t *testing.T) {
	fsys := staticFS(`import "/assets/js/missing.js"`)
	s, err := store.Open(filepath.Join(t.TempDir(), "runova.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	defer func() {
		if recover() == nil {
			t.Fatal("api.New accepted a bundle that references a missing asset")
		}
	}()
	api.New(api.Config{Store: s, Now: time.Now, DataDir: t.TempDir(), Static: fsys})
}

func TestStaticOnlyServesGetAndHead(t *testing.T) {
	srv := staticServer(t, staticFS(`x`))
	_, index := get(t, srv.URL+"/")
	asset := mainRE.FindString(index)
	for _, path := range []string{"/", asset} {
		for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodDelete} {
			req, _ := http.NewRequest(method, srv.URL+path, nil)
			res, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			res.Body.Close()
			if res.StatusCode != http.StatusMethodNotAllowed {
				t.Errorf("%s %s: status %d, want 405", method, path, res.StatusCode)
			}
		}
		req, _ := http.NewRequest(http.MethodHead, srv.URL+path, nil)
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		if res.StatusCode != http.StatusOK {
			t.Errorf("HEAD %s: status %d, want 200", path, res.StatusCode)
		}
	}
}
