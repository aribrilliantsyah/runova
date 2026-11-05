package dbs

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5/pgconn"

	"runova/internal/pg"
	"runova/internal/sqlguard"
	"runova/internal/store"
)

const (
	// MaxPageRows is the page size limit of the table browser.
	MaxPageRows = 100
	dumpMagic   = "PGDMP"
	uploadGlob  = ".upload-*.dump"
)

var (
	// ErrTableNotFound is ErrNotFound for a table the database does not have.
	ErrTableNotFound = &userError{ErrNotFound, "Tabel tidak ditemukan"}
	// ErrTooLarge marks an upload over the size limit.
	ErrTooLarge = errors.New("upload too large")
	// ErrExportBroken marks an export whose dump failed after bytes were sent.
	ErrExportBroken = errors.New("export broken off")
)

// creds returns the owner's login to the database, for a connection with the
// owner's own role.
func (s *Service) creds(ctx context.Context, u store.User, d store.Database) (pg.Creds, error) {
	password, err := s.rolePassword(ctx, u.ID)
	if err != nil {
		return pg.Creds{}, err
	}
	return pg.Creds{Role: u.Username, Password: password, Database: d.Name}, nil
}

// session resolves the user's database and their login to it.
func (s *Service) session(ctx context.Context, u store.User, dbID int64) (store.Database, pg.Creds, error) {
	d, err := s.owned(ctx, u, dbID)
	if err != nil {
		return store.Database{}, pg.Creds{}, err
	}
	c, err := s.creds(ctx, u, d)
	return d, c, err
}

// userFail classifies a failed call on the user's connection like pgFail,
// and keeps PostgreSQL's own message for errors the user caused.
func (s *Service) userFail(ctx context.Context, what string, err error) error {
	if pe, ok := errors.AsType[*pgconn.PgError](err); ok {
		return invalid("%s", pgMessage(pe))
	}
	if errors.Is(err, pg.ErrConnect) {
		if perr := s.PG.Ping(context.WithoutCancel(ctx)); perr != nil {
			return ErrPGDown
		}
		slog.Warn("connect as user", "op", what, "err", err)
		return &userError{ErrPG, "Tidak bisa masuk ke database dengan akun pengguna: " + connectReason(err)}
	}
	return s.pgFail(ctx, what, err)
}

func connectReason(err error) string {
	if pe, ok := errors.AsType[*pgconn.PgError](err); ok {
		return pe.Message
	}
	return err.Error()
}

// Tables lists the tables of the database's public schema with their columns.
func (s *Service) Tables(ctx context.Context, u store.User, dbID int64) ([]pg.Table, error) {
	_, c, err := s.session(ctx, u, dbID)
	if err != nil {
		return nil, err
	}
	tables, err := s.Users.Tables(ctx, c)
	if err != nil {
		return nil, s.userFail(ctx, "list tables of "+c.Database, err)
	}
	return tables, nil
}

// knownTable resolves the user's database and checks that table is one of its
// tables, so only names PostgreSQL itself listed ever reach a statement.
func (s *Service) knownTable(ctx context.Context, u store.User, dbID int64, table string) (pg.Creds, error) {
	_, c, err := s.session(ctx, u, dbID)
	if err != nil {
		return pg.Creds{}, err
	}
	tables, err := s.Users.Tables(ctx, c)
	if err != nil {
		return pg.Creds{}, s.userFail(ctx, "list tables of "+c.Database, err)
	}
	if !slices.ContainsFunc(tables, func(t pg.Table) bool { return t.Name == table }) {
		return pg.Creds{}, ErrTableNotFound
	}
	return c, nil
}

// TableRows returns up to limit rows of a table starting at offset.
func (s *Service) TableRows(ctx context.Context, u store.User, dbID int64, table string, offset, limit int) (pg.Result, error) {
	if _, err := s.owned(ctx, u, dbID); err != nil {
		return pg.Result{}, err
	}
	if offset < 0 || limit < 1 || limit > MaxPageRows {
		return pg.Result{}, invalid("Halaman tidak valid (offset ≥ 0, limit 1–%d)", MaxPageRows)
	}
	c, err := s.knownTable(ctx, u, dbID, table)
	if err != nil {
		return pg.Result{}, err
	}
	res, err := s.Users.Rows(ctx, c, table, offset, limit)
	if err != nil {
		return pg.Result{}, s.userFail(ctx, "read "+table, err)
	}
	return res, nil
}

