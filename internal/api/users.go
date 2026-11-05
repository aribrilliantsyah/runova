package api

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/mail"
	"strconv"
	"strings"
	"unicode/utf8"

	"runova/internal/auth"
	"runova/internal/store"
)

func validateProfile(displayName, email string) error {
	if utf8.RuneCountInString(displayName) > 100 {
		return errors.New("Nama tampilan maksimal 100 karakter")
	}
	if email != "" {
		addr, err := mail.ParseAddress(email)
		if err != nil || addr.Address != email || len(email) > 254 {
			return errors.New("Email tidak valid")
		}
	}
	return nil
}

func validRole(r store.Role) bool { return r == store.RoleAdmin || r == store.RoleUser }

// listUsers serves one page of the Users table with usage merged in:
// q (username, name, email), role, sort, order, limit, offset.
func (s *server) listUsers(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	f := store.UserListFilter{Q: q.Get("q"), Role: store.Role(q.Get("role")), Sort: q.Get("sort"), Order: q.Get("order")}
	for _, p := range []struct {
		name string
		dst  *int
	}{{"limit", &f.Limit}, {"offset", &f.Offset}} {
		if v := q.Get(p.name); v != "" {
			n, err := strconv.Atoi(v)
			if err != nil || n < 0 {
				writeError(w, http.StatusBadRequest, "Parameter "+p.name+" tidak valid")
				return
			}
			*p.dst = n
		}
	}
	page, err := s.store.UserList(r.Context(), f)
	if errors.Is(err, store.ErrBadFilter) {
		writeError(w, http.StatusBadRequest, "Filter tidak valid: sort, order, atau role tidak dikenal")
		return
	}
	if err != nil {
		serverError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, page)
}

func (s *server) createUser(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Username    string     `json:"username"`
		DisplayName string     `json:"display_name"`
		Email       string     `json:"email"`
		Password    string     `json:"password"`
		Role        store.Role `json:"role"`
	}
	if !decode(w, r, &body) {
		return
	}
	body.DisplayName = strings.TrimSpace(body.DisplayName)
	body.Email = strings.TrimSpace(body.Email)
	if body.Role == "" {
		body.Role = store.RoleUser
	}
	for _, err := range []error{
		store.ValidateUsername(body.Username),
		store.ValidatePassword(body.Password),
		validateProfile(body.DisplayName, body.Email),
	} {
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
	}
	if !validRole(body.Role) {
		writeError(w, http.StatusBadRequest, "Peran harus admin atau user")
		return
	}
	settings, err := s.store.Settings(r.Context())
	if err != nil {
		serverError(w, r, err)
		return
	}
	hash, err := auth.HashPassword(body.Password)
	if err != nil {
		serverError(w, r, err)
		return
	}
	u, err := s.store.CreateUser(r.Context(), store.NewUser{
		Username: body.Username, DisplayName: body.DisplayName, Email: body.Email,
		PasswordHash: hash, Role: body.Role, Quota: settings.DefaultQuota,
	}, s.now().UnixMilli())
	if errors.Is(err, store.ErrConflict) {
		writeError(w, http.StatusConflict, "Username sudah dipakai")
		return
	}
	if err != nil {
		serverError(w, r, err)
		return
	}
	s.userRoleHook(r, "Role PostgreSQL untuk "+u.Username+" belum dibuat", func() error { return s.dbs.OnUserCreated(r.Context(), u) })
	s.record(r, auth.FromContext(r.Context()), store.ActUserCreate, u.Username, "role="+string(u.Role))
	writeJSON(w, http.StatusCreated, u)
}

