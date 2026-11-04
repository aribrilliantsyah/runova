package main

import (
	"cmp"
	"context"
	"flag"
	"fmt"
	"io"
	"net/netip"
	"strconv"
	"strings"

	"runova/internal/caddy"
	"runova/internal/config"
	"runova/internal/store"
)

// panel handles `runova panel set [--domain <d> | --port <p>] [--public-ip <ip>]
// [--trusted-proxy <ip/cidr>]...`. It writes the panel address straight to the
// database, for the installer to use before the service runs. The panel is
// reached by domain or by IP:port, so each option replaces the other;
// --public-ip and --trusted-proxy leave the address alone. --trusted-proxy
// replaces the whole list; an empty value clears it.
func panel(cfg config.Config, args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 || args[0] != "set" {
		fmt.Fprint(stderr, usage)
		return 2
	}
	fs := flag.NewFlagSet("panel set", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	domain := fs.String("domain", "", "")
	port := fs.String("port", "", "")
	publicIP := fs.String("public-ip", "", "")
	var proxies proxyList
	fs.Var(&proxies, "trusted-proxy", "")
	if err := fs.Parse(args[1:]); err != nil || fs.NArg() != 0 {
		fmt.Fprint(stderr, usage)
		return 2
	}
	var domainSet, portSet, ipSet, proxySet bool
	fs.Visit(func(f *flag.Flag) {
		domainSet = domainSet || f.Name == "domain"
		portSet = portSet || f.Name == "port"
		ipSet = ipSet || f.Name == "public-ip"
		proxySet = proxySet || f.Name == "trusted-proxy"
	})
	if (domainSet && portSet) || !(domainSet || portSet || ipSet || proxySet) {
		fmt.Fprint(stderr, usage)
		return 2
	}

	st, err := store.Open(cfg.DBPath())
	if err != nil {
		fmt.Fprintf(stderr, "Could not open the database: %v\n", err)
		return 1
	}
	defer st.Close()

	ctx := context.Background()
	settings, err := st.Settings(ctx)
	if err != nil {
		fmt.Fprintf(stderr, "Could not read settings: %v\n", err)
		return 1
	}
	if domainSet || portSet {
		settings.PanelDomain, settings.PanelPort = "", 0
	}
	if ipSet {
		addr, err := netip.ParseAddr(*publicIP)
		if err != nil {
			fmt.Fprintf(stderr, "Invalid public IP: %q\n", *publicIP)
			return 1
		}
		settings.PublicIP = addr.String()
	}
	if proxySet {
		settings.TrustedProxies = proxies.nonEmpty()
	}
	if domainSet {
		host, err := caddy.ValidateDomain(*domain)
		if err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		taken, err := st.DomainHostTaken(ctx, host)
		if err != nil {
			fmt.Fprintf(stderr, "Could not check the domain: %v\n", err)
			return 1
		}
		if taken {
			fmt.Fprintln(stderr, "Domain is already used by an app")
			return 1
		}
		settings.PanelDomain = host
	} else if portSet {
		n, err := strconv.Atoi(*port)
		if err != nil {
			fmt.Fprintf(stderr, "Invalid port: %q\n", *port)
			return 1
		}
		settings.PanelPort = n
	}
	if err := st.SetSettings(ctx, settings); err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	switch {
	case domainSet:
		fmt.Fprintf(stdout, "Panel address: https://%s\n", settings.PanelDomain)
	case portSet:
		fmt.Fprintf(stdout, "Panel address: https://<server IP>:%d\n", settings.PanelPort)
	case ipSet:
		fmt.Fprintf(stdout, "Public IP: %s\n", settings.PublicIP)
	default:
		fmt.Fprintf(stdout, "Trusted proxies: %s\n", cmp.Or(strings.Join(settings.TrustedProxies, ", "), "none"))
	}
	return 0
}

// proxyList collects the values of a repeatable flag.
type proxyList []string

func (l *proxyList) String() string { return strings.Join(*l, ",") }

func (l *proxyList) Set(v string) error {
	*l = append(*l, strings.TrimSpace(v))
	return nil
}

func (l proxyList) nonEmpty() []string {
	out := []string{}
	for _, v := range l {
		if v != "" {
			out = append(out, v)
		}
	}
	return out
}
