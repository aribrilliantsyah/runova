# Development

**English** · [Bahasa Indonesia](development.id.md)

For people working on Runova itself: status, running it locally, tests, and releasing.

## Project status

**Status: feature-complete; installer proven on Ubuntu 26.04 with systemd in a container, not yet on bare metal or real VMs.** The `runova` binary bundles a JSON API and an embedded web panel; installer, updates (`runova update`/`rollback`), release scripts, and system tests are implemented (see Testing status below for what is verified and what is not).
The [interface preview](../preview-dashboard.html) (open that file in a browser) serves as the design reference.

## Testing status

Transparent about what is proven. **Proven** (`test/system/run.sh`, repeatable with a single command): `scripts/deploy.sh` installs Runova over SSH onto real Ubuntu 26.04 running systemd as PID 1 (privileged container, without VMs) using real `apt-get install` from Ubuntu repositories (`docker.io` 29.1.3, `caddy` 2.6.2, `postgresql-18` 18.6, `ufw`), Docker-in-Docker, and `ufw` active with its rules. In this environment, `runova check` passes completely, API login through Caddy succeeds, users and databases are created in apt PostgreSQL, sample binaries run in containers and are accessible via external IP access ports, a second installation does not print passwords or modify keys, data, or apps, `systemctl restart runova caddy postgresql` restores panel and apps, and `runova update` followed by `runova rollback` switches versions with login, users, databases, and apps intact. With real GitHub releases: installation via `curl`, upgrades 0.1.0 → 0.2.0 → 0.3.0 (automatic schema migrations and backups), `runova update --check`, and `rollback` to 0.2.0. In addition, there are `bats` tests with stubs and `shellcheck` for all scripts, as well as complete end-to-end product flows (`test/e2e`: JAR and binary deployment, zero-downtime deploy, cross-user isolation, backup and restore, subdomains from domain hosting over HTTPS) with real Docker, `caddy:2.6.2`, and `postgres:18`. **Unproven:** real server kernels and booting (containers use the host developer kernel; `systemd-modules-load` fails in containers causing systemd to report `degraded`), server reboots (units use `Wants`/`After`, `Restart=on-failure`, and Caddy `--resume`), effects of `ufw` on Docker iptables rules on a server kernel, `ufw` outside containers, and Let's Encrypt certificates for real domains (inside containers Caddy only issues internal certificates). Test on a staging server first.

## Trying it locally

Requires Go 1.27. Pure Go without cgo, so no C compiler is needed.

```bash
CGO_ENABLED=0 go build -o runova ./cmd/runova

export RUNOVA_DATA=$(mktemp -d)
export RUNOVA_SECRET_KEY=$RUNOVA_DATA/secret.key RUNOVA_LISTEN=127.0.0.1:7071

./runova keygen                  # generate encryption key (0600)
./runova admin create admin      # prints random Username and Password once
./runova serve &                 # API at http://127.0.0.1:7071

curl -s -c jar -H 'Content-Type: application/json' \
-d '{"username":"admin","password":"<password from admin create step>"}' \
127.0.0.1:7071/api/login
curl -s -b jar 127.0.0.1:7071/api/me
curl -s -b jar 127.0.0.1:7071/api/activity
```

Other subcommands: `runova admin reset-password <username>` generates a new random password. `serve` refuses to start if the key file is missing, so run `keygen` first. Environment variables: `RUNOVA_LISTEN` (default `127.0.0.1:7070`), `RUNOVA_DATA` (`/var/lib/runova`), `RUNOVA_SECRET_KEY` (`/etc/runova/secret.key`), `RUNOVA_DOCKER_HOST` (`unix:///var/run/docker.sock`). `serve` also refuses to start if Docker is unreachable.

The web panel is served from the same binary: open `http://127.0.0.1:7071/` and log in with the admin credentials from the step above. Without Docker, Caddy, and PostgreSQL on a development machine, run with fake backends (rejected if `RUNOVA_DATA=/var/lib/runova`):

```bash
RUNOVA_FAKE_BACKENDS=1 ./runova serve
```

All API mutations use `Content-Type: application/json`. Run `go test ./...` to test the server. Panel tests (Chromium via Playwright, zero JS dependencies in production):

