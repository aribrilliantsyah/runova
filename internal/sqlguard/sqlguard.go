// Package sqlguard is the panel's first fence around SQL typed into the query
// editor: exactly one statement, and only the commands the panel offers. It is
// a UX guard, not the security boundary; queries run as the user's own
// PostgreSQL role, and that role's privileges are what actually limit them.
package sqlguard

import (
	"errors"
	"fmt"
	"strings"
)

// MaxLen is the longest query text accepted, in bytes.
const MaxLen = 256 << 10

var (
	// ErrEmpty marks text with no statement in it.
	ErrEmpty = errors.New("empty query")
	// ErrMultiple marks text with more than one statement.
	ErrMultiple = errors.New("multiple statements")
	// ErrUnterminated marks a string, quoted identifier or comment that never ends.
	ErrUnterminated = errors.New("unterminated literal")
	// ErrNeedsWrite marks a command the panel allows only in write mode.
	ErrNeedsWrite = errors.New("write mode required")
	// ErrNotAllowed marks a command the panel never runs.
	ErrNotAllowed = errors.New("command not allowed")
)

// Error is a refusal with a message for the user.
type Error struct {
	Kind error
	Msg  string
}

func (e *Error) Error() string { return e.Msg }
func (e *Error) Unwrap() error { return e.Kind }

func refuse(kind error, format string, a ...any) error {
	return &Error{Kind: kind, Msg: fmt.Sprintf(format, a...)}
}

// Statement is one accepted statement.
type Statement struct {
	// SQL is the statement without trailing semicolons and comments. Leading
	// comments stay, so error positions keep pointing at the right line.
	SQL string
	// Offset is where SQL starts in the original text, in bytes.
	Offset int
	// Command is the first keyword, upper case: SELECT, WITH, EXPLAIN, SHOW,
	// INSERT, UPDATE, DELETE, TRUNCATE, CREATE, ALTER or DROP.
	Command string
	// Rows reports whether the statement answers with rows to show; DML and
	// DDL only report how many rows they touched.
	Rows bool
}

var (
	readCommands  = map[string]bool{"SELECT": true, "WITH": true, "EXPLAIN": true, "SHOW": true}
	writeCommands = map[string]bool{"INSERT": true, "UPDATE": true, "DELETE": true, "TRUNCATE": true, "CREATE": true, "ALTER": true, "DROP": true}
)

// Check accepts sql when it holds exactly one statement whose command the
// mode allows: SELECT, WITH, EXPLAIN and SHOW always; INSERT, UPDATE, DELETE,
// TRUNCATE and CREATE/ALTER/DROP of a TABLE or INDEX only when write is set.
func Check(sql string, write bool) (Statement, error) {
	if len(sql) > MaxLen {
		return Statement{}, refuse(ErrNotAllowed, "Query terlalu panjang (maksimal %d KB)", MaxLen>>10)
	}
	if strings.IndexByte(sql, 0) >= 0 {
		return Statement{}, refuse(ErrNotAllowed, "Query berisi karakter NUL")
	}
	toks, err := scan(sql)
	if err != nil {
		return Statement{}, err
	}

	// Split at top-level semicolons and keep the statements that have code.
	var stmts [][]token
	start, segStart := 0, 0
	stmtStart := -1
	for i, t := range toks {
		if t.kind != tokSemi {
			continue
		}
		if hasCode(toks[start:i]) {
			stmts = append(stmts, toks[start:i])
			if stmtStart < 0 {
				stmtStart = segStart
			}
		}
		start, segStart = i+1, t.end
	}
	if hasCode(toks[start:]) {
		stmts = append(stmts, toks[start:])
		if stmtStart < 0 {
			stmtStart = segStart
		}
	}
	switch {
	case len(stmts) == 0:
		return Statement{}, refuse(ErrEmpty, "Query kosong")
	case len(stmts) > 1:
		return Statement{}, refuse(ErrMultiple, "Hanya satu perintah per jalankan; hapus titik koma di antara perintah")
	}

	words := codeWords(sql, stmts[0])
	cmd, err := classify(words, write)
	if err != nil {
		return Statement{}, err
	}
	last := stmts[0][len(stmts[0])-1]
	for i := len(stmts[0]) - 1; i >= 0; i-- {
		if stmts[0][i].kind == tokCode {
			last = stmts[0][i]
			break
		}
	}
	return Statement{
		SQL:     sql[stmtStart:last.end],
		Offset:  stmtStart,
		Command: cmd,
		Rows:    readCommands[cmd],
	}, nil
}

