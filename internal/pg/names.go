package pg

import (
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
)

var (
	roleRE   = regexp.MustCompile(`^[a-z][a-z0-9]{1,30}$`)
	suffixRE = regexp.MustCompile(`^[a-z][a-z0-9_]{0,30}$`)
	dbNameRE = regexp.MustCompile(`^[a-z][a-z0-9]{1,30}_[a-z][a-z0-9_]{0,30}$`)
)

// ValidateDBSuffix checks the user-chosen part of a database name.
func ValidateDBSuffix(s string) error {
	if !suffixRE.MatchString(s) {
		return errors.New("Nama database harus diawali huruf kecil, hanya berisi huruf kecil, angka, dan garis bawah, maksimal 31 karakter")
	}
	return nil
}

// DBName returns the PostgreSQL database name "<username>_<suffix>". Both parts
// are at most 31 bytes, so the result never exceeds PostgreSQL's 63-byte limit.
func DBName(username, suffix string) (string, error) {
	if err := ValidateDBSuffix(suffix); err != nil {
		return "", err
	}
	if !roleRE.MatchString(username) {
		return "", errors.New("Username tidak valid untuk nama database")
	}
	return username + "_" + suffix, nil
}

var reservedRoles = []string{"postgres", "runova", "root", "public", "template"}

func validateRole(role string) error {
	if !roleRE.MatchString(role) || slices.Contains(reservedRoles, role) {
		return fmt.Errorf("%w: role %q", ErrInvalid, role)
	}
	return nil
}

// splitDBName returns the owning username and suffix of a Runova database name.
func splitDBName(name string) (owner, suffix string, err error) {
	if !dbNameRE.MatchString(name) {
		return "", "", fmt.Errorf("%w: database name %q", ErrInvalid, name)
	}
	owner, suffix, _ = strings.Cut(name, "_")
	if slices.Contains(reservedRoles, owner) {
		return "", "", fmt.Errorf("%w: database name %q", ErrInvalid, name)
	}
	return owner, suffix, nil
}