```bash
node web/test/unit.mjs            # test pages web/test/*.test.html
node web/test/smoke-deploy.mjs    # login, wizard, deploy
node web/test/smoke-dbenv.mjs     # database variables filled automatically, override, SSH tunnel hint
node web/test/smoke-app.mjs       # app detail tabs, logs, terminal
node web/test/smoke-admin.mjs     # users, databases, activity, server
node web/test/smoke-layout.mjs    # 5 viewport widths, light and dark, accessibility
node web/test/smoke-lifecycle.mjs # terminal, dialogs, and sockets do not outlive page/session
node web/test/smoke-hosting.mjs   # domain hosting: admin registers, user picks address, subdomain quota
node web/test/smoke-update.mjs    # update banner (admin yes, normal user no), toggle, no horizontal scroll
```

Icons are generated from `web/icons.txt` with `go run ./cmd/genicons` (results are committed).

## Testing the installer and local end-to-end tests

Without VMs. The installer is tested with `bats` and `shellcheck` in Docker (stubs replace `apt-get`, `systemctl`, `ufw`, and others, leaving this machine untouched):

```bash
test/bats.sh
```

System tests install real Runova on Ubuntu 26.04 with systemd as PID 1 (privileged container; real `apt-get`, `systemctl`, `ufw`, and Docker-in-Docker) via `scripts/deploy.sh`, then verify `runova check`, panel login, users and databases, deploying sample binaries via IP access ports, reinstalling, restarting services, and `runova update` followed by `runova rollback` against a local release server. Requires Docker and internet access; takes ~10 minutes. Containers are cleaned up at the end (`KEEP=1` to retain them):

```bash
test/system/run.sh
```

Product flows (login, users, databases, deploying binaries and JARs, zero-downtime deploy, cross-user isolation, restarting Runova, backup and restore, subdomains from domain hosting over HTTPS) are tested with Runova from source, real Docker, `caddy:2.6.2` (Ubuntu 26.04 package version; override via `CADDY_IMAGE`), and `postgres:18` in containers, rootless:

```bash
test/e2e/stack.sh up && test/e2e/run.sh && test/e2e/stack.sh down
```

With the stack up, `test/e2e/nginx.sh` puts nginx 1.18 in front of it with `docs/examples/nginx-runova.conf` (the file the guide embeds) and checks panel access without a redirect loop, the login limit per real client IP (two client containers, forged `X-Forwarded-For`), WebSocket, a new hosting subdomain without any public certificate (internal CA, domain status active), the IP in the activity log, and port 80 forwarded to Runova with nginx verifying Caddy's internal CA.

`stack.sh up` uses panel port 8443, Caddy admin 2019, PostgreSQL 55432, and IP access ports 20000+ on this host, so stop other services using those ports. `RUNOVA_DEV_CHMOD=1` creates application data directories with 0777 permissions instead of `chown` (dev process is not root); rejected for `RUNOVA_DATA=/var/lib/runova`. Without `down`, leftover `runova-*` containers and `test/e2e/.cache/state` directory remain; `run.sh` requires a freshly started stack. On first run, `run.sh` builds the sample Spring Boot JAR in `maven:3-eclipse-temurin-21` and caches it in `test/e2e/.cache/`.

Building release archives: `scripts/release.sh 0.4.0` outputs `dist/runova-0.4.0-linux-amd64.tar.gz` and `dist/SHA256SUMS`.

## Screenshots

The screenshots in the README and `docs/screenshots/` are captured from the real panel (`node web/test/screenshots.mjs`, which seeds a fake-backend server), not from the preview. Every view is taken twice, into `docs/screenshots/light/` and `docs/screenshots/dark/` under the same file name; the README shows the light set and folds the dark set below it. If another local stack holds ports 20000+, set `SHOT_PORT_MIN` (for example `SHOT_PORT_MIN=34000`). PNGs are compressed with `optipng`.

## Releasing a new version

Every change is recorded in [CHANGELOG.md](../CHANGELOG.md) (Indonesian: [CHANGELOG.id.md](../CHANGELOG.id.md)). To release, add a `## [X.Y.Z] - date` section to both files, then tag. Releases are built by GitHub Actions (`.github/workflows/release.yml`) on every `v*.*.*` tag: the release notes come from that changelog section (`scripts/changelog.sh`; a tag without one fails), then tests, `scripts/release.sh`, attestations, and a GitHub Release containing archives, `SHA256SUMS`, and `get.sh`.

```bash
git tag v0.4.0
git push origin v0.4.0
```

Every push to `main` also runs CI (`.github/workflows/ci.yml`): gofmt, vet, unit tests, integration tests against real Docker, PostgreSQL 18, and Caddy, as well as bats and shellcheck.
