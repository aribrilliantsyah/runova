package pgfake

import (
	"context"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5/pgconn"

	"runova/internal/pg"
)

type fakeTable struct {
	pg.Table
	rows [][]*string
}

// Users is an in-memory pg.Users over the databases and roles of its Fake:
// a call connects only with the role's current password to a database that
// role owns, like PostgreSQL with CONNECT revoked from PUBLIC.
type Users struct {
	f *Fake
	// tables: database → tables, created by AddTable.
	tables map[string][]*fakeTable
	// Demo gives every database two sample tables the first time it is used
	// (development backends only).
	Demo bool
	// QueryErr makes Query fail without running.
	QueryErr error
}

var _ pg.Users = (*Users)(nil)

// Users returns the user-connection side of the fake, sharing its roles and databases.
func (f *Fake) Users() *Users {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.users == nil {
		f.users = &Users{f: f, tables: map[string][]*fakeTable{}}
	}
	return f.users
}

// AddTable gives db a table with the columns (every one text, the first the
// primary key) and rows.
func (u *Users) AddTable(db, name string, cols []string, rows [][]string) {
	u.f.mu.Lock()
	defer u.f.mu.Unlock()
	u.addTable(db, name, cols, rows)
}

func (u *Users) addTable(db, name string, cols []string, rows [][]string) {
	t := &fakeTable{Table: pg.Table{Name: name, Columns: []pg.Column{}}}
	for i, c := range cols {
		t.Columns = append(t.Columns, pg.Column{Name: c, Type: "text", Nullable: i > 0, PrimaryKey: i == 0})
	}
	for _, r := range rows {
		row := make([]*string, len(r))
		for i, v := range r {
			if v != "NULL" {
				row[i] = new(v)
			}
		}
		t.rows = append(t.rows, row)
	}
	u.tables[db] = append(u.tables[db], t)
	slices.SortFunc(u.tables[db], func(a, b *fakeTable) int { return strings.Compare(a.Name, b.Name) })
}

func (u *Users) demo(db string) {
	if !u.Demo || u.tables[db] != nil {
		return
	}
	var inv [][]string
	for i := 1; i <= 230; i++ {
		memo := fmt.Sprintf("Tagihan %04d", i)
		paid := "true"
		if i%3 == 0 {
			paid = "false"
		}
		inv = append(inv, []string{strconv.Itoa(i), memo, strconv.Itoa(150000 + i*2500), paid, fmt.Sprintf("2026-10-%02d 09:%02d:00+07", 1+i%28, i%60)})
	}
	u.addTable(db, "invoices", []string{"id", "memo", "amount", "paid", "created_at"}, inv)
	u.addTable(db, "customers", []string{"id", "name", "email"}, [][]string{
		{"1", "PT Sinar Jaya", "finance@sinarjaya.co.id"},
		{"2", "CV Maju Bersama", "NULL"},
		{"3", "Toko Dua Saudara", "admin@duasaudara.id"},
	})
}

// login checks the credentials as PostgreSQL would and records the call.
func (u *Users) login(c pg.Creds, call string) error {
	u.f.record("user %s %s %s", c.Role, c.Database, call)
	r, ok := u.f.roles[c.Role]
	if !ok || r.Password != c.Password {
		return fmt.Errorf("%w: password authentication failed for user %q", pg.ErrConnect, c.Role)
	}
	if u.f.dbs[c.Database] != c.Role {
		return fmt.Errorf("%w: permission denied for database %q", pg.ErrConnect, c.Database)
	}
	u.demo(c.Database)
	return nil
}

func (u *Users) table(db, name string) (*fakeTable, int) {
	for i, t := range u.tables[db] {
		if t.Name == name {
			return t, i
		}
	}
	return nil, -1
}

func undefinedTable(name string) error {
	return &pgconn.PgError{Severity: "ERROR", Code: "42P01", Message: fmt.Sprintf("relation %q does not exist", name)}
}

func (u *Users) Tables(_ context.Context, c pg.Creds) ([]pg.Table, error) {
	u.f.mu.Lock()
	defer u.f.mu.Unlock()
	if err := u.login(c, "tables"); err != nil {
		return nil, err
	}
	out := []pg.Table{}
	for _, t := range u.tables[c.Database] {
		v := t.Table
		v.Rows = int64(len(t.rows))
		v.SizeBytes = 8192 + int64(len(t.rows))*96
		v.Columns = slices.Clone(t.Columns)
		out = append(out, v)
	}
	return out, nil
}

