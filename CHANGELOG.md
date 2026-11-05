# Changelog

All notable changes to Runova. Versions follow [semantic versioning](https://semver.org/); the newest is on top. Indonesian: [CHANGELOG.id.md](CHANGELOG.id.md).

Upgrade any server with `sudo runova update` (servers on 0.2.0 or older: run the `curl … get.sh | sudo bash` command once). Database schema changes are migrated automatically after a backup (`runova.db.bak-v<N>-*`); `sudo runova rollback` returns to the previous version and its database.

## [Unreleased]

### Added
- **File manager in the File tab.** Clickable breadcrumb with the folder kept in the URL (`#/apps/<id>/files?path=storage/2026`), up button, filter, list and grid view (image thumbnails), sortable Name/Size/Modified with folders on top, folder item counts.
- Select several items (checkbox, Shift-click, Ctrl+A) to download them as one ZIP or delete them together; keyboard: arrows, Enter opens, Backspace goes up, Delete deletes (with confirmation), F2 renames.
- Row menu (⋯ and right-click): preview, download (folders as ZIP), rename, move to another folder, copy the app's path (`./storage/x.png`, `/data/storage/x.png`, `/app/storage/x.png`), delete. Saved folders show why they cannot be renamed, moved or deleted.
- Upload several files at once or drag them onto the list, with progress per file; new folder.
- Preview panel: PNG/JPEG/GIF/WebP images and text files up to 1 MB (line numbers), with size, detected type, time and paths. SVG, PDF and other files are download only.
- Footer with item count, folder size and the app's disk bar (warning at 80 %, full at 100 %). Phones get compact rows and an action sheet.
- API: `POST /api/apps/{id}/files/folder`, `POST …/files/move`, `GET …/files/zip`, `GET …/files/info`, `GET …/files/preview`; multi-file upload. Activity: `file.rename`, `file.mkdir`.

### Changed
- `DELETE /api/apps/{id}/files` takes `{"paths": [...]}`; one refused path removes nothing.
- Lanjutan cards share one layout: title and description on the left, save action at the right of the card footer.

### Fixed
- Folder tersimpan: "Tambah folder" and "Simpan folder" no longer stack against each other. The card has a clear empty state, rows show where each folder came from (detected or added), "Tambah folder" sits in the header (under the list on the review page) and "Simpan folder" in the footer, enabled only after a change and marked "Belum disimpan".

## [0.3.9] - 2026-11-05

### Added
- `CHANGELOG.md` and `CHANGELOG.id.md` covering every release.

### Changed
- GitHub release notes are taken from the version's changelog section (`scripts/changelog.sh`); a tag without a section fails before anything is built.

### Fixed
- README no longer calls 0.3.4 unreleased.

## [0.3.8] - 2026-11-05

Schema 4 → 5.

### Added
- **App workspace.** Each app has a workspace shown in the panel as `<app>/` (server: `/var/lib/runova/data/<user>/<app>`; container: `/data`, the working directory). Relative paths such as `./storage` land there and survive redeploys and restarts.
- **Saved folders, detected automatically** from `.env.example`, `application.properties` and `application.yml` (`STORAGE_PATH=./storage`, `app.upload-dir`, `spring.servlet.multipart.location`, `${user.dir}/uploads`, …), listed on the review page and editable in Lanjutan → Folder tersimpan. Absolute paths such as `/app/storage` are mounted from the workspace, so apps writing there no longer fail with "read-only file system". System paths, `/tmp` and `..` are refused; links are never followed.
- **Disk quota per app.** Users get a Disk quota (default 1024 MB) split across their apps; each app picks a size at deploy (presets 256/512/1024 MB). Workspaces are measured every 5 minutes and around deploys and uploads: warning at 80 %, "Disk penuh" and new deploys refused at 100 %. Running apps are never stopped (soft limit; a hard limit needs XFS project quota).
- **File tab**: browse, download, delete and upload files in the workspace (owner and admins only; downloads are always attachments).
- Disk usage on Umum, app cards, Pengguna and Server.

### Changed
- `PATCH /api/users/{id}` merges `quota` into the stored quota; fields left out keep their value.

### Upgrade
- Existing apps keep running with disk "belum dibatasi" until the owner picks a size in Lanjutan. Apps that write to an absolute path: add it under Lanjutan → Folder tersimpan and redeploy.

## [0.3.7] - 2026-11-05

### Fixed
- **Redeploying a Spring Boot app failed at "Menunggu aplikasi siap".** The 60 s wait was shorter than a Spring Boot start on 0.5 core (57 s alone, 69 s during a zero-downtime redeploy). JAR apps now wait 90 s per core (at least 3 min, at most 15 min), longer while the JVM is still logging its start-up.
- Restart and Start wait until the app's port is open: "Memulai…", then "Berjalan" or "Gagal" with the reason.
- A failed health check names how long the JVM ran, its last log line, and the port Tomcat started on.

### Changed
- Port labels: "Port aplikasi (di dalam container)", "Akses IP", and the internal host port only in the technical details. An empty Port on the Deploy page shows and stores the port that will be used.

## [0.3.6] - 2026-11-05

### Fixed
- Collapsing the sidebar left the page blank. The content now fills the window and the sidebar slides smoothly (no animation with reduced motion).

## [0.3.5] - 2026-11-05

### Fixed
- Typing in an environment variable field on the Deploy page lost focus after every character.

## [0.3.4] - 2026-11-05

Schema 2 → 4.

### Added
- **Deployment timeline.** Every deployment records its steps (upload checked → runtime image → network/data/port → create → start → wait until ready → switch traffic → stop old version) with timing, shown live.
- Plain-language failure diagnosis with a suggestion and technical detail (port not opened in time, OOMKilled, Java version, Caddy refusing the address, crash loop with last exit code).
- Container logs kept per deployment, so failed and stopped apps still have logs; full-width log viewer with wrap, search, level filter, folded stack traces, copy, download and jump to the first error.
- **Database variables filled automatically.** Choosing a database fills detected variables such as `DB_URL`, `DB_HOST`, `DB_USER`, `DB_PASSWORD`, `JDBC_URL`, `SPRING_DATASOURCE_*` (URL format matches the app), password included and never sent to the browser; each can be overridden. SSH tunnel instructions on the Database page.
- **Activity explorer**: search syntax (`aksi:` `pelaku:` `target:` `ip:` `hasil:`, phrases, exclusions), time range, histogram with zoom, facets, expandable rows, paging, live refresh.
- **Users list**: search, role filter with counts, sortable columns, paging, usage bars.

### Changed
- **Deploy page**: upload, then one review page with the app name on top, detected runtime/Java/port, address, database, required variables first, size presets, advanced options folded; afterwards the page follows the deployment live.
- A failed deploy keeps a running app "Berjalan" with a warning instead of "Gagal".
- IP access links use the LAN address when the public IP belongs to a proxy; DNS check offers "Pakai IP ini" behind NAT.
- `GET /api/users` is paged (`{items, total, …}`); `GET /api/usage` was removed.

### Fixed
- JAR apps get `SERVER_PORT` as well as `PORT`, so Spring Boot listens where Runova checks.
- Behind nginx, new hosting subdomains failed with `tls: internal error` / 502. With a trusted proxy set, Caddy issues every certificate from its internal CA; nginx may redirect port 80 itself. Domain status names the cause.
- Status badges, error cards, Server page cards.
- The binary env scan no longer reports TLS key-log labels (`CLIENT_HANDSHAKE_TRAFFIC_SECRET`) as variables.

### Upgrade
- Behind nginx: make sure Proxy di depan Runova holds the nginx IP (`sudo runova panel set --trusted-proxy <IP>`). nginx needs no change.

## [0.3.3] - 2026-11-04

### Added
- **Trusted proxies** (`sudo runova panel set --trusted-proxy <IP>`, Server card, installer `--trusted-proxy`): behind nginx, the login limit and activity log use each visitor's real IP; forged `X-Forwarded-For` is ignored.
- Guide [nginx in front of Runova](docs/nginx-front-proxy.md) with a tested example config, wildcard certificates through certbot and Cloudflare DNS, and troubleshooting for `ERR_TOO_MANY_REDIRECTS`.

## [0.3.2] - 2026-11-04

### Changed
- macOS-style app icon (rocket on a dark squircle) for the panel logo, favicon, touch icon and README.

## [0.3.1] - 2026-11-04

### Changed
- Terminal output of the installer, `get.sh` and the `runova` binary in English, with per-step progress and a log file (`/var/log/runova-install.log`).
- README and install guide in English by default; Indonesian in `*.id.md`.

### Fixed
- Interactive install with `curl … | sudo bash` stopped silently on Ubuntu 26.04 (sudo-rs). `get.sh` now asks on the terminal itself.

## [0.3.0] - 2026-11-04

### Added
- Admin-only banner when a newer release exists (checked by the server every 6 hours; can be turned off on Server).
- `sudo runova update [--check] [--version X.Y.Z] [--yes]` and `sudo runova rollback` (previous binary and database backup).

## [0.2.0] - 2026-11-03

Schema 1 → 2.

### Added
- **Hosting domains**: the admin registers a wildcard domain; users pick `<label>.<domain>` for their apps without touching DNS. DNS check, per-subdomain certificates, subdomain quota equal to the app quota.

## [0.1.0] - 2026-11-03

First release.

### Added
- One-command installer for Ubuntu Server 26.04 (Docker, Caddy, PostgreSQL 18, systemd, ufw), panel by domain (HTTPS) or IP:port.
- Upload `.jar` (Java 8–25), static or glibc Linux binaries, or `.tar.gz` with `.env.example`; runtime, port and environment detection; language logos.
- One hardened Docker container per app (read-only root, no capabilities, own network per user), zero-downtime or stop-start deploys, rollback, crash-loop detection, logs, web terminal, monitoring.
- Domains with automatic HTTPS through Caddy, IP access per app.
- PostgreSQL role per user, databases with daily backups, restore and download.
- Users with quotas (apps, memory, CPU, databases), login limit, activity log, notifications.
- Versioned database migrations with automatic backups.

[0.3.9]: https://github.com/aribrilliantsyah/runova/compare/v0.3.8...v0.3.9
[0.3.8]: https://github.com/aribrilliantsyah/runova/compare/v0.3.7...v0.3.8
[0.3.7]: https://github.com/aribrilliantsyah/runova/compare/v0.3.6...v0.3.7
[0.3.6]: https://github.com/aribrilliantsyah/runova/compare/v0.3.5...v0.3.6
[0.3.5]: https://github.com/aribrilliantsyah/runova/compare/v0.3.4...v0.3.5
[0.3.4]: https://github.com/aribrilliantsyah/runova/compare/v0.3.3...v0.3.4
[0.3.3]: https://github.com/aribrilliantsyah/runova/compare/v0.3.2...v0.3.3
[0.3.2]: https://github.com/aribrilliantsyah/runova/compare/v0.3.1...v0.3.2
[0.3.1]: https://github.com/aribrilliantsyah/runova/compare/v0.3.0...v0.3.1
[0.3.0]: https://github.com/aribrilliantsyah/runova/compare/v0.2.0...v0.3.0
[0.2.0]: https://github.com/aribrilliantsyah/runova/compare/v0.1.0...v0.2.0
[0.1.0]: https://github.com/aribrilliantsyah/runova/releases/tag/v0.1.0
