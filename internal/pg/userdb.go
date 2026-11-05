package pg

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgproto3"
)

const (
	// MaxResultRows is the most rows a query returns to the panel.
	MaxResultRows = 1000
	// MaxResultBytes bounds the JSON size of the returned rows.
	MaxResultBytes = 5<<20 - 64<<10
	// maxMessageLen bounds one protocol message (one row), so a huge value
	// fails the read instead of filling memory.
	maxMessageLen = 8 << 20
	// exactCountBytes is the largest table (heap size) whose rows are counted
	// exactly; bigger ones show the planner's estimate.
	exactCountBytes = 16 << 20

	defaultReadTimeout  = 10 * time.Second
	defaultWriteTimeout = 30 * time.Second
	connectTimeout      = 5 * time.Second
	applicationName     = "runova-panel"
	loopbackHost        = "127.0.0.1"
)

// ErrConnect wraps a failure to open the user's connection: wrong password,
// no CONNECT privilege, connection limit reached or PostgreSQL down.
var ErrConnect = errors.New("pg: cannot connect as user")

// Creds is a user's own login to one of their databases.
type Creds struct {
	Role, Password, Database string
}

// Column is one column of a table.
type Column struct {
	Name       string `json:"name"`
	Type       string `json:"type"`
	Nullable   bool   `json:"nullable"`
	Default    string `json:"default,omitempty"`
	PrimaryKey bool   `json:"primary_key"`
}

// Table is one table of the public schema.
type Table struct {
	Name          string   `json:"name"`
	Rows          int64    `json:"rows"`
	RowsEstimated bool     `json:"rows_estimated"`
	SizeBytes     int64    `json:"size_bytes"`
	Columns       []Column `json:"columns"`
}

// ResultColumn names a result column and its SQL type.
type ResultColumn struct {
	Name string `json:"name"`
	Type string `json:"type"`
}

// Result is what one statement returned. Values are PostgreSQL's text
// output; nil is NULL.
type Result struct {
	Command   string         `json:"command"`
	Columns   []ResultColumn `json:"columns"`
	Rows      [][]*string    `json:"rows"`
	Affected  int64          `json:"affected"`
	Truncated bool           `json:"truncated"`
}

// Users runs statements in a user's database over a connection made with
// that user's own role, never through Runova's admin role.
type Users interface {
	Tables(ctx context.Context, c Creds) ([]Table, error)
	// Rows pages through a table (offset, at most limit rows); Truncated
	// reports that more rows follow.
	Rows(ctx context.Context, c Creds, table string, offset, limit int) (Result, error)
	// Query runs one statement: in a read-only transaction that is rolled
	// back, or with write set, in a transaction that is committed.
	Query(ctx context.Context, c Creds, sql string, write bool) (Result, error)
	Truncate(ctx context.Context, c Creds, table string) error
	DropTable(ctx context.Context, c Creds, table string) error
	// ClientEnv is the libpq environment for pg_dump and pg_restore as the user.
	ClientEnv(c Creds) []string
}

// PgxUsers implements Users with one fresh pgx connection per call, closed
// when the call ends.
type PgxUsers struct {
	Host string
	Port uint16
	// ReadTimeout and WriteTimeout are the statement_timeout of read and write
	// mode; zero means 10 and 30 seconds.
	ReadTimeout, WriteTimeout time.Duration
}

var _ Users = (*PgxUsers)(nil)

// NewUsers connects users to the server of the admin DSN. A Unix socket DSN
// (peer authentication, which cannot log in as another role) becomes TCP on
// 127.0.0.1 with the DSN's port, where users authenticate with scram-sha-256.
func NewUsers(adminDSN string) (*PgxUsers, error) {
	cfg, err := pgx.ParseConfig(adminDSN)
	if err != nil {
		return nil, fmt.Errorf("parse postgres dsn: %w", err)
	}
	u := &PgxUsers{Host: cfg.Host, Port: cfg.Port}
	if strings.HasPrefix(u.Host, "/") || u.Host == "" {
		u.Host = loopbackHost
	}
	if u.Port == 0 {
		u.Port = 5432
	}
	return u, nil
}

func (u *PgxUsers) timeout(write bool) time.Duration {
	if write {
		if u.WriteTimeout > 0 {
			return u.WriteTimeout
		}
		return defaultWriteTimeout
	}
	if u.ReadTimeout > 0 {
		return u.ReadTimeout
	}
	return defaultReadTimeout
}

