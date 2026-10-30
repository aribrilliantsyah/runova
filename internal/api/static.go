package api

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io/fs"
	"net/http"
	"path"
	"regexp"
	"sort"
	"strings"
)

const (
	assetPrefix = "/assets/"
	// contentSecurityPolicy lets the panel load nothing from outside its own origin.
	contentSecurityPolicy = "default-src 'self'; img-src 'self' data:; style-src 'self'; script-src 'self'; connect-src 'self'; frame-ancestors 'none'"
)

var (
	// assetRefRE finds references to panel files in index.html, scripts and styles.
	assetRefRE = regexp.MustCompile(`/assets/[A-Za-z0-9_./@-]*[A-Za-z0-9_]\.(?:js|mjs|css|svg|woff2|png|ico)`)
	// assetTagRE is the index.html placeholder {{asset "js/main.js"}}.
	assetTagRE = regexp.MustCompile(`\{\{asset "([^"]+)"\}\}`)

	contentTypes = map[string]string{
		".js":    "text/javascript; charset=utf-8",
		".mjs":   "text/javascript; charset=utf-8",
		".css":   "text/css; charset=utf-8",
		".svg":   "image/svg+xml",
		".woff2": "font/woff2",
		".png":   "image/png",
		".ico":   "image/x-icon",
	}
)

type asset struct {
	data []byte
	typ  string
}

// panel is the embedded web panel prepared for serving: every file but
// index.html is published under /assets/<dir>/<name>.<hash>.<ext>.
type panel struct {
	index  []byte
	assets map[string]asset // key: URL path below /assets/
}

// newPanel hashes fsys. One hash covers every file, so a reference between
// files never goes stale: if any file changes, every URL changes. It panics
// when index.html, a script or a style references a file the bundle lacks;
// that is a build defect, not a runtime condition.
func newPanel(fsys fs.FS) *panel {
	var names []string
	err := fs.WalkDir(fsys, ".", func(p string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			names = append(names, p)
		}
		return err
	})
	if err != nil {
		panic(fmt.Sprintf("api: read panel files: %v", err))
	}
	sort.Strings(names)

	raw := make(map[string][]byte, len(names))
	sum := sha256.New()
	for _, n := range names {
		b, err := fs.ReadFile(fsys, n)
		if err != nil {
			panic(fmt.Sprintf("api: read panel file %s: %v", n, err))
		}
		raw[n] = b
		fmt.Fprintf(sum, "%s\x00%d\x00", n, len(b))
		sum.Write(b)
	}
	if _, ok := raw["index.html"]; !ok {
		panic("api: panel has no index.html")
	}
	hash := hex.EncodeToString(sum.Sum(nil))[:8]

	hashed := func(name string) string {
		ext := path.Ext(name)
		return strings.TrimSuffix(name, ext) + "." + hash + ext
	}
	rewrite := func(owner string, b []byte) []byte {
		return assetRefRE.ReplaceAllFunc(b, func(ref []byte) []byte {
			name := strings.TrimPrefix(string(ref), assetPrefix)
			if _, ok := raw[name]; !ok || name == "index.html" {
				panic(fmt.Sprintf("api: %s references missing panel file %s", owner, ref))
			}
			return []byte(assetPrefix + hashed(name))
		})
	}

	p := &panel{assets: make(map[string]asset, len(names))}
	for _, n := range names {
		if n == "index.html" {
			continue
		}
		data := raw[n]
		if ext := path.Ext(n); ext == ".js" || ext == ".mjs" || ext == ".css" {
			data = rewrite(n, data)
		}
		typ := contentTypes[path.Ext(n)]
		if typ == "" {
			typ = "application/octet-stream"
		}
		p.assets[hashed(n)] = asset{data, typ}
	}

	index := assetTagRE.ReplaceAllFunc(raw["index.html"], func(tag []byte) []byte {
		return []byte(assetPrefix + string(assetTagRE.FindSubmatch(tag)[1]))
	})
	p.index = rewrite("index.html", index)
	return p
}

func securityHeaders(w http.ResponseWriter) {
	h := w.Header()
	h.Set("Content-Security-Policy", contentSecurityPolicy)
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Referrer-Policy", "same-origin")
}

func (p *panel) serveIndex(w http.ResponseWriter, r *http.Request) {
	securityHeaders(w)
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		writeError(w, http.StatusMethodNotAllowed, "Metode tidak diizinkan")
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Write(p.index)
}

func (p *panel) serveAsset(w http.ResponseWriter, r *http.Request) {
	securityHeaders(w)
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		writeError(w, http.StatusMethodNotAllowed, "Metode tidak diizinkan")
		return
	}
	a, ok := p.assets[strings.TrimPrefix(r.URL.Path, assetPrefix)]
	if !ok {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", a.typ)
	w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	w.Write(a.data)
}

// routes serves the panel: assets under /assets/, and index.html for every
// other GET or HEAD so that a reload on any path opens the panel.
func (p *panel) routes(mux *http.ServeMux) {
	mux.HandleFunc(assetPrefix, p.serveAsset)
	mux.HandleFunc("/", p.serveIndex)
}
