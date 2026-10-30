// Command dbapp is a tiny static HTTP server used to smoke-test Runova's
// PostgreSQL integration. It connects with $DATABASE_URL, keeps rows in a
// "visits" table, and listens on $PORT:
//
//	GET  /     the number of rows
//	POST /add  insert a row, reply with the new count
package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"

	"github.com/jackc/pgx/v5/pgxpool"
)

func main() {
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, os.Getenv("DATABASE_URL"))
	if err != nil {
		log.Fatalf("database url: %v", err)
	}
	if _, err := pool.Exec(ctx, `CREATE TABLE IF NOT EXISTS visits(id serial PRIMARY KEY, at timestamptz NOT NULL DEFAULT now())`); err != nil {
		log.Fatalf("create table: %v", err)
	}

	count := func(ctx context.Context) (n int, err error) {
		err = pool.QueryRow(ctx, `SELECT count(*) FROM visits`).Scan(&n)
		return
	}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /add", func(w http.ResponseWriter, r *http.Request) {
		if _, err := pool.Exec(r.Context(), `INSERT INTO visits DEFAULT VALUES`); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		n, err := count(r.Context())
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		fmt.Fprintf(w, "rows=%d\n", n)
	})
	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) {
		n, err := count(r.Context())
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		fmt.Fprintf(w, "rows=%d\n", n)
	})

	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}
	log.Printf("listening on :%s", port)
	log.Fatal(http.ListenAndServe(":"+port, mux))
}
