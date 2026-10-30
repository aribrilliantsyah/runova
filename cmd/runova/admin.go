package main

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"math/big"
	"time"

	"runova/internal/auth"
	"runova/internal/config"
	"runova/internal/store"
)

const (
	generatedPasswordLen = 20
	// passwordAlphabet leaves out look-alike characters: 0 O I l 1.
	passwordAlphabet = "ABCDEFGHJKLMNPQRSTUVWXYZabcdefghijkmnopqrstuvwxyz23456789"
)

func randomPassword() (string, error) {
	size := big.NewInt(int64(len(passwordAlphabet)))
	out := make([]byte, generatedPasswordLen)
	for i := range out {
		n, err := rand.Int(rand.Reader, size)
		if err != nil {
			return "", fmt.Errorf("generate password: %w", err)
		}
		out[i] = passwordAlphabet[n.Int64()]
	}
	return string(out), nil
}

func admin(cfg config.Config, args []string, stdout, stderr io.Writer) int {
	if len(args) != 2 || (args[0] != "create" && args[0] != "reset-password") {
		fmt.Fprint(stderr, usage)
		return 2
	}
	sub, username := args[0], args[1]

	st, err := store.Open(cfg.DBPath())
	if err != nil {
		fmt.Fprintf(stderr, "Tidak bisa membuka database: %v\n", err)
		return 1
	}
	defer st.Close()

	ctx := context.Background()
	if sub == "create" {
		return createAdmin(ctx, st, username, stdout, stderr)
	}
	return resetPassword(ctx, st, username, stdout, stderr)
}

func createAdmin(ctx context.Context, st *store.Store, username string, stdout, stderr io.Writer) int {
	if err := store.ValidateUsername(username); err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	settings, err := st.Settings(ctx)
	if err != nil {
		fmt.Fprintf(stderr, "Gagal membaca setelan: %v\n", err)
		return 1
	}
	pw, hash, ok := newCredentials(stderr)
	if !ok {
		return 1
	}
	_, err = st.CreateUser(ctx, store.NewUser{
		Username: username, DisplayName: username, PasswordHash: hash,
		Role: store.RoleAdmin, Quota: settings.DefaultQuota,
	}, time.Now().UnixMilli())
	if errors.Is(err, store.ErrConflict) {
		fmt.Fprintln(stderr, "pengguna sudah ada")
		return 1
	}
	if err != nil {
		fmt.Fprintf(stderr, "Gagal membuat pengguna: %v\n", err)
		return 1
	}
	fmt.Fprintf(stdout, "Username: %s\nPassword: %s\n", username, pw)
	return 0
}

func resetPassword(ctx context.Context, st *store.Store, username string, stdout, stderr io.Writer) int {
	u, _, err := st.UserByUsername(ctx, username)
	if errors.Is(err, store.ErrNotFound) {
		fmt.Fprintf(stderr, "pengguna %q tidak ditemukan\n", username)
		return 1
	}
	if err != nil {
		fmt.Fprintf(stderr, "Gagal membaca pengguna: %v\n", err)
		return 1
	}
	pw, hash, ok := newCredentials(stderr)
	if !ok {
		return 1
	}
	if err := st.SetPassword(ctx, u.ID, hash); err != nil {
		fmt.Fprintf(stderr, "Gagal mengganti password: %v\n", err)
		return 1
	}
	fmt.Fprintf(stdout, "Username: %s\nPassword: %s\n", username, pw)
	return 0
}

func newCredentials(stderr io.Writer) (password, hash string, ok bool) {
	password, err := randomPassword()
	if err == nil {
		hash, err = auth.HashPassword(password)
	}
	if err != nil {
		fmt.Fprintln(stderr, err)
		return "", "", false
	}
	return password, hash, true
}