// classify returns the statement's command, or why the mode refuses it.
// words are the statement's leading code words, upper case, with opening
// parentheses already skipped.
func classify(words []string, write bool) (string, error) {
	if len(words) == 0 || words[0] == "" {
		return "", refuse(ErrNotAllowed, "Perintah tidak dikenali; panel menjalankan SELECT, WITH, EXPLAIN, SHOW, dan di mode Tulis INSERT, UPDATE, DELETE, TRUNCATE, serta CREATE/ALTER/DROP TABLE atau INDEX")
	}
	cmd := words[0]
	switch {
	case readCommands[cmd]:
		if cmd == "EXPLAIN" {
			if err := checkExplained(words[1:], write); err != nil {
				return "", err
			}
		}
		return cmd, nil
	case writeCommands[cmd]:
		if name, ok := ddlTarget(words); !ok {
			return "", refuse(ErrNotAllowed, "%s tidak diizinkan di panel; hanya CREATE/ALTER/DROP TABLE atau INDEX", name)
		}
		if !write {
			return "", refuse(ErrNeedsWrite, "%s butuh mode Tulis", cmd)
		}
		return cmd, nil
	default:
		return "", refuse(ErrNotAllowed, "Perintah %s tidak diizinkan di panel", cmd)
	}
}

// ddlTarget reports whether a CREATE, ALTER or DROP touches a table or an
// index, and names the command for the refusal ("CREATE FUNCTION").
// Commands other than those three always pass.
func ddlTarget(words []string) (string, bool) {
	cmd := words[0]
	if cmd != "CREATE" && cmd != "ALTER" && cmd != "DROP" {
		return cmd, true
	}
	rest := words[1:]
	if cmd == "CREATE" {
		// CREATE [GLOBAL | LOCAL] [TEMP | TEMPORARY | UNLOGGED] TABLE, CREATE [UNIQUE] INDEX
		for len(rest) > 0 && (rest[0] == "GLOBAL" || rest[0] == "LOCAL" || rest[0] == "TEMP" || rest[0] == "TEMPORARY" || rest[0] == "UNLOGGED" || rest[0] == "UNIQUE") {
			rest = rest[1:]
		}
	}
	if len(rest) == 0 {
		return cmd, false
	}
	return cmd + " " + rest[0], rest[0] == "TABLE" || rest[0] == "INDEX"
}

// checkExplained applies the mode's rules to the statement EXPLAIN describes:
// EXPLAIN ANALYZE runs it.
func checkExplained(words []string, write bool) error {
	for len(words) > 0 && (words[0] == "ANALYZE" || words[0] == "ANALYSE" || words[0] == "VERBOSE" || words[0] == "(") {
		if words[0] == "(" {
			depth := 0
			for len(words) > 0 {
				if words[0] == "(" {
					depth++
				} else if words[0] == ")" {
					depth--
				}
				words = words[1:]
				if depth == 0 {
					break
				}
			}
			continue
		}
		words = words[1:]
	}
	if len(words) > 0 && words[0] == "EXPLAIN" {
		return refuse(ErrNotAllowed, "EXPLAIN di dalam EXPLAIN tidak diizinkan")
	}
	for len(words) > 0 && words[0] == "(" {
		words = words[1:]
	}
	_, err := classify(words, write)
	return err
}

type tokKind int

const (
	tokCode tokKind = iota
	tokSemi
	tokSpace // whitespace and comments
)

type token struct {
	kind       tokKind
	start, end int
}

func hasCode(toks []token) bool {
	for _, t := range toks {
		if t.kind == tokCode {
			return true
		}
	}
	return false
}

func isIdent(c byte) bool {
	return c == '_' || c == '$' || c >= 0x80 || ('a' <= c && c <= 'z') || ('A' <= c && c <= 'Z') || ('0' <= c && c <= '9')
}

func isIdentStart(c byte) bool {
	return c == '_' || c >= 0x80 || ('a' <= c && c <= 'z') || ('A' <= c && c <= 'Z')
}

