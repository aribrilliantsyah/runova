package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"strconv"

	"runova/internal/caddy"
	"runova/internal/config"
	"runova/internal/store"
)

// panel handles `runova panel set --domain <d> | --port <p>`. It writes the
// panel address straight to the database, for the installer to use before the
// service runs. The panel is reached by domain or by IP:port, so each option
// replaces the other.
func panel(cfg config.Config, args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 || args[0] != "set" {
		fmt.Fprint(stderr, usage)
		return 2
	}
	fs := flag.NewFlagSet("panel set", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	domain := fs.String("domain", "", "")
	port := fs.String("port", "", "")
	if err := fs.Parse(args[1:]); err != nil || fs.NArg() != 0 {
		fmt.Fprint(stderr, usage)
		return 2
	}
	var domainSet, portSet bool
	fs.Visit(func(f *flag.Flag) {
		domainSet = domainSet || f.Name == "domain"
		portSet = portSet || f.Name == "port"
	})
	if domainSet == portSet {
		fmt.Fprint(stderr, usage)
		return 2
	}

	st, err := store.Open(cfg.DBPath())
	if err != nil {
		fmt.Fprintf(stderr, "Tidak bisa membuka database: %v\n", err)
		return 1
	}
	defer st.Close()

	ctx := context.Background()
	settings, err := st.Settings(ctx)
	if err != nil {
		fmt.Fprintf(stderr, "Gagal membaca setelan: %v\n", err)
		return 1
	}
	settings.PanelDomain, settings.PanelPort = "", 0
	if domainSet {
		host, err := caddy.ValidateDomain(*domain)
		if err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		taken, err := st.DomainHostTaken(ctx, host)
		if err != nil {
			fmt.Fprintf(stderr, "Gagal memeriksa domain: %v\n", err)
			return 1
		}
		if taken {
			fmt.Fprintln(stderr, "Domain sudah dipakai aplikasi")
			return 1
		}
		settings.PanelDomain = host
	} else {
		n, err := strconv.Atoi(*port)
		if err != nil {
			fmt.Fprintf(stderr, "Port tidak valid: %q\n", *port)
			return 1
		}
		settings.PanelPort = n
	}
	if err := st.SetSettings(ctx, settings); err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	if domainSet {
		fmt.Fprintf(stdout, "Alamat panel: https://%s\n", settings.PanelDomain)
	} else {
		fmt.Fprintf(stdout, "Alamat panel: https://<IP server>:%d\n", settings.PanelPort)
	}
	return 0
}
