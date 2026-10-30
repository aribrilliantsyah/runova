// Package caddy builds Caddy's JSON configuration from Runova's state and
// keeps a running Caddy in line with it through the admin API.
package caddy

import (
	"errors"
	"net/netip"
	"regexp"
	"strings"
)

const maxHostLen = 253

var labelRE = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$`)

// ValidateDomain checks a host name for an app or the panel and returns it
// normalized: lowercase, without a trailing dot. Wildcards, IP addresses and
// single-label names are refused.
func ValidateDomain(s string) (string, error) {
	host := strings.TrimSuffix(strings.ToLower(strings.TrimSpace(s)), ".")
	if host == "" {
		return "", errors.New("Domain tidak boleh kosong")
	}
	if len(host) > maxHostLen {
		return "", errors.New("Domain terlalu panjang (maksimal 253 karakter)")
	}
	if _, err := netip.ParseAddr(host); err == nil {
		return "", errors.New("Gunakan nama domain, bukan alamat IP")
	}
	labels := strings.Split(host, ".")
	if len(labels) < 2 {
		return "", errors.New("Domain harus punya minimal dua bagian, misalnya app.contoh.id")
	}
	for _, l := range labels {
		if !labelRE.MatchString(l) {
			return "", errors.New("Domain hanya boleh berisi huruf kecil, angka, dan tanda hubung; tiap bagian 1–63 karakter dan tidak diawali atau diakhiri tanda hubung")
		}
	}
	return host, nil
}
