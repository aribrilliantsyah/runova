// Package pgtest starts a throwaway PostgreSQL server in Docker for integration tests.
package pgtest

import (
	"context"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

const (
	testImage       = "postgres:18"
	testSuperPass   = "postgres"
	testRunovaPass  = "x"
	testStartupWait = 90 * time.Second
)

// StartPG runs a throwaway PostgreSQL 18 container on a random host port and
// returns the DSN for its "runova" admin role. It skips unless RUNOVA_PG_TEST=1.
func StartPG(t *testing.T) string {
	t.Helper()
	if os.Getenv("RUNOVA_PG_TEST") != "1" {
		t.Skip("set RUNOVA_PG_TEST=1 to run PostgreSQL integration tests")
	}

	out, err := exec.Command("docker", "run", "-d", "--rm",
		"-p", "127.0.0.1::5432",
		"-e", "POSTGRES_PASSWORD="+testSuperPass,
		"-e", "POSTGRES_HOST_AUTH_METHOD=scram-sha-256",
		testImage, "-c", "fsync=off",
	).CombinedOutput()
	if err != nil {
		t.Fatalf("docker run: %v\n%s", err, out)
	}
	id := strings.TrimSpace(string(out))
	t.Cleanup(func() {
		if out, err := exec.Command("docker", "rm", "-f", "-v", id).CombinedOutput(); err != nil {
			t.Errorf("docker rm: %v\n%s", err, out)
		}
	})

	out, err = exec.Command("docker", "port", id, "5432/tcp").CombinedOutput()
	if err != nil {
		t.Fatalf("docker port: %v\n%s", err, out)
	}
	hostPort := strings.TrimSpace(strings.SplitN(string(out), "\n", 2)[0])
	i := strings.LastIndex(hostPort, ":")
	if i < 0 {
		t.Fatalf("unexpected docker port output %q", out)
	}
	host, port := hostPort[:i], hostPort[i+1:]

	superDSN := "host=" + host + " port=" + port + " user=postgres password=" + testSuperPass + " dbname=postgres"
	ctx, cancel := context.WithTimeout(context.Background(), testStartupWait)
	defer cancel()
	var conn *pgx.Conn
	for {
		conn, err = pgx.Connect(ctx, superDSN)
		if err == nil {
			break
		}
		if ctx.Err() != nil {
			t.Fatalf("postgres did not become ready: %v", err)
		}
		time.Sleep(250 * time.Millisecond)
	}
	defer conn.Close(ctx)

	for _, stmt := range []string{
		"CREATE ROLE runova LOGIN CREATEDB CREATEROLE PASSWORD '" + testRunovaPass + "'",
		"GRANT EXECUTE ON FUNCTION pg_reload_conf() TO runova",
	} {
		if _, err := conn.Exec(ctx, stmt); err != nil {
			t.Fatalf("%s: %v", stmt, err)
		}
	}
	return "host=" + host + " port=" + port + " user=runova password=" + testRunovaPass + " dbname=postgres"
}