// TruncateTable empties a table after the owner typed its name.
func (s *Service) TruncateTable(ctx context.Context, u store.User, dbID int64, table, confirm string) error {
	c, err := s.knownTable(ctx, u, dbID, table)
	if err != nil {
		return err
	}
	if confirm != table {
		return invalid("Ketik nama tabel untuk konfirmasi")
	}
	if err := s.Users.Truncate(ctx, c, table); err != nil {
		return s.userFail(ctx, "truncate "+table, err)
	}
	return nil
}

// DropTable drops a table after the owner typed its name.
func (s *Service) DropTable(ctx context.Context, u store.User, dbID int64, table, confirm string) error {
	c, err := s.knownTable(ctx, u, dbID, table)
	if err != nil {
		return err
	}
	if confirm != table {
		return invalid("Ketik nama tabel untuk konfirmasi")
	}
	if err := s.Users.DropTable(ctx, c, table); err != nil {
		return s.userFail(ctx, "drop "+table, err)
	}
	return nil
}

// QueryResult is the answer of the query editor. Error carries what the
// panel refused or PostgreSQL answered; the call itself still succeeded.
type QueryResult struct {
	Command    string            `json:"command"`
	Columns    []pg.ResultColumn `json:"columns"`
	Rows       [][]*string       `json:"rows"`
	Affected   int64             `json:"affected"`
	DurationMS int64             `json:"duration_ms"`
	Truncated  bool              `json:"truncated"`
	Error      string            `json:"error,omitempty"`
	// ErrorLine is the 1-based line of the query PostgreSQL pointed at; 0 when none.
	ErrorLine int `json:"error_line,omitempty"`
}

// Query runs one statement from the query editor as the database owner's
// role. Read mode runs it in a read-only transaction that is rolled back;
// write mode commits. The panel's whitelist refuses anything else before
// PostgreSQL is reached.
func (s *Service) Query(ctx context.Context, u store.User, dbID int64, sql string, write bool) (QueryResult, error) {
	_, c, err := s.session(ctx, u, dbID)
	if err != nil {
		return QueryResult{}, err
	}
	empty := QueryResult{Columns: []pg.ResultColumn{}, Rows: [][]*string{}}
	st, err := sqlguard.Check(sql, write)
	if err != nil {
		empty.Error = err.Error()
		return empty, nil
	}
	empty.Command = st.Command
	start := time.Now()
	res, err := s.Users.Query(ctx, c, st.SQL, write)
	elapsed := time.Since(start).Milliseconds()
	if err != nil {
		pe, ok := errors.AsType[*pgconn.PgError](err)
		if !ok {
			return QueryResult{}, s.userFail(ctx, "query "+c.Database, err)
		}
		empty.DurationMS = elapsed
		empty.Error = queryError(pe, write)
		empty.ErrorLine = errorLine(sql, st, pe.Position)
		return empty, nil
	}
	out := QueryResult{
		Command: res.Command, Columns: res.Columns, Rows: res.Rows, Affected: res.Affected,
		DurationMS: elapsed, Truncated: res.Truncated,
	}
	if !st.Rows && len(out.Columns) == 0 {
		out.Rows = [][]*string{}
	}
	return out, nil
}

func pgMessage(pe *pgconn.PgError) string {
	msg := pe.Message
	if pe.Detail != "" {
		msg += "\n" + pe.Detail
	}
	if pe.Hint != "" {
		msg += "\nPetunjuk: " + pe.Hint
	}
	return msg
}

func queryError(pe *pgconn.PgError, write bool) string {
	switch pe.Code {
	case "57014":
		limit, mode := pg.DefaultReadTimeout, "Baca"
		if write {
			limit, mode = pg.DefaultWriteTimeout, "Tulis"
		}
		return fmt.Sprintf("Query dihentikan setelah %d detik (batas waktu mode %s)", int(limit.Seconds()), mode)
	case "25006":
		return pgMessage(pe) + "\nMode Baca tidak mengubah data; pilih mode Tulis."
	}
	return fmt.Sprintf("%s %s: %s", pe.Severity, pe.Code, pgMessage(pe))
}

// errorLine turns PostgreSQL's 1-based character position in the statement
// sent into a line of the text the user typed.
func errorLine(sql string, st sqlguard.Statement, pos int32) int {
	if pos <= 0 {
		return 0
	}
	at := st.Offset
	for i := int32(1); i < pos && at < len(sql); i++ {
		_, size := utf8.DecodeRuneInString(sql[at:])
		at += size
	}
	return strings.Count(sql[:at], "\n") + 1
}

