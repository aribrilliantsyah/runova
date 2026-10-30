// Package pgfake provides an in-memory pg.Admin for tests of the packages
// that sit on top of internal/pg.
package pgfake

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"sync"

	"runova/internal/pg"
)

// Role is a role as the fake remembers it.
type Role struct {
	Password string
	ConnLim  int
}

// Fake records every call in order (Calls) and can be made to fail through
// its exported error fields. All methods are safe for concurrent use.
type Fake struct {
	mu    sync.Mutex
	calls []string
	roles map[string]Role
	dbs   map[string]string // database → owner

	// PingErr and the other *Err fields make the matching call fail without changing state.
	PingErr, EnsureRoleErr, SetConnLimitErr, SetPasswordErr, DropRoleErr      error
	CreateDatabaseErr, DropDatabaseErr, DatabasesErr, TerminateErr, ReloadErr error
}

var _ pg.Admin = (*Fake)(nil)

func New() *Fake { return &Fake{roles: map[string]Role{}, dbs: map[string]string{}} }

func (f *Fake) record(format string, a ...any) {
	f.calls = append(f.calls, fmt.Sprintf(format, a...))
}

// Calls returns the recorded calls: "ping", "ensure <role>", "limit <role> <n>",
// "password <role>", "droprole <role>", "create <db> <owner>", "drop <db>",
// "list <owner>", "terminate <db>", "reload".
func (f *Fake) Calls() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.calls)
}

// CallsWithPrefix returns the recorded calls that start with prefix.
func (f *Fake) CallsWithPrefix(prefix string) []string {
	var out []string
	for _, c := range f.Calls() {
		if strings.HasPrefix(c, prefix) {
			out = append(out, c)
		}
	}
	return out
}

// Role returns the role the fake holds.
func (f *Fake) Role(name string) (Role, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	r, ok := f.roles[name]
	return r, ok
}

// HasDatabase reports whether the fake holds the database.
func (f *Fake) HasDatabase(name string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	_, ok := f.dbs[name]
	return ok
}

// SetFailures changes the error fields under the lock, for tests that flip them while handlers run.
func (f *Fake) SetFailures(fn func(f *Fake)) {
	f.mu.Lock()
	defer f.mu.Unlock()
	fn(f)
}

func (f *Fake) Ping(context.Context) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.record("ping")
	return f.PingErr
}

func (f *Fake) EnsureRole(_ context.Context, role, password string, connLimit int) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.record("ensure %s", role)
	if f.EnsureRoleErr != nil {
		return f.EnsureRoleErr
	}
	f.roles[role] = Role{Password: password, ConnLim: connLimit}
	return nil
}

func (f *Fake) SetConnLimit(_ context.Context, role string, n int) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.record("limit %s %d", role, n)
	if f.SetConnLimitErr != nil {
		return f.SetConnLimitErr
	}
	r := f.roles[role]
	r.ConnLim = n
	f.roles[role] = r
	return nil
}

func (f *Fake) SetPassword(_ context.Context, role, password string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.record("password %s", role)
	if f.SetPasswordErr != nil {
		return f.SetPasswordErr
	}
	r := f.roles[role]
	r.Password = password
	f.roles[role] = r
	return nil
}

func (f *Fake) DropRole(_ context.Context, role string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.record("droprole %s", role)
	if f.DropRoleErr != nil {
		return f.DropRoleErr
	}
	for db, owner := range f.dbs {
		if owner == role {
			delete(f.dbs, db)
		}
	}
	delete(f.roles, role)
	return nil
}

func (f *Fake) CreateDatabase(_ context.Context, name, owner string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.record("create %s %s", name, owner)
	if f.CreateDatabaseErr != nil {
		return f.CreateDatabaseErr
	}
	if _, ok := f.dbs[name]; ok {
		return fmt.Errorf("database %q already exists", name)
	}
	f.dbs[name] = owner
	return nil
}

func (f *Fake) DropDatabase(_ context.Context, name string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.record("drop %s", name)
	if f.DropDatabaseErr != nil {
		return f.DropDatabaseErr
	}
	delete(f.dbs, name)
	return nil
}

// Databases reports every database of the owner with size 1000 bytes and 1 connection.
func (f *Fake) Databases(_ context.Context, owner string) ([]pg.DBInfo, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.record("list %s", owner)
	if f.DatabasesErr != nil {
		return nil, f.DatabasesErr
	}
	out := []pg.DBInfo{}
	for db, o := range f.dbs {
		if o == owner {
			out = append(out, pg.DBInfo{Name: db, SizeBytes: 1000, Connections: 1})
		}
	}
	slices.SortFunc(out, func(a, b pg.DBInfo) int { return strings.Compare(a.Name, b.Name) })
	return out, nil
}

func (f *Fake) TerminateConnections(_ context.Context, db string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.record("terminate %s", db)
	return f.TerminateErr
}

func (f *Fake) ReloadConfig(context.Context) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.record("reload")
	return f.ReloadErr
}