func (u *PgxUsers) ClientEnv(c Creds) []string {
	return []string{
		"PGHOST=" + u.Host, "PGPORT=" + strconv.Itoa(int(u.Port)),
		"PGUSER=" + c.Role, "PGPASSWORD=" + c.Password, "PGDATABASE=" + c.Database,
		"PGAPPNAME=" + applicationName, "PGCONNECT_TIMEOUT=5", "PGSSLMODE=prefer",
	}
}

// connect opens a connection as the user with the mode's statement_timeout
// and, unless write is set, read-only transactions as the session default.
func (u *PgxUsers) connect(ctx context.Context, c Creds, write bool) (*pgx.Conn, error) {
	if err := validateRole(c.Role); err != nil {
		return nil, err
	}
	if err := validatePassword(c.Password); err != nil {
		return nil, err
	}
	cfg, err := pgx.ParseConfig("")
	if err != nil {
		return nil, err
	}
	cfg.Host, cfg.Port = u.Host, u.Port
	cfg.User, cfg.Password, cfg.Database = c.Role, c.Password, c.Database
	cfg.ConnectTimeout = connectTimeout
	cfg.Fallbacks = nil
	cfg.RuntimeParams = map[string]string{
		"application_name":                    applicationName,
		"statement_timeout":                   strconv.FormatInt(u.timeout(write).Milliseconds(), 10),
		"lock_timeout":                        strconv.FormatInt(u.timeout(write).Milliseconds(), 10),
		"idle_in_transaction_session_timeout": "60000",
		"search_path":                         "public",
	}
	if !write {
		cfg.RuntimeParams["default_transaction_read_only"] = "on"
	}
	cfg.DefaultQueryExecMode = pgx.QueryExecModeExec
	conn, err := pgx.ConnectConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrConnect, err)
	}
	conn.PgConn().Frontend().SetMaxBodyLen(maxMessageLen)
	return conn, nil
}

func closeConn(conn *pgx.Conn) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	conn.Close(ctx)
}

// begin starts the transaction the statement runs in.
func begin(ctx context.Context, conn *pgx.Conn, write bool) error {
	mode := "BEGIN READ ONLY"
	if write {
		mode = "BEGIN READ WRITE"
	}
	_, err := conn.Exec(ctx, mode)
	return err
}

func (u *PgxUsers) Query(ctx context.Context, c Creds, sql string, write bool) (Result, error) {
	conn, err := u.connect(ctx, c, write)
	if err != nil {
		return Result{}, err
	}
	defer closeConn(conn)
	if err := begin(ctx, conn, write); err != nil {
		return Result{}, err
	}
	res, types, err := execOne(ctx, conn.PgConn(), sql)
	if err != nil {
		return Result{}, err
	}
	end := "ROLLBACK"
	if write {
		end = "COMMIT"
	}
	if _, err := conn.Exec(ctx, end); err != nil {
		return Result{}, err
	}
	if err := describe(ctx, conn, &res, types); err != nil {
		return Result{}, err
	}
	return res, nil
}

// colType is a result column's type identity until describe names it.
type colType struct {
	oid uint32
	mod int32
}