// userCommand runs a client binary logged in as the user's role: the libpq
// variables of the process are dropped so nothing of Runova's own login leaks in.
func (s *Service) userCommand(ctx context.Context, c pg.Creds, bin string, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, bin, args...)
	env := slices.DeleteFunc(os.Environ(), func(kv string) bool { return strings.HasPrefix(kv, "PG") })
	cmd.Env = append(env, s.Users.ClientEnv(c)...)
	return cmd
}

// restoreFile loads a dump into the database with the owner's role. Other
// connections are ended first. The dump's statements run with the user's
// privileges only, so a crafted archive cannot reach Runova's role.
func (s *Service) restoreFile(ctx context.Context, d store.Database, c pg.Creds, path string) error {
	defer s.dbLock(d.Name)()
	if err := s.PG.TerminateConnections(ctx, d.Name); err != nil {
		return s.pgFail(ctx, "terminate connections of "+d.Name, err)
	}
	var stderr limitedBuffer
	cmd := s.userCommand(ctx, c, s.restoreBin(), "--clean", "--if-exists", "--no-owner", "--role="+c.Role, "-d", d.Name, path)
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return &ToolError{Tool: "pg_restore", Stderr: stderr.String(), Err: err}
	}
	return nil
}

// RestoreUpload restores an uploaded pg_dump custom-format archive (at most
// limit bytes) into one of the user's databases after the owner typed its
// name. The upload goes to a temporary file with a generated name, removed
// afterwards; the client's file name is never used.
func (s *Service) RestoreUpload(ctx context.Context, u store.User, dbID int64, confirm string, r io.Reader, limit int64) error {
	d, c, err := s.session(ctx, u, dbID)
	if err != nil {
		return err
	}
	if confirm != d.Name {
		return invalid("Ketik nama database untuk konfirmasi")
	}
	if err := os.MkdirAll(s.BackupDir, 0o700); err != nil {
		return fmt.Errorf("create backup folder: %w", err)
	}
	tmp, err := os.CreateTemp(s.BackupDir, uploadGlob)
	if err != nil {
		return fmt.Errorf("create upload file: %w", err)
	}
	defer os.Remove(tmp.Name())
	n, err := io.Copy(tmp, io.LimitReader(r, limit+1))
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	switch {
	case err != nil:
		return fmt.Errorf("receive dump: %w", err)
	case n > limit:
		return &userError{ErrTooLarge, fmt.Sprintf("File melebihi batas %d MB", limit>>20)}
	}
	if err := checkDump(tmp.Name()); err != nil {
		return err
	}
	return s.restoreFile(ctx, d, c, tmp.Name())
}

// checkDump accepts only pg_dump's custom format, which pg_restore reads;
// a plain SQL script would need psql.
func checkDump(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	head := make([]byte, len(dumpMagic))
	if _, err := io.ReadFull(f, head); err != nil || string(head) != dumpMagic {
		return invalid("File bukan dump format custom (pg_dump -Fc). Buat dengan: pg_dump -Fc -d <database> -f data.dump")
	}
	return nil
}

// lazyWriter opens the destination on the first byte, so a dump that fails
// at once can still be answered with an error instead of a broken download.
type lazyWriter struct {
	open func() io.Writer
	w    io.Writer
}

func (l *lazyWriter) Write(p []byte) (int, error) {
	if l.w == nil {
		l.w = l.open()
	}
	return l.w.Write(p)
}

// Export streams a fresh pg_dump -Fc of the database, made with the owner's
// role, into the writer open returns; open gets the download's file name and
// is called only once the dump produced its first bytes. When it fails after
// that, the error wraps ErrExportBroken.
func (s *Service) Export(ctx context.Context, u store.User, dbID int64, open func(name string) io.Writer) (string, error) {
	d, c, err := s.session(ctx, u, dbID)
	if err != nil {
		return "", err
	}
	name := d.Name + "-" + s.now().Format(backupLayout) + backupExt
	out := &lazyWriter{open: func() io.Writer { return open(name) }}
	var stderr limitedBuffer
	cmd := s.userCommand(ctx, c, s.dumpBin(), "-Fc", "--no-owner", "-d", d.Name)
	cmd.Stdout, cmd.Stderr = out, &stderr
	if err := cmd.Run(); err != nil {
		te := &ToolError{Tool: "pg_dump", Stderr: stderr.String(), Err: err}
		if out.w != nil {
			return name, fmt.Errorf("%w: %w", ErrExportBroken, te)
		}
		return name, te
	}
	if out.w == nil {
		out.Write(nil)
	}
	return name, nil
}
