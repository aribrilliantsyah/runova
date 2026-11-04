// Command relserver imitates the parts of github.com that `runova update` and get.sh use:
//
//	/<repo>/releases/latest            -> 302 to /<repo>/releases/tag/v<latest>
//	/<repo>/releases/tag/v<version>    -> 200
//	/<repo>/releases/download/...      -> files under <dir>
//
// Usage: relserver <dir> <addr> <repo> <latest>
package main

import (
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"
)

func main() {
	if len(os.Args) != 5 {
		fmt.Fprintln(os.Stderr, "usage: relserver <dir> <addr> <repo> <latest>")
		os.Exit(2)
	}
	dir, addr, repo, latest := os.Args[1], os.Args[2], os.Args[3], os.Args[4]
	files := http.FileServer(http.Dir(dir))
	log.Fatal(http.ListenAndServe(addr, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/"+repo+"/releases/latest":
			http.Redirect(w, r, "/"+repo+"/releases/tag/v"+latest, http.StatusFound)
		case strings.HasPrefix(r.URL.Path, "/"+repo+"/releases/tag/"):
			w.WriteHeader(http.StatusOK)
		default:
			files.ServeHTTP(w, r)
		}
	})))
}