func (t *fakeTable) resultColumns() []pg.ResultColumn {
	cols := make([]pg.ResultColumn, len(t.Columns))
	for i, c := range t.Columns {
		cols[i] = pg.ResultColumn{Name: c.Name, Type: c.Type}
	}
	return cols
}

func (u *Users) Rows(_ context.Context, c pg.Creds, table string, offset, limit int) (pg.Result, error) {
	u.f.mu.Lock()
	defer u.f.mu.Unlock()
	if err := u.login(c, "rows "+table); err != nil {
		return pg.Result{}, err
	}
	t, _ := u.table(c.Database, table)
	if t == nil {
		return pg.Result{}, undefinedTable(table)
	}
	res := pg.Result{Command: "SELECT", Columns: t.resultColumns(), Rows: [][]*string{}}
	if offset < len(t.rows) {
		end := min(len(t.rows), offset+limit)
		res.Rows = slices.Clone(t.rows[offset:end])
		res.Truncated = end < len(t.rows)
	}
	return res, nil
}

// Query understands just enough for the panel's tests: SELECT … FROM <table>
// returns the table, any other SELECT one row, anything else affects one row.
func (u *Users) Query(_ context.Context, c pg.Creds, sql string, write bool) (pg.Result, error) {
	u.f.mu.Lock()
	defer u.f.mu.Unlock()
	mode := "read"
	if write {
		mode = "write"
	}
	if err := u.login(c, "query "+mode+" "+sql); err != nil {
		return pg.Result{}, err
	}
	if u.QueryErr != nil {
		return pg.Result{}, u.QueryErr
	}
	var code []string
	for _, line := range strings.Split(sql, "\n") {
		if !strings.HasPrefix(strings.TrimSpace(line), "--") {
			code = append(code, line)
		}
	}
	words := strings.Fields(strings.ToLower(strings.Join(code, "\n")))
	if len(words) == 0 {
		return pg.Result{}, &pgconn.PgError{Severity: "ERROR", Code: "42601", Message: "syntax error"}
	}
	cmd := strings.ToUpper(words[0])
	switch cmd {
	case "SELECT", "WITH", "EXPLAIN", "SHOW":
		for i, w := range words {
			if w == "from" && i+1 < len(words) {
				name := strings.Trim(words[i+1], `";`)
				t, _ := u.table(c.Database, name)
				if t == nil {
					return pg.Result{}, undefinedTable(name)
				}
				res := pg.Result{Command: "SELECT", Columns: t.resultColumns(), Rows: slices.Clone(t.rows)}
				if len(res.Rows) > pg.MaxResultRows {
					res.Rows, res.Truncated = res.Rows[:pg.MaxResultRows], true
				}
				return res, nil
			}
		}
		return pg.Result{Command: cmd, Columns: []pg.ResultColumn{{Name: "?column?", Type: "integer"}}, Rows: [][]*string{{new("1")}}}, nil
	}
	if !write {
		return pg.Result{}, &pgconn.PgError{Severity: "ERROR", Code: "25006", Message: "cannot execute " + cmd + " in a read-only transaction"}
	}
	return pg.Result{Command: cmd, Columns: []pg.ResultColumn{}, Rows: [][]*string{}, Affected: 1}, nil
}

func (u *Users) Truncate(_ context.Context, c pg.Creds, table string) error {
	u.f.mu.Lock()
	defer u.f.mu.Unlock()
	if err := u.login(c, "truncate "+table); err != nil {
		return err
	}
	t, _ := u.table(c.Database, table)
	if t == nil {
		return undefinedTable(table)
	}
	t.rows = nil
	return nil
}

func (u *Users) DropTable(_ context.Context, c pg.Creds, table string) error {
	u.f.mu.Lock()
	defer u.f.mu.Unlock()
	if err := u.login(c, "drop "+table); err != nil {
		return err
	}
	_, i := u.table(c.Database, table)
	if i < 0 {
		return undefinedTable(table)
	}
	u.tables[c.Database] = slices.Delete(u.tables[c.Database], i, i+1)
	return nil
}

func (u *Users) ClientEnv(c pg.Creds) []string {
	return []string{"PGUSER=" + c.Role, "PGPASSWORD=" + c.Password, "PGDATABASE=" + c.Database}
}
