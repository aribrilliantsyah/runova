# Architecture and how it works

**English** · [Bahasa Indonesia](architecture.id.md)

Technical background: what runs where, how isolation, quotas, databases and files work. For the plain-language overview see the [README](../README.md).

## Design goals

Platforms like Vercel or Railway build applications from source code and run them in their cloud. Many internal teams already have finished build artifacts from their own pipelines (`.jar` files from Maven, Go or Rust binaries from CI) and simply need a single place on their office server to run them cleanly:

- **Upload, don't build.** Runova does not touch source code. It inspects uploaded files: file type, minimum required Java version, port, and referenced environment variables.
- **Isolated per user.** Each application runs in its own Docker container with a memory limit; all apps share the server's CPU cores with equal weight. Users cannot see or touch applications belonging to other users.
- **Test via IP first, then assign a domain.** Applications can be accessed at `server-IP:port` for testing. An admin registers a wildcard domain once, after which users can assign their application an address like `name.registered-domain` without managing DNS; Caddy automatically provisions HTTPS certificates, and IP access can be closed.

## What can run

Admins choose which runtimes are allowed on the server. Runova detects the file type upon upload and displays its language logo.

| Type | Example languages | User options | Base image |
|---|---|---|---|
| **JAR** | Java, Kotlin, Scala, Clojure, Groovy | Java 8, 11, 17, 21, 25 | `eclipse-temurin:<version>-jre` |
| **Static binary** | Go, Rust (musl), Zig, Nim, Odin, V | – | `gcr.io/distroless/static-debian12` |
| **glibc binary** | Rust, C, C++, .NET, Swift, Haskell, Crystal, Dart, OCaml, Bun, Deno | – | `ubuntu:26.04` |

- `.jar` files have their bytecode version inspected. Java versions too old for the file cannot be selected, accompanied by an explanation.
- Binaries are validated for format (must be Linux ELF) and architecture (must match the server). Windows `.exe` files or macOS binaries are rejected with clear error messages.

## How a deploy works

Deployment is divided into four steps:

1. **Upload.** Drag and drop the file onto the page. Inspection results appear as a checklist.
2. **Runtime.** Select Java version (for `.jar`), JVM options, and the port the application listens on.
3. **Environment.** Variables referenced by the application appear as a form. Secret values are stored encrypted.
4. **Resources & access.** Give the app memory and disk from the remaining account quota (CPU is shared, not chosen). Enable IP access for testing, and set a domain when ready.

The **Deploy** button is only enabled after all required fields are satisfied. The summary panel indicates what is still missing.

Once running, the application page provides controls (upload new version, restart, stop, terminal), environment variables, domains, deployment history with rollback support, logs, monitoring, files, and advanced settings.

## Application files

Every application has a **workspace**, shown in the panel as `<app>/` (for example `starboom/`). It is the container's working directory (`/data`, also in `RUNOVA_DATA_DIR`), lives on the server at `/var/lib/runova/data/<user>/<app>/`, and keeps its files across redeploys, restarts and rollbacks. The rest of the container is read-only.

- An app that writes to a relative path such as `./storage` already writes into the workspace (`starboom/storage`).
- An app that writes to an absolute path such as `/app/storage` or `/var/www/uploads` gets that path **mounted automatically** from a workspace folder (`starboom/storage`). Runova finds such folders in `.env.example`, `application.properties` and `application.yml` (`spring.servlet.multipart.location`, `*.upload-dir`, `*.storage.*path*`, `STORAGE_PATH`, `UPLOAD_DIR`, …) without running the app; the review page lists them ticked under **Folder tersimpan**, and they can be changed there or in the app's **Lanjutan** tab. System folders (`/etc`, `/usr`, `/proc`, …), `/tmp` and the artifact itself are refused.
- The **File** tab lists the workspace, downloads and deletes files, and uploads small files.

## Users and quotas

