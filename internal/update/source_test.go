package update

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func releaseServer(t *testing.T, latest func(w http.ResponseWriter, r *http.Request)) Source {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/me/runova/releases/latest", latest)
	mux.HandleFunc("/me/runova/releases/tag/", func(http.ResponseWriter, *http.Request) {})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return Source{GitHub: srv.URL, Repo: "me/runova"}
}

func redirectTo(tag string) func(http.ResponseWriter, *http.Request) {
	return func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/me/runova/releases/tag/"+tag, http.StatusFound)
	}
}

func TestSourceLatest(t *testing.T) {
	for _, tc := range []struct {
		name    string
		handler func(http.ResponseWriter, *http.Request)
		want    string
		wantErr bool
	}{
		{"redirect to tag", redirectTo("v0.3.0"), "0.3.0", false},
		{"pre-release tag", redirectTo("v0.3.0-rc.1"), "0.3.0-rc.1", false},
		{"no redirect", func(http.ResponseWriter, *http.Request) {}, "", true},
		{"unparsable tag", redirectTo("vbogus"), "", true},
		{"two-part tag", redirectTo("v1.2"), "", true},
		{"tag without v", redirectTo("0.3.0"), "", true},
		{"server error", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(500) }, "", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := releaseServer(t, tc.handler).Latest(context.Background())
			if (err != nil) != tc.wantErr || got != tc.want {
				t.Fatalf("Latest = %q, %v; want %q, error=%v", got, err, tc.want, tc.wantErr)
			}
		})
	}
}

func TestSourceLatestNetworkFailure(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	url := srv.URL
	srv.Close()
	if v, err := (Source{GitHub: url, Repo: "me/runova"}).Latest(context.Background()); err == nil {
		t.Fatalf("Latest = %q, want an error", v)
	}
}

func TestSourceURLs(t *testing.T) {
	s := Source{GitHub: "https://example.test/", Repo: "me/runova"}
	if got, want := s.ReleaseURL("0.3.0"), "https://example.test/me/runova/releases/tag/v0.3.0"; got != want {
		t.Errorf("ReleaseURL = %q, want %q", got, want)
	}
	if got, want := s.AssetURL("0.3.0", "get.sh"), "https://example.test/me/runova/releases/download/v0.3.0/get.sh"; got != want {
		t.Errorf("AssetURL = %q, want %q", got, want)
	}
}
