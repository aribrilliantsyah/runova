package update

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"
)

const lookupTimeout = 15 * time.Second

// Source reads releases of one GitHub repository the way get.sh does: it
// follows the redirect of /releases/latest, so it does not use api.github.com
// and its rate limit.
type Source struct {
	GitHub, Repo string
	Client       *http.Client
}

func (s Source) base() string { return strings.TrimRight(s.GitHub, "/") + "/" + s.Repo }

// ReleaseURL is the release notes page of version (without the "v").
func (s Source) ReleaseURL(version string) string {
	return s.base() + "/releases/tag/v" + version
}

// AssetURL is the download URL of a file attached to release version.
func (s Source) AssetURL(version, name string) string {
	return s.base() + "/releases/download/v" + version + "/" + name
}

// Latest returns the version of the latest release, without the "v".
func (s Source) Latest(ctx context.Context) (string, error) {
	client := s.Client
	if client == nil {
		client = &http.Client{Timeout: lookupTimeout}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodHead, s.base()+"/releases/latest", nil)
	if err != nil {
		return "", err
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("latest release: HTTP %d", resp.StatusCode)
	}
	_, tag, found := strings.Cut(resp.Request.URL.Path, "/releases/tag/")
	if !found {
		return "", errors.New("latest release: no redirect to a release tag")
	}
	version, ok := strings.CutPrefix(tag, "v")
	if !ok || !Valid(version) {
		return "", fmt.Errorf("latest release: unrecognised tag %q", tag)
	}
	return version, nil
}