func (s *server) updateUser(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	var body struct {
		DisplayName *string     `json:"display_name"`
		Email       *string     `json:"email"`
		Role        *store.Role `json:"role"`
		// Quota is merged onto the current quota: a field the body leaves out keeps its value.
		Quota json.RawMessage `json:"quota"`
	}
	if !decode(w, r, &body) {
		return
	}
	if body.DisplayName != nil {
		v := strings.TrimSpace(*body.DisplayName)
		body.DisplayName = &v
	}
	if body.Email != nil {
		v := strings.TrimSpace(*body.Email)
		body.Email = &v
	}
	if body.DisplayName != nil {
		if err := validateProfile(*body.DisplayName, ""); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
	}
	if body.Email != nil {
		if err := validateProfile("", *body.Email); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
	}
	if body.Role != nil && !validRole(*body.Role) {
		writeError(w, http.StatusBadRequest, "Peran harus admin atau user")
		return
	}
	var quota *store.Quota
	if body.Quota != nil {
		cur, err := s.store.User(r.Context(), id)
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "Pengguna tidak ditemukan")
			return
		}
		if err != nil {
			serverError(w, r, err)
			return
		}
		q := cur.Quota
		if err := json.Unmarshal(body.Quota, &q); err != nil {
			writeError(w, http.StatusBadRequest, "Kuota tidak valid")
			return
		}
		if err := q.Validate(); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		quota = &q
	}

	actor := auth.FromContext(r.Context())
	if id == actor.ID && body.Role != nil && *body.Role != store.RoleAdmin {
		s.rejectSelfDemotion(w, r)
		return
	}

	u, err := s.store.UpdateUser(r.Context(), id, store.UserPatch{
		DisplayName: body.DisplayName, Email: body.Email, Role: body.Role, Quota: quota,
	})
	switch {
	case errors.Is(err, store.ErrNotFound):
		writeError(w, http.StatusNotFound, "Pengguna tidak ditemukan")
		return
	case errors.Is(err, store.ErrLastAdmin):
		writeError(w, http.StatusConflict, "Minimal harus ada satu admin")
		return
	case err != nil:
		serverError(w, r, err)
		return
	}
	if body.Quota != nil {
		s.userRoleHook(r, "Batas koneksi PostgreSQL untuk "+u.Username+" belum diterapkan", func() error { return s.dbs.OnQuotaChanged(r.Context(), u) })
	}

	if body.DisplayName != nil || body.Email != nil || body.Role != nil {
		var fields []string
		if body.DisplayName != nil {
			fields = append(fields, "display_name")
		}
		if body.Email != nil {
			fields = append(fields, "email")
		}
		if body.Role != nil {
			fields = append(fields, "role="+string(*body.Role))
		}
		s.record(r, actor, store.ActUserUpdate, u.Username, strings.Join(fields, ","))
	}
	if body.Quota != nil {
		q := u.Quota
		s.record(r, actor, store.ActQuotaUpdate, u.Username,
			"apps="+itoa(q.Apps)+" memory_mb="+itoa(q.MemoryMB)+" cpu_milli="+itoa(q.CPUMilli)+
				" databases="+itoa(q.Databases)+" pg_connections="+itoa(q.PGConnections)+" disk_mb="+itoa(q.DiskMB))
	}
	writeJSON(w, http.StatusOK, u)
}

// rejectSelfDemotion answers 409 for an admin lowering their own role: the
// last-admin message when no other admin exists, otherwise the self message.
func (s *server) rejectSelfDemotion(w http.ResponseWriter, r *http.Request) {
	users, err := s.store.Users(r.Context())
	if err != nil {
		serverError(w, r, err)
		return
	}
	admins := 0
	for _, u := range users {
		if u.IsAdmin() {
			admins++
		}
	}
	if admins <= 1 {
		writeError(w, http.StatusConflict, "Minimal harus ada satu admin")
	} else {
		writeError(w, http.StatusConflict, "Tidak bisa menurunkan peran akun sendiri")
	}
}

func (s *server) resetPassword(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	var body struct{ Password string }
	if !decode(w, r, &body) {
		return
	}
	if err := store.ValidatePassword(body.Password); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	target, err := s.store.User(r.Context(), id)
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "Pengguna tidak ditemukan")
		return
	}
	if err != nil {
		serverError(w, r, err)
		return
	}
	if !s.setPassword(w, r, id, body.Password) {
		return
	}
	s.record(r, auth.FromContext(r.Context()), store.ActPasswordChange, target.Username, "reset by admin")
	writeJSON(w, http.StatusOK, okBody)
}

func (s *server) deleteUser(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	actor := auth.FromContext(r.Context())
	if id == actor.ID {
		writeError(w, http.StatusConflict, "Tidak bisa menghapus akun sendiri")
		return
	}
	target, err := s.store.User(r.Context(), id)
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "Pengguna tidak ditemukan")
		return
	}
	if err != nil {
		serverError(w, r, err)
		return
	}
	if s.apps != nil {
		if err := s.apps.DeleteAllForUser(r.Context(), actor, target); err != nil {
			appError(w, r, err)
			return
		}
	}
	s.userRoleHook(r, "Role PostgreSQL untuk "+target.Username+" belum dihapus", func() error { return s.dbs.OnUserDeleted(r.Context(), target.Username) })
	switch err := s.store.DeleteUser(r.Context(), id); {
	case errors.Is(err, store.ErrNotFound):
		writeError(w, http.StatusNotFound, "Pengguna tidak ditemukan")
		return
	case errors.Is(err, store.ErrLastAdmin):
		writeError(w, http.StatusConflict, "Minimal harus ada satu admin")
		return
	case err != nil:
		serverError(w, r, err)
		return
	}
	if s.apps != nil {
		if err := s.apps.ApplyCaddy(context.WithoutCancel(r.Context())); err != nil {
			slog.Error("update caddy after user delete", "user", target.Username, "err", err)
		}
	}
	s.record(r, actor, store.ActUserDelete, target.Username, "")
	writeJSON(w, http.StatusOK, okBody)
}