- **Admin** registers users. There is no self-registration.
- Each user only sees and manages their own applications.
- Admins configure quotas per user: **number of applications, memory, number of databases** (default 3) **and disk** (default 1 GB). Users share that quota out across their applications; CPU is not part of the quota, every app uses all cores; each app gets a disk size at deploy (256 MB by default, changeable in **Lanjutan**).
- Disk counts everything in an app's workspace (its saved folders included); artifacts, logs and PostgreSQL databases do not count. Runova measures every workspace every 5 minutes and before each deploy. At 80% the owner is warned; at 100% the app shows **Disk penuh**, the owner gets an error notification and new deploys of that app are refused until files are deleted or the size is raised. The running app is **not** stopped, so no data is lost. This is a soft limit: an app can write past it until the next measurement. A hard limit needs a filesystem with project quotas (XFS `prjquota`), which stock Ubuntu installs do not have; that is future work. Admins see server disk use and the apps' share on the **Server** page.
- Applications exceeding their memory limit are killed and restarted by Docker without affecting other applications.

## Database

Runova installs **PostgreSQL 18** on the same server (official Ubuntu 26.04 package).

- Each user receives **one PostgreSQL account** upon admin registration, for example `budi`.
- Users create their own databases in the panel, with names prefixed by their username (`budi_billing`, `budi_auth`). The count is capped by admin quotas, defaulting to **3 per user**.
- Only the owner can access their database. User accounts have no superuser privileges, cannot create roles, and have connection limits.
- During deployment, users select a database for their application (or none). Runova automatically populates `DATABASE_URL`, `SPRING_DATASOURCE_URL`, and `PGHOST`/`PGPORT`/`PGUSER`/`PGPASSWORD`/`PGDATABASE`, plus every database variable the artifact names (`DB_URL`, `DB_USER`, `DB_PASSWORD`, `POSTGRES_PASSWORD`, `JDBC_URL`, `SPRING_R2DBC_URL`, `TYPEORM_*`, …), password included; those rows show as "Otomatis" and need no typing. A value the user enters for one of them wins. The host is `host.docker.internal`: this server, as seen from inside the container (what `localhost` is on the server). Multiple applications belonging to the same user may share the same database.
- Host, port, database name, and password can be viewed and copied on the **Database** page, for example to connect via DBeaver from networks permitted by the admin, or through an SSH tunnel (`ssh -N -L 5433:127.0.0.1:5432 <user>@<server>`, then `localhost:5433`).

## Technical architecture

| Component | Choice |
|---|---|
| Target server | Ubuntu Server 26.04 LTS, `linux/amd64` |
| Runova | Single Go binary, embedded frontend |
| App isolation | Docker, one container per application, memory limit, shared CPU with equal weight |
| Domains & HTTPS | Caddy, automated certificates |
| Database | PostgreSQL 18 from Ubuntu apt, systemd service on the host. Only accepts connections from Docker networks and networks allowed by admin |
| IP access | Containers only bind to `127.0.0.1`. On: Caddy opens the application IP access port and forwards traffic. Off: the port is closed |
| App ports | Allocated automatically from admin-configured range |
| Server public IP | Set once by admin. If empty, domain setup guide only offers Cloudflare Tunnel |

Detailed specifications are in [`docs/spec.md`](spec.md). Implementation plans per section are in `docs/superpowers/plans/`.

## Panel interface notes

- Light and dark modes follow the system theme and can be overridden from the account menu; dense layout inspired by Dokploy and Railway, without gradients.
- The panel does not load any external assets: fonts, icons, logos, and xterm.js are bundled in `web/vendor/`, and CSP headers reject outside sources. The panel works on servers without outbound internet access.
- On mobile, menus move to the bottom navigation bar. During deployment, the bottom navigation is replaced with **Kembali** (Back) and **Lanjut** (Next) buttons.
- Icons: [Lucide](https://lucide.dev). Language logos: [Devicon](https://devicon.dev).
