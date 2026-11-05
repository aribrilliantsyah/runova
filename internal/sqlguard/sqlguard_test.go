package sqlguard_test

import (
	"errors"
	"strings"
	"testing"

	"runova/internal/sqlguard"
)

func TestReadModeAllows(t *testing.T) {
	for _, tc := range []struct {
		sql, command string
		rows         bool
	}{
		{"SELECT 1", "SELECT", true},
		{"select * from invoices", "SELECT", true},
		{"  \n\tSELECT 1", "SELECT", true},
		{"-- note\nSELECT 1", "SELECT", true},
		{"/* a /* nested */ comment */ SELECT 1", "SELECT", true},
		{"WITH t AS (SELECT 1) SELECT * FROM t", "WITH", true},
		{"(SELECT 1) UNION (SELECT 2)", "SELECT", true},
		{"EXPLAIN SELECT 1", "EXPLAIN", true},
		{"explain analyze select 1", "EXPLAIN", true},
		{"SHOW search_path", "SHOW", true},
		{"SELECT 1;", "SELECT", true},
		{"SELECT 1 ;  -- done\n", "SELECT", true},
		{"SELECT 1;;", "SELECT", true},
	} {
		st, err := sqlguard.Check(tc.sql, false)
		if err != nil {
			t.Errorf("%q: %v", tc.sql, err)
			continue
		}
		if st.Command != tc.command || st.Rows != tc.rows {
			t.Errorf("%q: command %q rows %v, want %q %v", tc.sql, st.Command, st.Rows, tc.command, tc.rows)
		}
	}
}

func TestReadModeRejectsWrites(t *testing.T) {
	for _, sql := range []string{
		"INSERT INTO t VALUES (1)",
		"UPDATE t SET a = 1",
		"DELETE FROM t",
		"TRUNCATE t",
		"CREATE TABLE t (id int)",
		"ALTER TABLE t ADD COLUMN b int",
		"DROP TABLE t",
	} {
		_, err := sqlguard.Check(sql, false)
		if !errors.Is(err, sqlguard.ErrNeedsWrite) {
			t.Errorf("%q: err = %v, want ErrNeedsWrite", sql, err)
		}
	}
}

func TestExplainFollowsTheExplainedStatement(t *testing.T) {
	for _, sql := range []string{"EXPLAIN ANALYZE DELETE FROM t", "EXPLAIN (ANALYZE, BUFFERS) INSERT INTO t VALUES (1)"} {
		if _, err := sqlguard.Check(sql, false); !errors.Is(err, sqlguard.ErrNeedsWrite) {
			t.Errorf("%q read: err = %v, want ErrNeedsWrite", sql, err)
		}
		if _, err := sqlguard.Check(sql, true); err != nil {
			t.Errorf("%q write: %v", sql, err)
		}
	}
	for _, sql := range []string{"EXPLAIN COPY t TO '/tmp/x'", "EXPLAIN (FORMAT JSON) CREATE FUNCTION f() RETURNS int AS 'x'", "EXPLAIN EXPLAIN SELECT 1"} {
		if _, err := sqlguard.Check(sql, true); !errors.Is(err, sqlguard.ErrNotAllowed) {
			t.Errorf("%q: err = %v, want ErrNotAllowed", sql, err)
		}
	}
}

func TestWriteModeAllows(t *testing.T) {
	for _, tc := range []struct {
		sql, command string
		rows         bool
	}{
		{"INSERT INTO t VALUES (1)", "INSERT", false},
		{"insert into t values (1) returning id", "INSERT", false},
		{"UPDATE t SET a = 1 WHERE id = 2", "UPDATE", false},
		{"DELETE FROM t WHERE id = 2", "DELETE", false},
		{"TRUNCATE TABLE t", "TRUNCATE", false},
		{"CREATE TABLE t (id int PRIMARY KEY)", "CREATE", false},
		{"CREATE TABLE IF NOT EXISTS t (id int)", "CREATE", false},
		{"create temp table t (id int)", "CREATE", false},
		{"CREATE UNLOGGED TABLE t (id int)", "CREATE", false},
		{"CREATE TABLE t AS SELECT 1", "CREATE", false},
		{"CREATE INDEX ON t (id)", "CREATE", false},
		{"CREATE UNIQUE INDEX t_id ON t (id)", "CREATE", false},
		{"ALTER TABLE t ADD COLUMN memo text", "ALTER", false},
		{"ALTER INDEX t_id RENAME TO t_id2", "ALTER", false},
		{"DROP TABLE t", "DROP", false},
		{"DROP TABLE IF EXISTS t", "DROP", false},
		{"DROP INDEX t_id", "DROP", false},
		{"SELECT 1", "SELECT", true},
		{"WITH d AS (DELETE FROM t RETURNING *) SELECT count(*) FROM d", "WITH", true},
	} {
		st, err := sqlguard.Check(tc.sql, true)
		if err != nil {
			t.Errorf("%q: %v", tc.sql, err)
			continue
		}
		if st.Command != tc.command || st.Rows != tc.rows {
			t.Errorf("%q: command %q rows %v, want %q %v", tc.sql, st.Command, st.Rows, tc.command, tc.rows)
		}
	}
}

