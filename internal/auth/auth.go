// Package auth hashes passwords, manages cookie sessions and limits login attempts.
package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"slices"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"

	"runova/internal/store"
)

const (
	CookieName = "runova_session"
	SessionTTL = 7 * 24 * time.Hour
)

// bcryptInput maps any password to ≤72 bytes: bcrypt rejects longer input, and
// passwords may be up to 200 characters. SHA-256 keeps all of them significant.
func bcryptInput(pw string) []byte {
	sum := sha256.Sum256([]byte(pw))
	b := make([]byte, base64.RawStdEncoding.EncodedLen(len(sum)))
	base64.RawStdEncoding.Encode(b, sum[:])
	return b
}

func HashPassword(pw string) (string, error) {
	h, err := bcrypt.GenerateFromPassword(bcryptInput(pw), bcrypt.DefaultCost)
	if err != nil {
		return "", fmt.Errorf("hash password: %w", err)
	}
	return string(h), nil
}

func CheckPassword(hash, pw string) bool {
	return bcrypt.CompareHashAndPassword([]byte(hash), bcryptInput(pw)) == nil
}

// NewToken returns 32 random bytes, hex-encoded.
func NewToken() string {
	var b [32]byte
	rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

// HashToken returns the hex SHA-256 under which a session token is stored.
func HashToken(tok string) string {
	sum := sha256.Sum256([]byte(tok))
	return hex.EncodeToString(sum[:])
}

func isLoopback(r *http.Request) bool {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return false
	}
	addr, err := netip.ParseAddr(host)
	return err == nil && addr.Unmap().IsLoopback()
}

// IsHTTPS reports whether the client reached us over TLS, directly or through
// a loopback reverse proxy.
func IsHTTPS(r *http.Request) bool {
	return r.TLS != nil || (isLoopback(r) && r.Header.Get("X-Forwarded-Proto") == "https")
}

// ParseTrusted turns settings entries (IPs or CIDRs) into prefixes; invalid
// entries are skipped, since the settings validation already rejects them.
func ParseTrusted(nets []string) []netip.Prefix {
	out := make([]netip.Prefix, 0, len(nets))
	for _, n := range nets {
		if p, err := netip.ParsePrefix(n); err == nil {
			out = append(out, p.Masked())
		} else if a, err := netip.ParseAddr(n); err == nil {
			out = append(out, netip.PrefixFrom(a.Unmap(), a.Unmap().BitLen()))
		}
	}
	return out
}

// ClientIP returns the client's address. Connections to Runova come from the
// local Caddy, which appends the peer it saw to X-Forwarded-For (keeping the
// incoming header only when that peer is a trusted proxy). So the header is
// read from the right, skipping loopback and trusted proxies; the first other
// address is the client, and anything left of it may be forged and is never
// used. When every entry is a proxy, the leftmost is used. A connection from
// outside loopback, or an unparsable entry on the way, yields the peer address.
func ClientIP(r *http.Request, trusted []netip.Prefix) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	if !isLoopback(r) {
		return host
	}
	entries := strings.Split(strings.Join(r.Header.Values("X-Forwarded-For"), ","), ",")
	var leftmost netip.Addr
	for i := len(entries) - 1; i >= 0; i-- {
		addr, err := netip.ParseAddr(strings.TrimSpace(entries[i]))
		if err != nil {
			return host
		}
		// One canonical spelling per client, or "::ffff:1.2.3.4" and "1.2.3.4"
		// would get separate login-failure counters.
		addr = addr.Unmap().WithZone("")
		if addr.IsLoopback() || slices.ContainsFunc(trusted, func(p netip.Prefix) bool { return p.Contains(addr) }) {
			leftmost = addr
			continue
		}
		return addr.String()
	}
	if leftmost.IsValid() {
		return leftmost.String()
	}
	return host
}

// Sessions issues and resolves session cookies backed by the store.
type Sessions struct {
	Store *store.Store
	Now   func() time.Time
}

func (s *Sessions) cookie(r *http.Request, value string, exp time.Time, maxAge int) *http.Cookie {
	return &http.Cookie{
		Name: CookieName, Value: value, Path: "/", Expires: exp, MaxAge: maxAge,
		HttpOnly: true, SameSite: http.SameSiteLaxMode, Secure: IsHTTPS(r),
	}
}

// Login creates a session for userID and sets the cookie.
func (s *Sessions) Login(w http.ResponseWriter, r *http.Request, userID int64) error {
	tok := NewToken()
	exp := s.Now().Add(SessionTTL)
	if err := s.Store.CreateSession(r.Context(), HashToken(tok), userID, exp.UnixMilli()); err != nil {
		return err
	}
	http.SetCookie(w, s.cookie(r, tok, exp, int(SessionTTL/time.Second)))
	return nil
}

// Logout deletes the request's session (if any) and clears the cookie.
func (s *Sessions) Logout(w http.ResponseWriter, r *http.Request) error {
	if c, err := r.Cookie(CookieName); err == nil {
		if err := s.Store.DeleteSession(r.Context(), HashToken(c.Value)); err != nil {
			return err
		}
	}
	http.SetCookie(w, s.cookie(r, "", time.Unix(0, 0), -1))
	return nil
}

// User returns the logged-in user, or ok=false when there is no valid session.
func (s *Sessions) User(r *http.Request) (store.User, bool, error) {
	c, err := r.Cookie(CookieName)
	if err != nil || c.Value == "" {
		return store.User{}, false, nil
	}
	u, err := s.Store.SessionUser(r.Context(), HashToken(c.Value), s.Now().UnixMilli())
	if errors.Is(err, store.ErrNotFound) {
		return store.User{}, false, nil
	}
	if err != nil {
		return store.User{}, false, err
	}
	return u, true, nil
}

type ctxKey struct{}

// FromContext returns the user stored by Require.
func FromContext(ctx context.Context) store.User {
	u, _ := ctx.Value(ctxKey{}).(store.User)
	return u
}

// ErrorFunc writes an error response; the API layer supplies its JSON writer.
type ErrorFunc func(w http.ResponseWriter, status int, msg string)

// Require wraps h so it runs only for a logged-in user (and an admin when admin is true).
// The user is available via FromContext.
func (s *Sessions) Require(admin bool, fail ErrorFunc, h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		u, ok, err := s.User(r)
		switch {
		case err != nil:
			fail(w, http.StatusInternalServerError, "Terjadi kesalahan di server")
		case !ok:
			fail(w, http.StatusUnauthorized, "Silakan masuk dulu")
		case admin && !u.IsAdmin():
			fail(w, http.StatusForbidden, "Khusus admin")
		default:
			h(w, r.WithContext(context.WithValue(r.Context(), ctxKey{}, u)))
		}
	}
}