// execOne runs one statement with the extended protocol: an unnamed Parse
// refuses text with more than one statement, and Execute fetches at most
// MaxResultRows+1 rows, so a SELECT without LIMIT stops on the server instead
// of running to the end (PortalSuspended → Truncated). DML with RETURNING
// still runs completely; only its output is cut. Rows that would push the
// JSON past MaxResultBytes are dropped and Truncated set.
func execOne(ctx context.Context, pc *pgconn.PgConn, sql string) (Result, []colType, error) {
	fe := pc.Frontend()
	fe.SendParse(&pgproto3.Parse{Query: sql})
	fe.SendBind(&pgproto3.Bind{})
	fe.SendDescribe(&pgproto3.Describe{ObjectType: 'P'})
	fe.SendExecute(&pgproto3.Execute{MaxRows: MaxResultRows + 1})
	fe.SendSync(&pgproto3.Sync{})
	if err := fe.Flush(); err != nil {
		return Result{}, nil, fmt.Errorf("send query: %w", err)
	}

	res := Result{Columns: []ResultColumn{}, Rows: [][]*string{}}
	var types []colType
	var pgErr error
	size := 0
	for {
		msg, err := pc.ReceiveMessage(ctx)
		if err != nil {
			return Result{}, nil, err
		}
		switch m := msg.(type) {
		case *pgproto3.RowDescription:
			for _, f := range m.Fields {
				res.Columns = append(res.Columns, ResultColumn{Name: string(f.Name)})
				types = append(types, colType{f.DataTypeOID, f.TypeModifier})
			}
		case *pgproto3.DataRow:
			if res.Truncated {
				continue
			}
			rowSize := 2
			for _, v := range m.Values {
				rowSize += jsonLen(v) + 1
			}
			if len(res.Rows) == MaxResultRows || size+rowSize > MaxResultBytes {
				res.Truncated = true
				continue
			}
			size += rowSize
			row := make([]*string, len(m.Values))
			for i, v := range m.Values {
				if v != nil {
					row[i] = new(string(v))
				}
			}
			res.Rows = append(res.Rows, row)
		case *pgproto3.PortalSuspended:
			res.Truncated = true
			res.Command = commandOf(sql)
		case *pgproto3.CommandComplete:
			tag := pgconn.NewCommandTag(string(m.CommandTag))
			res.Command = commandName(tag.String())
			res.Affected = tag.RowsAffected()
		case *pgproto3.ErrorResponse:
			pgErr = pgconn.ErrorResponseToPgError(m)
		case *pgproto3.ReadyForQuery:
			if pgErr != nil {
				return Result{}, nil, pgErr
			}
			return res, types, nil
		}
	}
}

// describe names the result's column types with format_type, as psql does.
func describe(ctx context.Context, conn *pgx.Conn, res *Result, types []colType) error {
	if len(types) == 0 {
		return nil
	}
	oids := make([]uint32, len(types))
	mods := make([]int32, len(types))
	for i, t := range types {
		oids[i], mods[i] = t.oid, t.mod
	}
	rows, err := conn.Query(ctx, `SELECT coalesce(format_type(o, nullif(m, -1)), '?') FROM unnest($1::oid[], $2::int4[]) WITH ORDINALITY AS x(o, m, n) ORDER BY n`, oids, mods)
	if err != nil {
		return err
	}
	names, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return err
	}
	for i := range res.Columns {
		if i < len(names) {
			res.Columns[i].Type = names[i]
		}
	}
	return nil
}

// jsonLen is the length of v as a JSON string, with worst-case escapes.
func jsonLen(v []byte) int {
	if v == nil {
		return 4
	}
	n := 2
	for _, b := range v {
		switch {
		case b == '"' || b == '\\':
			n += 2
		case b < 0x20 || b == '<' || b == '>' || b == '&':
			n += 6
		default:
			n++
		}
	}
	return n
}

// commandName turns a command tag ("INSERT 0 3", "SELECT 5") into its verb.
func commandName(tag string) string {
	f := strings.Fields(tag)
	switch {
	case len(f) == 0:
		return ""
	case len(f) >= 2 && (f[0] == "CREATE" || f[0] == "ALTER" || f[0] == "DROP") && !isNumber(f[1]):
		return f[0] + " " + f[1]
	}
	return f[0]
}

func isNumber(s string) bool {
	_, err := strconv.ParseInt(s, 10, 64)
	return err == nil
}

// commandOf is the first word of sql, for a portal suspended before its tag.
func commandOf(sql string) string {
	f := strings.Fields(strings.TrimLeft(sql, "( \t\r\n"))
	if len(f) == 0 {
		return ""
	}
	return strings.ToUpper(f[0])
}

func tableIdent(table string) (string, error) {
	if table == "" || strings.ContainsRune(table, 0) || len(table) > 63 {
		return "", fmt.Errorf("%w: table name %q", ErrInvalid, table)
	}
	return pgx.Identifier{"public", table}.Sanitize(), nil
}

