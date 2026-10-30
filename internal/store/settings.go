package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/netip"
	"slices"
)

var RuntimeIDs = []string{"java8", "java11", "java17", "java21", "java25", "static", "glibc"}

type Settings struct {
	DefaultQuota  Quota           `json:"default_quota"`
	UploadLimitMB int             `json:"upload_limit_mb"`
	PortMin       int             `json:"port_min"`
	PortMax       int             `json:"port_max"`
	Runtimes      map[string]bool `json:"runtimes"`
	PublicIP      string          `json:"public_ip"`
}

func defaultSettings() Settings {
	rt := make(map[string]bool, len(RuntimeIDs))
	for _, id := range RuntimeIDs {
		rt[id] = true
	}
	return Settings{
		DefaultQuota:  Quota{Apps: 3, MemoryMB: 1024, CPUMilli: 1000, Databases: 3, PGConnections: 20},
		UploadLimitMB: 500,
		PortMin:       20000,
		PortMax:       20999,
		Runtimes:      rt,
	}
}

func (s Settings) Validate() error {
	if err := s.DefaultQuota.Validate(); err != nil {
		return fmt.Errorf("kuota bawaan: %w", err)
	}
	if s.UploadLimitMB < 1 || s.UploadLimitMB > 100000 {
		return errors.New("Batas unggahan harus antara 1 dan 100000 MB")
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
