// Command hello is a tiny static HTTP server used to smoke-test Runova.
// It listens on $PORT, logs each request, and exits with status 1 on /crash.
package main

import (
	"fmt"
	"log"
	"net/http"
	"os"
)

func main() {
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}
	if err := os.WriteFile(os.Getenv("RUNOVA_DATA_DIR")+"/started", []byte("ok\n"), 0o644); err != nil {
		log.Printf("data dir not writable: %v", err)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/crash", func(w http.ResponseWriter, r *http.Request) {
		log.Print("crashing on request")
		os.Exit(1)
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		log.Printf("%s %s", r.Method, r.URL.Path)
		fmt.Fprintln(w, "hello from runova")
	})
	log.Printf("listening on :%s", port)
	log.Fatal(http.ListenAndServe(":"+port, mux))
}