func (u *PgxUsers) Tables(ctx context.Context, c Creds) ([]Table, error) {
	conn, err := u.connect(ctx, c, false)
	if err != nil {
		return nil, err
	}
	defer closeConn(conn)
	rows, err := conn.Query(ctx, `
SELECT c.relname, c.reltuples::int8, pg_total_relation_size(c.oid), pg_relation_size(c.oid)
FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace
WHERE n.nspname = 'public' AND c.relkind IN ('r', 'p')
ORDER BY c.relname COLLATE "C"`)
	if err != nil {
		return nil, err
	}
	type rel struct {
		Table
		heap int64
	}
	rels, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (rel, error) {
		var t rel
		err := r.Scan(&t.Name, &t.Rows, &t.SizeBytes, &t.heap)
		return t, err
	})
	if err != nil {
		return nil, err
	}
	out := make([]Table, 0, len(rels))
	for _, r := range rels {
		t := r.Table
		if r.heap <= exactCountBytes || t.Rows < 0 {
			id, err := tableIdent(t.Name)
			if err != nil {
				return nil, err
			}
			if err := conn.QueryRow(ctx, "SELECT count(*) FROM "+id).Scan(&t.Rows); err != nil {
				return nil, fmt.Errorf("count %s: %w", t.Name, err)
			}
		} else {
			t.RowsEstimated = true
		}
		if t.Columns, err = columns(ctx, conn, t.Name); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, nil
}

func columns(ctx context.Context, conn *pgx.Conn, table string) ([]Column, error) {
	rows, err := conn.Query(ctx, `
SELECT a.attname, format_type(a.atttypid, a.atttypmod), NOT a.attnotnull,
       coalesce(pg_get_expr(d.adbin, d.adrelid), ''),
       EXISTS (SELECT 1 FROM pg_index i WHERE i.indrelid = a.attrelid AND i.indisprimary AND a.attnum = ANY (i.indkey))
FROM pg_attribute a
JOIN pg_class c ON c.oid = a.attrelid
JOIN pg_namespace n ON n.oid = c.relnamespace
LEFT JOIN pg_attrdef d ON d.adrelid = a.attrelid AND d.adnum = a.attnum
WHERE n.nspname = 'public' AND c.relname = $1 AND a.attnum > 0 AND NOT a.attisdropped
ORDER BY a.attnum`, table)
	if err != nil {
		return nil, err
	}
	cols, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (Column, error) {
		var c Column
		err := r.Scan(&c.Name, &c.Type, &c.Nullable, &c.Default, &c.PrimaryKey)
		return c, err
	})
	if cols == nil {
		cols = []Column{}
	}
	return cols, err
}

func (u *PgxUsers) Rows(ctx context.Context, c Creds, table string, offset, limit int) (Result, error) {
	id, err := tableIdent(table)
	if err != nil {
		return Result{}, err
	}
	if offset < 0 || limit < 1 || limit > MaxResultRows {
		return Result{}, fmt.Errorf("%w: offset %d limit %d", ErrInvalid, offset, limit)
	}
	conn, err := u.connect(ctx, c, false)
	if err != nil {
		return Result{}, err
	}
	defer closeConn(conn)
	cols, err := columns(ctx, conn, table)
	if err != nil {
		return Result{}, err
	}
	var order []string
	for _, col := range cols {
		if col.PrimaryKey {
			order = append(order, pgx.Identifier{col.Name}.Sanitize())
		}
	}
	if len(order) == 0 {
		order = []string{"ctid"}
	}
	if err := begin(ctx, conn, false); err != nil {
		return Result{}, err
	}
	sql := fmt.Sprintf("SELECT * FROM %s ORDER BY %s OFFSET %d LIMIT %d", id, strings.Join(order, ", "), offset, limit+1)
	res, types, err := execOne(ctx, conn.PgConn(), sql)
	if err != nil {
		return Result{}, err
	}
	if _, err := conn.Exec(ctx, "ROLLBACK"); err != nil {
		return Result{}, err
	}
	res.Truncated = res.Truncated || len(res.Rows) > limit
	if len(res.Rows) > limit {
		res.Rows = res.Rows[:limit]
	}
	if err := describe(ctx, conn, &res, types); err != nil {
		return Result{}, err
	}
	return res, nil
}

func (u *PgxUsers) Truncate(ctx context.Context, c Creds, table string) error {
	return u.ddl(ctx, c, table, "TRUNCATE TABLE %s")
}

func (u *PgxUsers) DropTable(ctx context.Context, c Creds, table string) error {
	return u.ddl(ctx, c, table, "DROP TABLE %s")
}

func (u *PgxUsers) ddl(ctx context.Context, c Creds, table, format string) error {
	id, err := tableIdent(table)
	if err != nil {
		return err
	}
	conn, err := u.connect(ctx, c, true)
	if err != nil {
		return err
	}
	defer closeConn(conn)
	_, err = conn.Exec(ctx, fmt.Sprintf(format, id))
	return err
}
