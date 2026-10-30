package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"sync"
	"time"

	"runova/internal/auth"
	"runova/internal/store"
)

const maxLoginName = 64

// dummyHash lets logins for unknown usernames spend the same bcrypt time as real ones.
var dummyHash = sync.OnceValue(func() string {
	h, _ := auth.HashPassword("dummy-password")
	return h
})

func clip(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n])
	}
	return s
}

// reserveAttempt reports whether (name, ip) is locked out and, if not, counts
// one failure up front. The mutex makes check-and-count atomic across requests.
func (s *server) reserveAttempt(ctx context.Context, name, ip string) (until time.Time, locked bool, err error) {
	s.loginMu.Lock()
	defer s.loginMu.Unlock()
	if until, locked, err = s.throttle.Locked(ctx, name, ip); err != nil || locked {
		return until, locked, err
	}
	return time.Time{}, false, s.throttle.Fail(ctx, name, ip)
}

func (s *server) login(w http.ResponseWriter, r *http.Request) {
	var body struct{ Username, Password string }
	if !decode(w, r, &body) {
		return
	}
	ctx := r.Context()
	name := clip(body.Username, maxLoginName)
	ip := auth.ClientIP(r)

	// Check and count the attempt before the slow password check, atomically:
	// otherwise parallel guesses all pass the lock check, and a client that drops
	// the connection mid-bcrypt would never be counted. A success resets the count.
	until, locked, err := s.reserveAttempt(ctx, name, ip)
	if err != nil {
		serverError(w, r, err)
		return
	}
	if locked {
		wait := max(int(until.Sub(s.now()).Round(time.Second)/time.Second), 1)
		w.Header().Set("Retry-After", strconv.Itoa(wait))
		writeError(w, http.StatusTooManyRequests, "Terlalu banyak percobaan. Coba lagi pukul "+until.Local().Format("15:04"))
		return
	}

	u, hash, err := s.store.UserByUsername(ctx, body.Username)
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		serverError(w, r, err)
		return
	}
	if err != nil {
		hash = dummyHash()
	}
	if err != nil || !auth.CheckPassword(hash, body.Password) {
		s.record(r, store.User{ID: u.ID, Username: name}, store.ActLoginFailed, "", "")
		writeError(w, http.StatusUnauthorized, "Username atau password salah")
		return
	}

	if err := s.throttle.Reset(ctx, name, ip); err != nil {
		serverError(w, r, err)
		return
	}
	if err := s.sessions.Login(w, r, u.ID); err != nil {
		serverError(w, r, err)
		return
	}
	s.record(r, u, store.ActLogin, "", "")
	writeJSON(w, http.StatusOK, u)
}

func (s *server) logout(w http.ResponseWriter, r *http.Request) {
	u := auth.FromContext(r.Context())
	if err := s.sessions.Logout(w, r); err != nil {
		serverError(w, r, err)
		return
	}
	s.record(r, u, store.ActLogout, "", "")
	writeJSON(w, http.StatusOK, okBody)
}

func (s *server) me(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, auth.FromContext(r.Context()))
}

func (s *server) changePassword(w http.ResponseWriter, r *http.Request) {
	var body struct{ Current, New string }
	if !decode(w, r, &body) {
		return
	}
	u := auth.FromContext(r.Context())
	_, hash, err := s.store.UserByUsername(r.Context(), u.Username)
	if err != nil {
		serverError(w, r, err)
		return
	}
	if !auth.CheckPassword(hash, body.Current) {
		writeError(w, http.StatusBadRequest, "Password saat ini salah")
		return
	}
	if err := store.ValidatePassword(body.New); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if !s.setPassword(w, r, u.ID, body.New) {
		return
	}
	s.record(r, u, store.ActPasswordChange, u.Username, "")
	writeJSON(w, http.StatusOK, okBody)
}

// setPassword stores a new password (ending all of the user's sessions) and,
// when the caller changed their own, starts a fresh session for this request.
func (s *server) setPassword(w http.ResponseWriter, r *http.Request, id int64, password string) bool {
	hash, err := auth.HashPassword(password)
	if err != nil {
		serverError(w, r, err)
		return false
	}
	if err := s.store.SetPassword(r.Context(), id, hash); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "Pengguna tidak ditemukan")
		} else {
			serverError(w, r, fmt.Errorf("set password: %w", err))
		}
		return false
	}
	if auth.FromContext(r.Context()).ID == id {
		if err := s.sessions.Login(w, r, id); err != nil {
			serverError(w, r, err)
			return false
		}
	}
	return true
}
