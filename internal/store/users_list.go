package store

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

// Usage is what a user has allocated: apps with their memory and CPU, and databases.
type Usage struct {
	Apps      int `json:"apps"`
	MemoryMB  int `json:"memory_mb"`
	CPUMilli  int `json:"cpu_milli"`
	Databases int `json:"databases"`
}

// UserRow is a user in the Users table with its usage and last successful login (0 = never).
type UserRow struct {
	User
	Usage       Usage `json:"usage"`
	LastLoginAt int64 `json:"last_login_at"`
}

// UserListFilter selects one page of the Users table. Sort is a column key from
// userSortCols, Order "asc" or "desc"; anything else is an error, never SQL.
type UserListFilter struct {
	Q      string
	Role   Role
	Sort   string
	Order  string
	Limit  int
	Offset int
}

type RoleCounts struct {
	All   int `json:"all"`
	Admin int `json:"admin"`
	User  int `json:"user"`
}

// UserPage is one page of users. Total counts every match; Roles counts the matches
// of Q per role, ignoring the role filter, for the role filter's own numbers.
type UserPage struct {
	Items  []UserRow  `json:"items"`
	Total  int        `json:"total"`
	Limit  int        `json:"limit"`
	Offset int        `json:"offset"`
	Roles  RoleCounts `json:"roles"`
}

var userSortCols = map[string]string{
	"username":   "u.username",
	"role":       "u.role",
	"apps":       "apps",
	"memory":     "memory_mb",
	"cpu":        "cpu_milli",
	"databases":  "dbs",
	"created":    "u.created_at",
	"last_login": "last_login",
}

const (
	userListDefault = 25
	userListMax     = 100
)

// ErrBadFilter wraps an unknown sort key, order or role in a UserListFilter.
var ErrBadFilter = errors.New("invalid filter")

// UserList returns one page of users with usage, in one query plus one count.
func (s *Store) UserList(ctx context.Context, f UserListFilter) (UserPage, error) {
	sortKey := f.Sort
	if sortKey == "" {
		sortKey = "username"
	}
	col, ok := userSortCols[sortKey]
	if !ok {
		return UserPage{}, fmt.Errorf("%w: sort %q", ErrBadFilter, f.Sort)
	}
	dir := "ASC"
	switch f.Order {
	case "", "asc":
	case "desc":
		dir = "DESC"
	default:
		return UserPage{}, fmt.Errorf("%w: order %q", ErrBadFilter, f.Order)
	}
	if f.Role != "" && f.Role != RoleAdmin && f.Role != RoleUser {
		return UserPage{}, fmt.Errorf("%w: role %q", ErrBadFilter, f.Role)
	}
	p := UserPage{Items: []UserRow{}, Limit: f.Limit, Offset: max(f.Offset, 0)}
	switch {
	case p.Limit <= 0:
		p.Limit = userListDefault
	case p.Limit > userListMax:
		p.Limit = userListMax
	}

	match := "1"
	var args []any
	if q := strings.TrimSpace(f.Q); q != "" {
		match = `(LOWER(u.username) LIKE ?1 ESCAPE '\' OR LOWER(u.display_name) LIKE ?1 ESCAPE '\' OR LOWER(u.email) LIKE ?1 ESCAPE '\')`
		args = append(args, "%"+likeEscape(strings.ToLower(q))+"%")
	}
	if err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*), COALESCE(SUM(u.role = 'admin'), 0), COALESCE(SUM(u.role = 'user'), 0) FROM users u WHERE `+match, args...).
		Scan(&p.Roles.All, &p.Roles.Admin, &p.Roles.User); err != nil {
		return UserPage{}, fmt.Errorf("count users: %w", err)
	}
	switch f.Role {
	case RoleAdmin:
		p.Total = p.Roles.Admin
	case RoleUser:
		p.Total = p.Roles.User
	default:
		p.Total = p.Roles.All
	}

	roleCond := ""
	if f.Role != "" {
		roleCond = " AND u.role = '" + string(f.Role) + "'" // validated above
	}
	order := col + " " + dir
	if sortKey == "last_login" {
		order = "last_login = 0, " + order // never logged in goes last either way
	}
	if sortKey != "username" {
		order += ", u.username"
	}
	// q is ?1 (or absent); limit and offset follow it.
	n := len(args)
	rows, err := s.db.QueryContext(ctx, `
		SELECT `+prefixed(userCols, "u.")+`,
			COALESCE(a.n, 0) AS apps, COALESCE(a.mem, 0) AS memory_mb, COALESCE(a.cpu, 0) AS cpu_milli,
			COALESCE(d.n, 0) AS dbs,
			COALESCE((SELECT MAX(at) FROM activity WHERE action = 'login' AND actor_id = u.id), 0) AS last_login
		FROM users u
		LEFT JOIN (SELECT user_id, COUNT(*) n, SUM(memory_mb) mem, SUM(cpu_milli) cpu FROM apps GROUP BY user_id) a ON a.user_id = u.id
		LEFT JOIN (SELECT user_id, COUNT(*) n FROM databases GROUP BY user_id) d ON d.user_id = u.id
		WHERE `+match+roleCond+`
		ORDER BY `+order+`
		LIMIT ?`+fmt.Sprint(n+1)+` OFFSET ?`+fmt.Sprint(n+2),
		append(args, p.Limit, p.Offset)...)
	if err != nil {
		return UserPage{}, fmt.Errorf("list users: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var r UserRow
		u, err := scanUser(rows, &r.Usage.Apps, &r.Usage.MemoryMB, &r.Usage.CPUMilli, &r.Usage.Databases, &r.LastLoginAt)
		if err != nil {
			return UserPage{}, fmt.Errorf("list users: %w", err)
		}
		r.User = u
		p.Items = append(p.Items, r)
	}
	if err := rows.Err(); err != nil {
		return UserPage{}, fmt.Errorf("list users: %w", err)
	}
	return p, nil
}

// prefixed qualifies every column of a comma-separated list.
func prefixed(cols, prefix string) string {
	parts := strings.Split(cols, ",")
	for i, c := range parts {
		parts[i] = prefix + strings.TrimSpace(c)
	}
	return strings.Join(parts, ", ")
}
