// Command hello is the sample app of the local end-to-end test. It listens on
// $PORT and serves:
//
//	GET /                  "hello <version>" (version injected with -ldflags -X main.version)
//	GET /db                the number of rows in the visits table of $DATABASE_URL (a row is added per call)
//	GET /connect?dsn=<url> "ok" or "error: ..." after connecting to that PostgreSQL URL from inside the container
//	GET /dial?addr=h:p     "ok" or "error: ..." after a TCP connection from inside the container
package main

import (
	"context"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5"
)

var version = "dev"

const probeTimeout = 3 * time.Second

func main() {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, "hello %s\n", version)
	})
	mux.HandleFunc("GET /db", db)
	mux.HandleFunc("GET /connect", connect)
	mux.HandleFunc("GET /dial", dial)

	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}
	log.Printf("hello %s listening on :%s", version, port)
	// Like a Spring Boot or Go server with signal handling: SIGTERM ends the
	// process with exit code 0, which Docker's on-failure policy never restarts.
	srv := &http.Server{Addr: ":" + port, Handler: mux}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, os.Interrupt)
	defer stop()
	go func() {
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		srv.Shutdown(shutdown)
	}()
	if err := srv.ListenAndServe(); err != http.ErrServerClosed {
		log.Fatal(err)
	}
	log.Print("stopped by signal")
}

func db(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	conn, err := pgx.Connect(ctx, os.Getenv("DATABASE_URL"))
	if err != nil {
		http.Error(w, "connect: "+err.Error(), http.StatusInternalServerError)
		return
	}
	defer conn.Close(ctx)
	var n int
	if _, err := conn.Exec(ctx, `CREATE TABLE IF NOT EXISTS visits(id serial PRIMARY KEY, at timestamptz NOT NULL DEFAULT now())`); err != nil {
		http.Error(w, "create: "+err.Error(), http.StatusInternalServerError)
		return
	}
	if r.URL.Query().Get("add") == "1" {
		if _, err := conn.Exec(ctx, `INSERT INTO visits DEFAULT VALUES`); err != nil {
			http.Error(w, "insert: "+err.Error(), http.StatusInternalServerError)
			return
		}
	}
	if err := conn.QueryRow(ctx, `SELECT count(*) FROM visits`).Scan(&n); err != nil {
		http.Error(w, "count: "+err.Error(), http.StatusInternalServerError)
		return
	}
	fmt.Fprintf(w, "rows=%d\n", n)
}

func connect(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), probeTimeout)
	defer cancel()
	conn, err := pgx.Connect(ctx, r.URL.Query().Get("dsn"))
	if err != nil {
		fmt.Fprintf(w, "error: %v\n", err)
		return
	}
	defer conn.Close(ctx)
	if err := conn.Ping(ctx); err != nil {
		fmt.Fprintf(w, "error: %v\n", err)
		return
	}
	fmt.Fprintln(w, "ok")
}

func dial(w http.ResponseWriter, r *http.Request) {
	c, err := net.DialTimeout("tcp", r.URL.Query().Get("addr"), probeTimeout)
	if err != nil {
		fmt.Fprintf(w, "error: %v\n", err)
		return
	}
	c.Close()
	fmt.Fprintln(w, "ok")
}
