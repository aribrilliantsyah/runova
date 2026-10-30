package auth_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"runova/internal/auth"
	"runova/internal/store"
)

type clock struct{ t time.Time }

func (c *clock) Now() time.Time          { return c.t }
func (c *clock) advance(d time.Duration) { c.t = c.t.Add(d) }

func newClock() *clock { return &clock{t: time.Date(2026, 10, 30, 9, 0, 0, 0, time.UTC)} }

func newStore(t *testing.T) *store.Store {
	t.Helper()
	s, err := store.Open(filepath.Join(t.TempDir(), "runova.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func mustUser(t *testing.T, s *store.Store, username string, role store.Role) store.User {
	t.Helper()
	u, err := s.CreateUser(context.Background(), store.NewUser{Username: username, PasswordHash: "x", Role: role}, 1)
	if err != nil {
		t.Fatal(err)
	}
	return u
}

func TestPasswordRoundTrip(t *testing.T) {
	h, err := auth.HashPassword("rahasia-panjang")
	if err != nil {
		t.Fatal(err)
	}
	if !auth.CheckPassword(h, "rahasia-panjang") || auth.CheckPassword(h, "rahasia-salah") {
		t.Fatal("CheckPassword wrong result")
	}
}

func TestPasswordBeyond72Bytes(t *testing.T) {
	base := strings.Repeat("a", 199)
	h, err := auth.HashPassword(base + "b")
	if err != nil {
		t.Fatal(err)
	}
	if !auth.CheckPassword(h, base+"b") {
		t.Fatal("same password rejected")
	}
	if auth.CheckPassword(h, base+"c") {
		t.Fatal("passwords differing after byte 72 treated as equal")
	}
}

func TestTokens(t *testing.T) {
	a, b := auth.NewToken(), auth.NewToken()
	if len(a) != 64 || a == b {
		t.Fatalf("tokens %q %q", a, b)
	}
	if auth.HashToken(a) == a || len(auth.HashToken(a)) != 64 || auth.HashToken(a) != auth.HashToken(a) {
		t.Fatal("HashToken must be a stable 64-hex digest different from the token")
	}
}

func TestSessionCookieAttributes(t *testing.T) {
	cases := []struct {
		name       string
		remoteAddr string
		xfp        string
		secure     bool
	}{
		{"plain", "203.0.113.9:5000", "", false},
		{"loopback proxy https", "127.0.0.1:5000", "https", true},
		{"external claims https", "203.0.113.9:5000", "https", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s := newStore(t)
			u := mustUser(t, s, "budi", store.RoleUser)
			clk := newClock()
			sess := &auth.Sessions{Store: s, Now: clk.Now}
			r := httptest.NewRequest("POST", "/api/login", nil)
			r.RemoteAddr = c.remoteAddr
			if c.xfp != "" {
				r.Header.Set("X-Forwarded-Proto", c.xfp)
			}
			w := httptest.NewRecorder()
			if err := sess.Login(w, r, u.ID); err != nil {
				t.Fatal(err)
			}
			cookies := w.Result().Cookies()
			if len(cookies) != 1 {
				t.Fatalf("cookies = %v", cookies)
			}
			ck := cookies[0]
			if ck.Name != "runova_session" || !ck.HttpOnly || ck.SameSite != http.SameSiteLaxMode ||
				ck.Secure != c.secure || ck.Path != "/" || ck.MaxAge != 604800 {
				t.Fatalf("cookie = %+v", ck)
			}
			if _, err := s.SessionUser(context.Background(), ck.Value, clk.Now().UnixMilli()); err == nil {
				t.Fatal("raw cookie value is stored as session key")
			}
			got, err := s.SessionUser(context.Background(), auth.HashToken(ck.Value), clk.Now().UnixMilli())
			if err != nil || got.ID != u.ID {
				t.Fatalf("SessionUser(hash) = %+v, %v", got, err)
			}
			if _, err := s.SessionUser(context.Background(), auth.HashToken(ck.Value), clk.Now().Add(auth.SessionTTL).UnixMilli()); err == nil {
				t.Fatal("session lives past SessionTTL")
			}
		})
	}
}

func TestIsHTTPS(t *testing.T) {
	tls := httptest.NewRequest("GET", "https://x/", nil)
	if !auth.IsHTTPS(tls) {
		t.Fatal("TLS request not https")
	}
	r := httptest.NewRequest("GET", "/", nil)
	r.RemoteAddr = "[::1]:5000"
	r.Header.Set("X-Forwarded-Proto", "https")
	if !auth.IsHTTPS(r) {
		t.Fatal("loopback X-Forwarded-Proto ignored")
	}
	r.Header.Set("X-Forwarded-Proto", "http")
	if auth.IsHTTPS(r) {
		t.Fatal("X-Forwarded-Proto http treated as https")
	}
}

func TestLogoutDeletesSession(t *testing.T) {
	s := newStore(t)
	u := mustUser(t, s, "budi", store.RoleUser)
	sess := &auth.Sessions{Store: s, Now: newClock().Now}
	w := httptest.NewRecorder()
	if err := sess.Login(w, httptest.NewRequest("POST", "/", nil), u.ID); err != nil {
		t.Fatal(err)
	}
	ck := w.Result().Cookies()[0]
	r := httptest.NewRequest("POST", "/", nil)
	r.AddCookie(ck)
	if _, ok, err := sess.User(r); !ok || err != nil {
		t.Fatalf("User before logout: %v %v", ok, err)
	}
	lw := httptest.NewRecorder()
	if err := sess.Logout(lw, r); err != nil {
		t.Fatal(err)
	}
	if c := lw.Result().Cookies(); len(c) != 1 || c[0].MaxAge >= 0 {
		t.Fatalf("logout cookie = %v", c)
	}
	if _, ok, _ := sess.User(r); ok {
		t.Fatal("session survives logout")
	}
}

func TestRequire(t *testing.T) {
	s := newStore(t)
	admin := mustUser(t, s, "andi", store.RoleAdmin)
	user := mustUser(t, s, "budi", store.RoleUser)
	sess := &auth.Sessions{Store: s, Now: newClock().Now}
	fail := func(w http.ResponseWriter, status int, msg string) { http.Error(w, msg, status) }
	var seen store.User
	h := sess.Require(true, fail, func(w http.ResponseWriter, r *http.Request) {
		seen = auth.FromContext(r.Context())
	})

	login := func(u store.User) *http.Cookie {
		w := httptest.NewRecorder()
		if err := sess.Login(w, httptest.NewRequest("POST", "/", nil), u.ID); err != nil {
			t.Fatal(err)
		}
		return w.Result().Cookies()[0]
	}
	do := func(ck *http.Cookie) int {
		r := httptest.NewRequest("GET", "/", nil)
		if ck != nil {
			r.AddCookie(ck)
		}
		w := httptest.NewRecorder()
		h(w, r)
		return w.Code
	}
	if code := do(nil); code != 401 {
		t.Errorf("no cookie: %d", code)
	}
	if code := do(&http.Cookie{Name: auth.CookieName, Value: "bogus"}); code != 401 {
		t.Errorf("bogus cookie: %d", code)
	}
	if code := do(login(user)); code != 403 {
		t.Errorf("user on admin route: %d", code)
	}
	if code := do(login(admin)); code != 200 || seen.ID != admin.ID {
		t.Errorf("admin: %d, seen %+v", code, seen)
	}
}

func TestClientIP(t *testing.T) {
	cases := []struct {
		remote, xff, want string
	}{
		{"127.0.0.1:5000", "198.51.100.7, 10.0.0.1", "198.51.100.7"},
		{"[::1]:5000", "198.51.100.7", "198.51.100.7"},
		{"203.0.113.9:5000", "1.2.3.4", "203.0.113.9"},
		{"127.0.0.1:5000", "", "127.0.0.1"},
		{"127.0.0.1:5000", "not-an-ip", "127.0.0.1"},
		{"127.0.0.1:5000", "  198.51.100.7  ", "198.51.100.7"},
		{"203.0.113.9:5000", "", "203.0.113.9"},
	}
	for _, c := range cases {
		r := httptest.NewRequest("GET", "/", nil)
		r.RemoteAddr = c.remote
		if c.xff != "" {
			r.Header.Set("X-Forwarded-For", c.xff)
		}
		if got := auth.ClientIP(r); got != c.want {
			t.Errorf("ClientIP(%s, %q) = %q, want %q", c.remote, c.xff, got, c.want)
		}
	}
}

func TestThrottle(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	clk := newClock()
	th := &auth.Throttle{Store: s, Now: clk.Now}
	locked := func(user, ip string) (time.Time, bool) {
		t.Helper()
		until, l, err := th.Locked(ctx, user, ip)
		if err != nil {
			t.Fatal(err)
		}
		return until, l
	}

	for range auth.MaxFailures - 1 {
		if err := th.Fail(ctx, "budi", "1.1.1.1"); err != nil {
			t.Fatal(err)
		}
		clk.advance(time.Second)
	}
	if _, l := locked("budi", "1.1.1.1"); l {
		t.Fatal("locked after 4 failures")
	}
	failedAt := clk.Now()
	if err := th.Fail(ctx, "budi", "1.1.1.1"); err != nil {
		t.Fatal(err)
	}
	until, l := locked("budi", "1.1.1.1")
	if !l || !until.Equal(failedAt.Add(auth.Lockout)) {
		t.Fatalf("after 5th: locked=%v until=%v, want %v", l, until, failedAt.Add(auth.Lockout))
	}
	if _, l := locked("budi", "2.2.2.2"); l {
		t.Fatal("other ip locked")
	}
	if _, l := locked("andi", "1.1.1.1"); l {
		t.Fatal("other username locked")
	}
	clk.t = until
	if _, l := locked("budi", "1.1.1.1"); l {
		t.Fatal("still locked at until")
	}

	clk.t = failedAt
	if _, l := locked("budi", "1.1.1.1"); !l {
		t.Fatal("not locked again at failure time")
	}
	if err := th.Reset(ctx, "budi", "1.1.1.1"); err != nil {
		t.Fatal(err)
	}
	if _, l := locked("budi", "1.1.1.1"); l {
		t.Fatal("locked after Reset")
	}
}

func TestThrottleWindow(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	clk := newClock()
	th := &auth.Throttle{Store: s, Now: clk.Now}
	for range auth.MaxFailures - 1 {
		th.Fail(ctx, "budi", "1.1.1.1")
	}
	clk.advance(auth.Window + time.Second)
	th.Fail(ctx, "budi", "1.1.1.1")
	if _, l, _ := th.Locked(ctx, "budi", "1.1.1.1"); l {
		t.Fatal("failures older than Window counted toward lockout")
	}
}