func TestRejectedInBothModes(t *testing.T) {
	for _, sql := range []string{
		"COPY t FROM '/etc/passwd'",
		"COPY t TO '/tmp/x'",
		"COPY (SELECT 1) TO PROGRAM 'id'",
		"SET ROLE runova",
		"SET search_path = x",
		"RESET ROLE",
		"GRANT runova TO budi",
		"REVOKE ALL ON t FROM PUBLIC",
		"REASSIGN OWNED BY budi TO andi",
		"SECURITY LABEL ON TABLE t IS 'x'",
		"COMMENT ON TABLE t IS 'x'",
		"DO $$ BEGIN PERFORM 1; END $$",
		"CALL p()",
		"EXECUTE s",
		"PREPARE s AS SELECT 1",
		"DEALLOCATE s",
		"LISTEN c",
		"NOTIFY c",
		"LOCK TABLE t",
		"VACUUM t",
		"ANALYZE t",
		"CLUSTER t",
		"REINDEX TABLE t",
		"REFRESH MATERIALIZED VIEW v",
		"IMPORT FOREIGN SCHEMA s FROM SERVER x INTO public",
		"LOAD 'plpgsql'",
		"DISCARD ALL",
		"BEGIN",
		"COMMIT",
		"ROLLBACK",
		"START TRANSACTION",
		"SAVEPOINT a",
		"MERGE INTO t USING s ON true WHEN MATCHED THEN DELETE",
		"VALUES (1)",
		"TABLE t",
		"ALTER ROLE budi SUPERUSER",
		"ALTER SYSTEM SET x = 1",
		"ALTER DATABASE budi_app OWNER TO andi",
		"ALTER DEFAULT PRIVILEGES GRANT SELECT ON TABLES TO andi",
		"CREATE FUNCTION f() RETURNS int LANGUAGE sql AS 'SELECT 1'",
		"CREATE OR REPLACE FUNCTION f() RETURNS int LANGUAGE c AS 'x'",
		"CREATE EXTENSION file_fdw",
		"CREATE ROLE x",
		"CREATE DATABASE x",
		"CREATE SCHEMA x",
		"CREATE VIEW v AS SELECT 1",
		"CREATE OR REPLACE VIEW v AS SELECT 1",
		"CREATE TRIGGER tr AFTER INSERT ON t EXECUTE FUNCTION f()",
		"CREATE LANGUAGE plpython3u",
		"CREATE SERVER s FOREIGN DATA WRAPPER w",
		"CREATE FOREIGN TABLE f () SERVER s",
		"CREATE CAST (int AS text) WITH INOUT",
		"DROP FUNCTION f",
		"DROP ROLE andi",
		"DROP DATABASE budi_app",
		"DROP SCHEMA public",
		"DROP OWNED BY budi",
		"CHECKPOINT",
		"FETCH ALL FROM c",
		"DECLARE c CURSOR FOR SELECT 1",
		"\\! id",
		"\\copy t from x",
		"CREATE",
		"ALTER",
		"DROP",
	} {
		for _, write := range []bool{false, true} {
			_, err := sqlguard.Check(sql, write)
			if err == nil {
				t.Errorf("%q (write=%v) was allowed", sql, write)
				continue
			}
			if errors.Is(err, sqlguard.ErrNeedsWrite) {
				t.Errorf("%q (write=%v): ErrNeedsWrite, want a refusal in both modes", sql, write)
			}
		}
	}
}

func TestRejectionNamesTheCommand(t *testing.T) {
	_, err := sqlguard.Check("copy t to '/tmp/x'", true)
	if err == nil || !strings.Contains(err.Error(), "COPY") {
		t.Fatalf("err = %v, want the COPY command named", err)
	}
	_, err = sqlguard.Check("CREATE FUNCTION f() RETURNS int LANGUAGE sql AS 'SELECT 1'", true)
	if err == nil || !strings.Contains(err.Error(), "CREATE FUNCTION") {
		t.Fatalf("err = %v, want CREATE FUNCTION named", err)
	}
}

