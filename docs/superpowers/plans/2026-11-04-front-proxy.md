# Front Proxy (nginx mode A) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Runova behind an nginx that terminates TLS and forwards over HTTPS: no redirect loop, real client IP for the login throttle and the activity log.

**Architecture:** New setting `TrustedProxies` flows into the Caddy `reverse_proxy` handlers (`trusted_proxies`), so Caddy keeps the incoming `X-Forwarded-For` and appends the peer. `auth.ClientIP` walks that header right to left and skips trusted proxies. A shared nginx example file is the single source for the guide and the Docker lab test.

**Tech Stack:** Go 1.26, Caddy 2.6.2, bash/bats, Playwright (web/test), Docker, nginx 1.18.

**Spec:** `docs/superpowers/specs/2026-11-04-front-proxy-design.md`

## Global Constraints

- Setting `TrustedProxies []string` json `trusted_proxies`, max 10 entries, default `[]`, entries are IP or CIDR, `0.0.0.0/0` and `::/0` rejected with a clear message.
- CLI/installer output English; panel/API messages Indonesian; docs English (`*.md`) + Indonesian (`*.id.md`).
- `runova panel set --trusted-proxy <ip/cidr>` repeatable; `--trusted-proxy ""` clears. Installer/`get.sh` option `--trusted-proxy <ip/cidr>`.
- Caddy 2.6.2 only (no PROXY protocol). Do not push.

## Review Focus

- Client forges `X-Forwarded-For: fake, real, nginx` → `real`. Pinned in Task 1 and the e2e.
- Everything in XFF trusted → leftmost. Task 1.
- IPv4-mapped / zoned / invalid XFF entries get one canonical spelling or fall back to the peer. Task 1.
- `0.0.0.0/0` as trusted proxy would let anyone spoof. Task 2 validation test.
- Old DB without the key decodes to `[]`, not nil. Task 2.

---

### Task 1: `auth.ClientIP` right-to-left

**Files:** Modify `internal/auth/auth.go`, `internal/auth/auth_test.go`, `internal/api/api.go`, `internal/api/session.go`.

**Interfaces:**
- Produces: `auth.ClientIP(r *http.Request, trusted []netip.Prefix) string`; `auth.ParseTrusted(nets []string) []netip.Prefix` (IP → /32 or /128, bad entries skipped); `(*server).clientIP(r) string` in `internal/api` (reads `store.Settings().TrustedProxies`, falls back to none on error).

- [ ] **Step 1:** Replace `TestClientIP` with a table using `trusted`: no trusted (old cases kept), one trusted `10.0.0.1`, chain `203.0.113.7, 10.0.0.1` → `203.0.113.7`, forged `1.1.1.1, 203.0.113.7, 10.0.0.1` → `203.0.113.7`, all trusted `10.0.0.2, 10.0.0.1` → `10.0.0.2`, CIDR `10.0.0.0/8`, IPv6 `2001:db8::5, 10.0.0.1`, invalid entry `junk, 10.0.0.1` → peer `127.0.0.1`, non-loopback peer ignores XFF.
- [ ] **Step 2:** `go test ./internal/auth` → FAIL (signature).
- [ ] **Step 3:** Implement: from the right, skip entries that are loopback or in `trusted`; return first other (canonical `Unmap().WithZone("")`); an unparsable entry stops the walk and falls back to peer host; all skipped → leftmost parsed entry.
- [ ] **Step 4:** Add `server.clientIP`, use it in `login` and `record`. `go test ./internal/auth ./internal/api` → PASS.
- [ ] **Step 5:** Commit `auth: client IP walks X-Forwarded-For from the right past trusted proxies`.

### Task 2: Setting `TrustedProxies` + validation + API

**Files:** Modify `internal/store/settings.go`, `internal/store/*_test.go`, `internal/api/api_test.go`.

**Interfaces:** `store.MaxTrustedProxies = 10`; `Settings.TrustedProxies []string`; `Validate` messages in Indonesian ("Proxy tepercaya tidak valid: …", "Proxy tepercaya maksimal 10", "Proxy tepercaya tidak boleh mencakup seluruh internet (0.0.0.0/0 atau ::/0)"); nil normalized to `[]` on read/write; entries stored as given-but-canonical (`netip` string).

- [ ] **Step 1:** Test `TestSettingsTrustedProxies`: default `[]` non-nil; round trip; `10.0.0.1`, `10.0.0.0/8`, `2001:db8::/32` accepted; `nope`, `10.0.0.0/33`, `0.0.0.0/0`, `::/0`, `0.0.0.0/0` spelled `0.0.0.0/00` rejected; 11 entries rejected; JSON lacking the key decodes to `[]`. API test: PUT with proxies then GET returns them, and a bad one → 400.
- [ ] **Step 2:** FAIL. **Step 3:** Implement. **Step 4:** `go test ./internal/store ./internal/api` PASS. **Step 5:** Commit `settings: trusted proxies`.

