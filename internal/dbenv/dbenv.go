// Package dbenv maps environment variable names to parts of a PostgreSQL
// connection, so an app gets its database variables without the user typing
// them. It is pure: no store, no PostgreSQL.
package dbenv

import (
	"net/url"
	"slices"
	"strconv"
	"strings"
)

// Kind says which part of the database connection a variable holds.
type Kind string

const (
	URL      Kind = "url"      // postgres://user:password@host:port/db
	JDBC     Kind = "jdbc"     // jdbc:postgresql://host:port/db
	R2DBC    Kind = "r2dbc"    // r2dbc:postgresql://host:port/db
	Host     Kind = "host"     // host.docker.internal
	Port     Kind = "port"     // 5432
	User     Kind = "user"     // the owner's role
	Password Kind = "password" // the role's password
	Database Kind = "database" // the database name
)

// Var is a variable Runova fills from the app's database. Standard marks the
// names every app with a database gets, whether its artifact names them or not.
type Var struct {
	Name     string `json:"name"`
	Kind     Kind   `json:"kind"`
	Standard bool   `json:"standard"`
}

var standard = []Var{
	{"DATABASE_URL", URL, true},
	{"SPRING_DATASOURCE_URL", JDBC, true},
	{"SPRING_DATASOURCE_USERNAME", User, true},
	{"SPRING_DATASOURCE_PASSWORD", Password, true},
	{"PGHOST", Host, true},
	{"PGPORT", Port, true},
	{"PGUSER", User, true},
	{"PGPASSWORD", Password, true},
	{"PGDATABASE", Database, true},
}

// StandardNames returns the names every app with a database gets.
func StandardNames() []string {
	out := make([]string, len(standard))
	for i, v := range standard {
		out[i] = v.Name
	}
	return out
}

// dbWords mark a name as being about the database. At least one must come
// before the field word.
var dbWords = map[string]bool{
	"DB": true, "DATABASE": true, "POSTGRES": true, "POSTGRESQL": true, "PG": true,
	"DATASOURCE": true, "JDBC": true, "R2DBC": true, "TYPEORM": true, "SQL": true,
}

// frameworkWords may also come before the field word: SPRING_DATASOURCE_URL,
// QUARKUS_DATASOURCE_JDBC_URL, APP_DATABASE_URL. Any other word (TEST, READ,
// REPLICA, ADMIN, BILLING…) makes the name ambiguous, and it is left alone.
var frameworkWords = map[string]bool{"SPRING": true, "QUARKUS": true, "APP": true}

var fieldWords = map[string]Kind{
	"URL": URL, "URI": URL, "DSN": URL,
	"HOST": Host, "HOSTNAME": Host, "SERVER": Host,
	"PORT": Port,
	"USER": User, "USERNAME": User, "LOGIN": User,
	"PASS": Password, "PASSWORD": Password, "PWD": Password, "PASSWD": Password,
	"NAME": Database, "DATABASE": Database, "DBNAME": Database, "DB": Database,
}

// splitWords splits NAME on "_" and expands glued forms such as DBURL or
// PGPASS into DB URL and PG PASS.
func splitWords(name string) []string {
	var out []string
	for _, w := range strings.Split(name, "_") {
		for _, p := range []string{"DB", "PG"} {
			if rest, ok := strings.CutPrefix(w, p); ok {
				if _, field := fieldWords[rest]; field {
					out = append(out, p)
					w = rest
					break
				}
			}
		}
		out = append(out, w)
	}
	return out
}

// KindOf tells what a variable named name should hold when the app uses a
// PostgreSQL database; "" when Runova should not fill it. jar picks JDBC for a
// plain URL name. Names are matched case-insensitively.
func KindOf(name string, jar bool) Kind {
	name = strings.ToUpper(name)
	for _, v := range standard {
		if v.Name == name {
			return v.Kind
		}
	}
	words := splitWords(name)
	n := len(words)
	var kind Kind
	var quals []string
	switch {
	case n >= 3 && words[n-2] == "CONNECTION" && words[n-1] == "STRING":
		kind, quals = URL, words[:n-2]
	case n >= 2:
		kind, quals = fieldWords[words[n-1]], words[:n-1]
	}
	if kind == "" || len(quals) == 0 {
		return ""
	}
	context := false
	for _, q := range quals {
		switch {
		case dbWords[q]:
			context = true
		case frameworkWords[q]:
		default:
			return ""
		}
	}
	// SQL_SERVER is Microsoft's database, not a host.
	if !context || (kind == Host && words[n-1] == "SERVER" && quals[len(quals)-1] == "SQL") {
		return ""
	}
	if kind != URL {
		return kind
	}
	has := func(ws ...string) bool {
		return slices.ContainsFunc(quals, func(q string) bool { return slices.Contains(ws, q) })
	}
	switch {
	case has("R2DBC"):
		return R2DBC
	case has("JDBC", "DATASOURCE", "SPRING", "QUARKUS"):
		return JDBC
	case words[n-1] == "DSN":
		return URL
	case jar:
		return JDBC
	}
	return URL
}

// Vars returns every variable an app with a database gets: the standard
// names, then the detected names KindOf recognizes, in order and without
// repeats. Names are kept as given.
func Vars(detected []string, jar bool) []Var {
	out := slices.Clone(standard)
	for _, name := range detected {
		if slices.ContainsFunc(out, func(v Var) bool { return v.Name == name }) {
			continue
		}
		if k := KindOf(name, jar); k != "" {
			out = append(out, Var{Name: name, Kind: k})
		}
	}
	return out
}

// Conn is what the variables are built from.
type Conn struct {
	Host     string
	Port     int
	User     string
	Password string
	Database string
}

// Value returns the variable's value for kind k.
func (c Conn) Value(k Kind) string {
	hostport := c.Host + ":" + strconv.Itoa(c.Port)
	switch k {
	case URL:
		u := url.URL{Scheme: "postgres", User: url.UserPassword(c.User, c.Password), Host: hostport, Path: "/" + c.Database}
		return u.String()
	case JDBC:
		return "jdbc:postgresql://" + hostport + "/" + c.Database
	case R2DBC:
		return "r2dbc:postgresql://" + hostport + "/" + c.Database
	case Host:
		return c.Host
	case Port:
		return strconv.Itoa(c.Port)
	case User:
		return c.User
	case Password:
		return c.Password
	case Database:
		return c.Database
	}
	return ""
}