func TestMultipleStatementsRejected(t *testing.T) {
	for _, sql := range []string{
		"SELECT 1; SELECT 2",
		"SELECT 1;SELECT 2;",
		"SELECT 1; DROP TABLE t",
		"SELECT 1; -- c\nDELETE FROM t",
		"SELECT 1 /* ; */ ; SELECT 2",
		"SELECT 'a'; SELECT 'b'",
		"SELECT $$x$$; SELECT 1",
		"SELECT E'\\''; SELECT 1",
		"SELECT \"a;b\"; SELECT 1",
		"SELECT 1 ;\n\n COMMIT",
		"SELECT 1; ;SELECT 2",
	} {
		_, err := sqlguard.Check(sql, true)
		if !errors.Is(err, sqlguard.ErrMultiple) {
			t.Errorf("%q: err = %v, want ErrMultiple", sql, err)
		}
	}
}

func TestSemicolonsInsideLiteralsAreOneStatement(t *testing.T) {
	for _, sql := range []string{
		"SELECT ';'",
		"SELECT 'it''s; fine'",
		"SELECT E'a\\'; b'",
		"SELECT e'a\\\\'",
		"SELECT \"weird;name\" FROM t",
		"SELECT \"say \"\"hi\"\";\" FROM t",
		"SELECT $$ ; $$",
		"SELECT $fn$ $$ ; $$ $fn$",
		"SELECT $a$;$a$, $b$;$b$",
		"SELECT 1 -- ; trailing comment",
		"SELECT 1 /* ; */",
		"SELECT /* /* ; */ ; */ 1",
		"SELECT U&'d\\0061t\\+000061;'",
		"SELECT B'1010', X'1F'",
		"SELECT a$b FROM t",
		"SELECT 1 FROM t WHERE x = $1",
	} {
		if _, err := sqlguard.Check(sql, false); err != nil {
			t.Errorf("%q: %v", sql, err)
		}
	}
}

func TestStandardStringsKeepBackslashLiteral(t *testing.T) {
	// standard_conforming_strings is on: in '...' a backslash is an ordinary character,
	// so the quote after it closes the string and the ; that follows separates statements.
	if _, err := sqlguard.Check(`SELECT 'a\'; SELECT 1 --'`, false); !errors.Is(err, sqlguard.ErrMultiple) {
		t.Fatalf("err = %v, want ErrMultiple", err)
	}
}

func TestUnterminated(t *testing.T) {
	for _, sql := range []string{
		"SELECT 'abc",
		"SELECT \"abc",
		"SELECT $$abc",
		"SELECT $x$abc$y$",
		"SELECT /* abc",
		"SELECT /* /* */ abc",
		"SELECT E'abc\\'",
	} {
		_, err := sqlguard.Check(sql, false)
		if !errors.Is(err, sqlguard.ErrUnterminated) {
			t.Errorf("%q: err = %v, want ErrUnterminated", sql, err)
		}
	}
}

func TestEmpty(t *testing.T) {
	for _, sql := range []string{"", "   ", ";", " ; ;", "-- only a comment", "/* x */"} {
		if _, err := sqlguard.Check(sql, false); !errors.Is(err, sqlguard.ErrEmpty) {
			t.Errorf("%q: err = %v, want ErrEmpty", sql, err)
		}
	}
}

func TestNULAndSizeRejected(t *testing.T) {
	if _, err := sqlguard.Check("SELECT 1\x00", false); err == nil {
		t.Error("NUL byte accepted")
	}
	if _, err := sqlguard.Check("SELECT '"+strings.Repeat("x", sqlguard.MaxLen)+"'", false); err == nil {
		t.Error("oversized query accepted")
	}
}

func TestSQLDropsTrailingSemicolonsAndComments(t *testing.T) {
	for in, want := range map[string]string{
		"SELECT 1;":                 "SELECT 1",
		"  SELECT 1 ; -- done\n  ":  "  SELECT 1",
		"SELECT 1 -- trailing":      "SELECT 1",
		"SELECT 1 /* c */ ;;":       "SELECT 1",
		"-- head\nSELECT 1":         "-- head\nSELECT 1",
		"SELECT ';' ; ":             "SELECT ';'",
		"SELECT 1\n-- a\n/* b */\n": "SELECT 1",
	} {
		st, err := sqlguard.Check(in, false)
		if err != nil {
			t.Errorf("%q: %v", in, err)
			continue
		}
		if st.SQL != want {
			t.Errorf("%q: SQL = %q, want %q", in, st.SQL, want)
		}
	}
}
