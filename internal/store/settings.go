package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/netip"
	"slices"

	"runova/internal/caddy"
)

const caddyAdminPort = 2019

var RuntimeIDs = []string{"java8", "java11", "java17", "java21", "java25", "static", "glibc"}

// MaxPGAllowedNets caps Settings.PGAllowedNets.
const MaxPGAllowedNets = 20

// MaxTrustedProxies caps Settings.TrustedProxies.
const MaxTrustedProxies = 10

type Settings struct {
	DefaultQuota  Quota `json:"default_quota"`
	UploadLimitMB int   `json:"upload_limit_mb"`
	// MaxPendingUploads caps the uploads a user may have waiting to be deployed.
	MaxPendingUploads int             `json:"max_pending_uploads"`
	PortMin           int             `json:"port_min"`
	PortMax           int             `json:"port_max"`
	Runtimes          map[string]bool `json:"runtimes"`
	PublicIP          string          `json:"public_ip"`
	// PanelDomain and PanelPort make the panel reachable through Caddy; both
	// empty means only at RUNOVA_LISTEN. A port serves it over HTTPS on that IP port.
	PanelDomain string `json:"panel_domain"`
	PanelPort   int    `json:"panel_port"`
	// PGAllowedNets lists extra CIDRs allowed to reach PostgreSQL from outside Docker.
	PGAllowedNets []string `json:"pg_allowed_nets"`
	// TrustedProxies lists the IPs and CIDRs of reverse proxies in front of
	// Runova (such as nginx) whose X-Forwarded-For is believed.
	TrustedProxies []string `json:"trusted_proxies"`
	// UpdateCheck lets the server look for a newer Runova release.
	UpdateCheck bool `json:"update_check"`
}

func defaultSettings() Settings {
	rt := make(map[string]bool, len(RuntimeIDs))
	for _, id := range RuntimeIDs {
		rt[id] = true
	}
	return Settings{
		DefaultQuota:      Quota{Apps: 3, MemoryMB: 1024, CPUMilli: 1000, Databases: 3, PGConnections: 20},
		UploadLimitMB:     500,
		MaxPendingUploads: 5,
		PortMin:           20000,
		PortMax:           20999,
		Runtimes:          rt,
		PGAllowedNets:     []string{},
		TrustedProxies:    []string{},
		UpdateCheck:       true,
	}
}

func (s Settings) Validate() error {
	if err := s.DefaultQuota.Validate(); err != nil {
		return fmt.Errorf("kuota bawaan: %w", err)
	}
	if s.UploadLimitMB < 1 || s.UploadLimitMB > 100000 {
		return errors.New("Batas unggahan harus antara 1 dan 100000 MB")
	}
	if s.MaxPendingUploads < 1 || s.MaxPendingUploads > 50 {
		return errors.New("Batas unggahan tertunda harus antara 1 dan 50")
	}
	if s.PortMin < 1024 || s.PortMax > 65535 || s.PortMin > s.PortMax {
		return errors.New("Rentang port harus di dalam 1024–65535 dan awal tidak boleh melebihi akhir")
	}
	for id := range s.Runtimes {
		if !slices.Contains(RuntimeIDs, id) {
			return fmt.Errorf("Runtime tidak dikenal: %s", id)
		}
	}
	if s.PublicIP != "" {
		if _, err := netip.ParseAddr(s.PublicIP); err != nil {
			return errors.New("IP publik tidak valid")
		}
	}
	if s.PanelDomain != "" {
		host, err := caddy.ValidateDomain(s.PanelDomain)
		if err != nil {
			return fmt.Errorf("domain panel: %w", err)
		}
		if host != s.PanelDomain {
			return errors.New("Domain panel harus huruf kecil tanpa titik di akhir")
		}
	}
	if s.PanelPort != 0 {
		switch {
		case s.PanelPort < 1024 || s.PanelPort > 65535:
			return errors.New("Port panel harus di dalam 1024–65535")
		case s.PanelPort == caddyAdminPort:
			return fmt.Errorf("Port panel tidak boleh %d (dipakai Caddy)", caddyAdminPort)
		case s.PanelPort >= s.PortMin && s.PanelPort <= s.PortMax:
			return errors.New("Port panel tidak boleh berada di rentang port akses IP")
		}
	}
	if len(s.PGAllowedNets) > MaxPGAllowedNets {
		return fmt.Errorf("Jaringan PostgreSQL maksimal %d", MaxPGAllowedNets)
	}
	for _, n := range s.PGAllowedNets {
		if _, err := netip.ParsePrefix(n); err != nil {
			return fmt.Errorf("Jaringan PostgreSQL tidak valid: %s", n)
		}
	}
	return s.validateTrustedProxies()
}

func (s Settings) validateTrustedProxies() error {
	if len(s.TrustedProxies) > MaxTrustedProxies {
		return fmt.Errorf("Proxy tepercaya maksimal %d", MaxTrustedProxies)
	}
	for _, n := range s.TrustedProxies {
		p, err := netip.ParsePrefix(n)
		if err != nil {
			a, aerr := netip.ParseAddr(n)
			if aerr != nil {
				return fmt.Errorf("Proxy tepercaya tidak valid: %s", n)
			}
			p = netip.PrefixFrom(a, a.BitLen())
		}
		if p.Bits() == 0 {
			return errors.New("Proxy tepercaya tidak boleh mencakup seluruh internet (0.0.0.0/0 atau ::/0)")
		}
	}
	return nil
}

// Settings returns the stored settings, or the defaults on a fresh database.
func (s *Store) Settings(ctx context.Context) (Settings, error) {
	var raw string
	err := s.db.QueryRowContext(ctx, `SELECT value FROM settings WHERE id = 1`).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return defaultSettings(), nil
	}
	if err != nil {
		return Settings{}, fmt.Errorf("read settings: %w", err)
	}
	out := defaultSettings()
	clear(out.Runtimes)
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		return Settings{}, fmt.Errorf("decode settings: %w", err)
	}
	return out, nil
}

func (s *Store) SetSettings(ctx context.Context, v Settings) error {
	if err := v.Validate(); err != nil {
		return err
	}
	rt := make(map[string]bool, len(RuntimeIDs))
	for _, id := range RuntimeIDs {
		rt[id] = v.Runtimes[id]
	}
	v.Runtimes = rt
	if v.PGAllowedNets == nil {
		v.PGAllowedNets = []string{}
	}
	if v.TrustedProxies == nil {
		v.TrustedProxies = []string{}
	}
	raw, err := json.Marshal(v)
	if err != nil {
		return fmt.Errorf("encode settings: %w", err)
	}
	_, err = s.db.ExecContext(ctx,
		`INSERT INTO settings(id, value) VALUES(1, ?) ON CONFLICT(id) DO UPDATE SET value = excluded.value`, string(raw))
	if err != nil {
		return fmt.Errorf("write settings: %w", err)
	}
	return nil
}