### Task 3: Caddy builder + CaddyState + integration

**Files:** Modify `internal/caddy/config.go`, `config_test.go`, `integration_test.go`, `internal/apps/caddystate.go` (+test).

**Interfaces:** `caddy.State.TrustedProxies []string`; every `reverse_proxy` handler (panel domain, sites, IP listeners, panel IP) gets `"trusted_proxies": [...]` only when non-empty; sorted for determinism.

- [ ] **Step 1:** Builder tests: all routes carry the field when set, none when empty, shuffle-stable. `CaddyState` test copies the setting.
- [ ] **Step 2:** Integration subtest (`RUNOVA_CADDY_TEST=1`): Caddy on host network; client sends `X-Forwarded-For: 198.51.100.9` from 127.0.0.1; with `TrustedProxies: ["127.0.0.1"]` upstream sees `198.51.100.9, 127.0.0.1`, without it sees `127.0.0.1`.
- [ ] **Step 3:** Implement; `go test ./internal/caddy ./internal/apps`, then `RUNOVA_CADDY_TEST=1 go test -count=1 ./internal/caddy`. **Step 4:** Commit `caddy: trusted_proxies on reverse proxies`.

### Task 4: CLI, installer, get.sh

**Files:** Modify `cmd/runova/panel.go`, `main.go` (usage), `panel_test.go`, `install.sh`, `get.sh` comment, `test/install.bats`, `test/get.bats`.

**Interfaces:** `panel set --trusted-proxy <v>` repeatable (custom `flag.Value`), empty value clears, valid alone or combined; prints `Trusted proxies: a, b` (or `Trusted proxies: none`) when used alone. Installer: `TRUSTED_PROXIES` array, `--trusted-proxy`, validated with `valid_ip` or `ip/prefix`, passed to `panel set`; usage text updated.

- [ ] **Step 1:** Go test `TestPanelSetTrustedProxy` (set two, keep with other options, clear, invalid, `0.0.0.0/0` rejected, rejected command changes nothing). bats: parse + forwarded in `panel set` call; invalid rejected; get.sh forwards.
- [ ] **Step 2:** FAIL. **Step 3:** Implement. **Step 4:** `go test ./cmd/runova`, `test/bats.sh` PASS. **Step 5:** Commit.

### Task 5: Panel card

**Files:** Modify `web/js/pages/server.js`, `web/test/smoke-admin.mjs` (or a hosting/server smoke that already covers the Server card).

- [ ] **Step 1:** Playwright: Server card shows textarea "Proxy di depan Runova", saves two entries, reload shows them; bad entry shows the API error toast.
- [ ] **Step 2:** Implement textarea (one per line, same parsing as `pg_allowed_nets`) with hint. **Step 3:** run web tests. **Step 4:** Commit.

### Task 6: nginx example, guide, e2e lab

**Files:** Create `docs/examples/nginx-runova.conf`, `docs/nginx-front-proxy.md`, `docs/nginx-front-proxy.id.md`, `test/e2e/nginx.sh`; modify README(.id).md, `docs/install-ubuntu(.id).md`, `test/bats.sh` only if shellcheck globs need it.

**Interfaces:** The example conf uses placeholders `RUNOVA_ADDR`, `PANEL_NAME`, `WILDCARD_NAME`, `CERT_PATH_*` substituted by `sed`; both docs embed the file verbatim and a Go-free check in `nginx.sh` fails when the embedded block differs from the file (extract fenced block after marker `<!-- nginx-runova.conf -->`). `nginx.sh` runs nginx 1.18 in `ubuntu:22.04` plus two curl clients with distinct IPs on a Docker network and the e2e stack on the host.

- [ ] **Step 1:** Write `nginx.sh` asserting every spec point: panel 200 no loop; HTTP→HTTPS exactly one redirect; client A 5 wrong passwords → 429; client B login ok; A forging XFF still 429; WebSocket (logs); hosting subdomain through nginx; activity IP equals client A/B container IP, not nginx.
- [ ] **Step 2:** Run → FAIL before conf/settings are wired. **Step 3:** Write conf + docs (with Cloudflare DNS-01 certbot section, troubleshooting, links). **Step 4:** Run → PASS. **Step 5:** Commit.

### Task 7: Full verification

- [ ] `gofmt -l .`, `go vet ./...`, `go test -count=1 ./...`, `CGO_ENABLED=0 go build ./...`; caddy integration 5×; `test/bats.sh`; web tests; e2e stack + `run.sh` + `nginx.sh`; `test/system/run.sh`; notes in `docs/superpowers/notes/front-proxy.md`; `git status` clean.
