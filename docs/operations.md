# Operations: updates, rollback, installer details, uninstall

**English** · [Bahasa Indonesia](operations.id.md)

Day-2 reference for server admins. Backups and troubleshooting are in [Installing on Ubuntu](install-ubuntu.md#8-backups).

## Installer details

Requirements: clean Ubuntu Server 26.04 LTS (amd64), a user with `sudo`, and internet access to the Ubuntu apt repositories. The installer installs `docker.io`, `caddy`, `postgresql-18`, `ufw`, `jq`, and `curl` from official Ubuntu apt; no third-party repositories.

A step-by-step guide (specifications, cloud firewall, DNS, backups, troubleshooting) is in [Installing on Ubuntu](install-ubuntu.md).

`get.sh` downloads the latest release from [GitHub Releases](https://github.com/aribrilliantsyah/runova/releases), verifies `SHA256SUMS` (archive and its contents), then runs `install.sh` with your options. `--version 0.3.1` installs a specific version. Every release also includes build provenance [attestations](https://docs.github.com/en/actions/security-for-github-actions/using-artifact-attestations): `gh attestation verify runova-<version>-linux-amd64.tar.gz -R aribrilliantsyah/runova`.

From a source checkout (for instance, to test unreleased changes), `scripts/deploy.sh admin@IP-SERVER [options]` builds a local release, copies it over SSH, and runs the same installer.

`install.sh` options: `--domain <host>` or `--port <n>` (mutually exclusive; defaults to `--port 8443` without either), `--public-ip <ip>` (saved to settings; for servers behind NAT), `--skip-firewall` (do not touch `ufw`), `--yes` (non-interactive, for headless installations). For `--domain`, point that domain's DNS to the server first.

`--trusted-proxy <ip/cidr>` (repeatable) is for servers behind an nginx that forwards to Runova; see [nginx in front of Runova](nginx-front-proxy.md).

At the end, the installer prints the panel address, username `admin`, and a random password **once only**. If lost: `sudo -u runova env RUNOVA_DATA=/var/lib/runova runova admin reset-password admin`.

What the installer does: creates user `runova` (`docker` group), encryption key, `/etc/runova/runova.env`, and systemd unit `runova.service`; writes `/etc/caddy/Caddyfile` containing `{ admin localhost:2019 }` and runs Caddy with `--resume`; creates PostgreSQL role `runova` and enables `listen_addresses = '*'` and `include_dir pg_hba.d`; merges `default-address-pools` (`172.31.0.0/16`) into `/etc/docker/daemon.json`; opens ports 22 (or the actual `sshd` port), 80, 443, 20000-20999, 5432, and the panel port in `ufw`, then enables it if not already active.

If `/etc/caddy/Caddyfile` has already been modified (not the package default), the installer stops before making any changes. Migrate your sites first, or rerun with `--yes` to backup to `Caddyfile.runova-backup` and replace it.

`runova check` validates Docker, `docker-init`, Caddy, PostgreSQL, keys, and directory permissions (exits with 1 if any check fails); the installer runs it at the end.

## Updates

Admins are notified in the panel: when a newer release is available, a banner below the topbar on all pages (admin-only, non-dismissible) displays the new version, the `sudo runova update` command with a copy button, and a link to release notes. The banner automatically disappears after the server is updated.

The check is performed by `runova serve` (not the browser), first 1 minute after startup and then every 6 hours, following the redirect of `github.com/<repo>/releases/latest` just like `get.sh` (without `api.github.com`). Servers without internet access display nothing. Disable it on the **Server** page ("Periksa pembaruan" toggle, `update_check` setting). `dev` builds never check. `RUNOVA_REPO` overrides the repository (default `aribrilliantsyah/runova`) and `RUNOVA_GITHUB` overrides `https://github.com` (used for tests).

Update on the server:

```bash
sudo runova update                 # to latest release, with confirmation prompt
sudo runova update --yes           # non-interactive (required without terminal)
sudo runova update --version 0.3.1 # to specific version (cannot be older than installed)
runova update --check              # check only: exit 0 = up to date, 10 = update available, 1 = failed
```

`runova update` downloads `get.sh` from the target release along with `SHA256SUMS`, verifies the `get.sh` checksum, then runs `bash get.sh --version X --yes`. All installation logic remains centralized (`get.sh` → `install.sh`), producing identical results to the `curl` command below. The panel address does not change. The `curl` command remains available for initial installation and for servers on Runova 0.2.0 or older that do not yet have `runova update`:

```bash
curl -fsSL https://github.com/aribrilliantsyah/runova/releases/latest/download/get.sh | sudo bash -s --
```

The installer is idempotent: completed steps are skipped, binary and units are replaced, and services are restarted. Data, keys, admin accounts, applications, and panel address are left untouched, and no new password is printed. Pass `--domain`/`--port` only if you wish to change the panel address.

### Rolling back to a previous version

When installing another version, `install.sh` saves the currently installed binary to `/var/lib/runova/rollback/runova` (its version recorded in `rollback/VERSION`). Only one previous version is retained.

```bash
sudo runova rollback          # with confirmation prompt
sudo runova rollback --yes
```

`rollback` stops the service, restores the previous binary, and runs `runova check`. If the database was already migrated past the older binary's schema, it restores the latest backup `runova.db.bak-v<older binary schema>-*` (created automatically before migration); without a matching backup, `rollback` aborts without changing anything. **Panel data changes since the update (users, applications, domains, settings) are lost** in that scenario; the pre-rollback database is preserved at `runova.db.before-rollback-<unix timestamp>`. Application containers and user PostgreSQL databases are untouched. Replaced binaries are preserved in `rollback/`, so running `rollback` again (or `runova update`) rolls forward again.

The database schema is also migrated automatically when Runova starts. Prior to the first pending migration, Runova copies the database to `/var/lib/runova/runova.db.bak-v<older version>-<unix timestamp>` (mode 0600; 3 most recent backups retained) and logs the path. If migration fails, changes are rolled back and Runova refuses to start. Databases from newer Runova versions are rejected untouched, so reverting to an older binary requires restoring from backup. `runova db version` prints the current schema version and the highest version known to the binary:

```bash
sudo -u runova env RUNOVA_DATA=/var/lib/runova runova db version
```

## After a server reboot

Nothing needs to be done by hand. The installer enables `docker`, `containerd` (when present), `caddy`, `postgresql` and `runova` with `systemctl enable`, so they start at boot, and `runova.service` starts after Docker.

| What | Who brings it back |
|---|---|
| Panel, Caddy, PostgreSQL | systemd |
| Applications whose status was **running** or **starting** | Runova, at start-up |
| Applications **stopped** by a user, or **failed** (crash loop, did not become ready) | nobody: they stay down until someone presses Jalankan |

Docker stops the containers at shutdown with SIGTERM. Most servers (Spring Boot, a Go server with signal handling) exit with code 0, and the `on-failure` restart policy never restarts an exit 0, so Docker alone does not bring them back. When Runova starts it looks at every app that ought to run whose container is stopped and starts it again: status `starting`, then `running` once its port opens, with the same wait as Jalankan (JAR time limits, OOM and restart detection). Apps are started one every 5 seconds, not all at once, so many JVMs do not fight for the CPU. Each revived app raises one notification ("Aplikasi … dinyalakan lagi setelah server dimulai ulang") and one Activity entry; an app that cannot be started ends up `failed` with an error notification. Containers are never recreated: a deleted container still marks the app failed ("deploy ulang").

This happens once per start of Runova. A container that exits 0 while Runova is running is still treated as the app's own decision and marked stopped.

## Manual uninstall

There is no uninstall command yet. Manual procedure (removes **all** Runova data, including applications and user databases):

```bash
sudo systemctl disable --now runova
# remove application containers and user networks
sudo docker ps -aq --filter label=runova.app | xargs -r sudo docker rm -f
sudo docker network ls --format '{{.Name}}' | grep -E '^runova-u[0-9]+$' | xargs -r sudo docker network rm
# remove PostgreSQL databases and roles (one role per Runova user, plus runova role)
sudo -u postgres psql -c '\l'    # note databases <user>_<name>, then DROP DATABASE individually
sudo -u postgres psql -c '\du'   # DROP ROLE for each Runova user, then DROP ROLE runova
sudo rm -f /etc/postgresql/18/main/conf.d/runova.conf
sudo rm -rf /etc/postgresql/18/main/pg_hba.d      # and remove "include_dir pg_hba.d" line from pg_hba.conf
sudo systemctl reload postgresql
sudo rm -f /etc/systemd/system/runova.service /etc/systemd/system/caddy.service.d/runova.conf /usr/local/bin/runova
sudo rm -rf /etc/runova /var/lib/runova
sudo systemctl daemon-reload
sudo userdel runova
```

The Caddyfile (`{ admin localhost:2019 }`), `daemon.json`, `ufw` rules, and apt packages are not reverted; adjust manually if necessary.