// scan splits sql into code, semicolon and whitespace/comment tokens the way
// PostgreSQL's lexer delimits strings ('…', E'…', U&'…'), quoted identifiers,
// dollar-quoted strings and nested block comments, with
// standard_conforming_strings on (PostgreSQL's default since 9.1).
func scan(sql string) ([]token, error) {
	var toks []token
	n := len(sql)
	i := 0
	for i < n {
		c := sql[i]
		start := i
		switch {
		case c == ';':
			toks = append(toks, token{tokSemi, i, i + 1})
			i++
		case c == ' ' || c == '\t' || c == '\n' || c == '\r' || c == '\f' || c == '\v':
			for i < n && strings.IndexByte(" \t\n\r\f\v", sql[i]) >= 0 {
				i++
			}
			toks = append(toks, token{tokSpace, start, i})
		case c == '-' && i+1 < n && sql[i+1] == '-':
			for i < n && sql[i] != '\n' {
				i++
			}
			toks = append(toks, token{tokSpace, start, i})
		case c == '/' && i+1 < n && sql[i+1] == '*':
			depth := 0
			for {
				if i+1 >= n {
					return nil, refuse(ErrUnterminated, "Komentar /* tidak ditutup")
				}
				if sql[i] == '/' && sql[i+1] == '*' {
					depth++
					i += 2
				} else if sql[i] == '*' && sql[i+1] == '/' {
					depth--
					i += 2
					if depth == 0 {
						break
					}
				} else {
					i++
				}
			}
			toks = append(toks, token{tokSpace, start, i})
		case c == '\'':
			escapes := i > 0 && (sql[i-1] == 'E' || sql[i-1] == 'e') && (i < 2 || !isIdent(sql[i-2]))
			end, ok := quoted(sql, i, '\'', escapes)
			if !ok {
				return nil, refuse(ErrUnterminated, "String '…' tidak ditutup")
			}
			i = end
			toks = append(toks, token{tokCode, start, i})
		case c == '"':
			end, ok := quoted(sql, i, '"', false)
			if !ok {
				return nil, refuse(ErrUnterminated, "Nama dalam tanda kutip \"…\" tidak ditutup")
			}
			i = end
			toks = append(toks, token{tokCode, start, i})
		case c == '$' && (i == 0 || !isIdent(sql[i-1])) && dollarTag(sql, i) != "":
			tag := dollarTag(sql, i)
			j := strings.Index(sql[i+len(tag):], tag)
			if j < 0 {
				return nil, refuse(ErrUnterminated, "String %s…%s tidak ditutup", tag, tag)
			}
			i += len(tag) + j + len(tag)
			toks = append(toks, token{tokCode, start, i})
		case isIdent(c):
			for i < n && isIdent(sql[i]) {
				i++
			}
			toks = append(toks, token{tokCode, start, i})
		default:
			i++
			toks = append(toks, token{tokCode, start, i})
		}
	}
	return toks, nil
}

// quoted returns the index after the literal that opens at i, where a doubled
// quote stands for one and, with escapes, a backslash escapes the next byte.
func quoted(sql string, i int, q byte, escapes bool) (int, bool) {
	for i++; i < len(sql); i++ {
		switch {
		case escapes && sql[i] == '\\':
			i++
		case sql[i] == q:
			if i+1 < len(sql) && sql[i+1] == q {
				i++
				continue
			}
			return i + 1, true
		}
	}
	return 0, false
}

// dollarTag returns the "$tag$" opening at i, or "" when none does.
func dollarTag(sql string, i int) string {
	j := i + 1
	if j < len(sql) && sql[j] == '$' {
		return "$$"
	}
	if j >= len(sql) || !isIdentStart(sql[j]) {
		return ""
	}
	for j < len(sql) && sql[j] != '$' {
		if !isIdent(sql[j]) {
			return ""
		}
		j++
	}
	if j >= len(sql) {
		return ""
	}
	return sql[i : j+1]
}

// codeWords returns the statement's code tokens as upper-case words; an
// opening parenthesis before the first word is dropped, other punctuation is
// kept as its own word.
func codeWords(sql string, toks []token) []string {
	var out []string
	for _, t := range toks {
		if t.kind != tokCode {
			continue
		}
		w := sql[t.start:t.end]
		if len(out) == 0 && w == "(" {
			continue
		}
		out = append(out, strings.ToUpper(w))
		if len(out) == 32 {
			break
		}
	}
	return out
}
