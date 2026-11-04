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
	exists := len(args) == 1 && args[0] == "exists"
	if !exists && (len(args) != 2 || (args[0] != "create" && args[0] != "reset-password")) {
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
	switch {
	case exists:
		return adminExists(ctx, st, stderr)
	case args[0] == "create":
		return createAdmin(ctx, st, args[1], stdout, stderr)
	default:
		return resetPassword(ctx, st, args[1], stdout, stderr)
	}
}

// adminExists exits 0 when at least one admin exists and 1 when none does, so
// the installer creates the first admin only once.
func adminExists(ctx context.Context, st *store.Store, stderr io.Writer) int {
	users, err := st.Users(ctx)
	if err != nil {
		fmt.Fprintf(stderr, "Could not read users: %v\n", err)
		return 2
	}
	for _, u := range users {
		if u.IsAdmin() {
			return 0
		}
	}
	return 1
}

func createAdmin(ctx context.Context, st *store.Store, username string, stdout, stderr io.Writer) int {
	if err := store.ValidateUsername(username); err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	settings, err := st.Settings(ctx)
	if err != nil {
		fmt.Fprintf(stderr, "Could not read settings: %v\n", err)
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
		fmt.Fprintln(stderr, "user already exists")
		return 1
	}
	if err != nil {
		fmt.Fprintf(stderr, "Could not create the user: %v\n", err)
		return 1
	}
	fmt.Fprintf(stdout, "Username: %s\nPassword: %s\n", username, pw)
	return 0
}

func resetPassword(ctx context.Context, st *store.Store, username string, stdout, stderr io.Writer) int {
	u, _, err := st.UserByUsername(ctx, username)
	if errors.Is(err, store.ErrNotFound) {
		fmt.Fprintf(stderr, "user %q not found\n", username)
		return 1
	}
	if err != nil {
		fmt.Fprintf(stderr, "Could not read the user: %v\n", err)
		return 1
	}
	pw, hash, ok := newCredentials(stderr)
	if !ok {
		return 1
	}
	if err := st.SetPassword(ctx, u.ID, hash); err != nil {
		fmt.Fprintf(stderr, "Could not change the password: %v\n", err)
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
