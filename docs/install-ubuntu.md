# Installing Runova on Ubuntu Server 26.04

**English** · [Bahasa Indonesia](install-ubuntu.id.md)

Guide for server admins. End result: the Runova panel running on your server, complete with Docker, Caddy (automated HTTPS), and PostgreSQL 18, ready for users to deploy applications.

## 1. Server specifications

Runova only supports **Ubuntu Server 26.04 LTS, amd64**, installed clean (no pre-existing custom Docker, Caddy, or PostgreSQL setup).

| | Minimum | Recommended |
|---|---|---|
| CPU | 2 vCPU | 4 vCPU |
| RAM | 4 GB | 8 GB or more |
| Disk | 40 GB | 80 GB SSD or more |

Assumptions behind these numbers: the panel, Caddy, PostgreSQL, and Docker collectively use around 1 GB of RAM; the remainder is allocated across user applications. Java applications typically require 256–512 MB each, whereas Go or Rust binaries require significantly less. Disk space is consumed by Docker images (Java images are around 200–300 MB per version), uploaded artifacts, application data, databases, and logs. User storage is not subject to quotas, so monitor disk usage on the **Server** page.

The server requires outbound internet access to Ubuntu apt repositories and, during the initial deployment, to Docker Hub and `gcr.io` for base images. Once images are present, the panel loads nothing from external sources.

## 2. Setting up a fresh server

Log in as the user provided by your server provider (typically `ubuntu` or `root`), then:

```bash
# update system
sudo apt-get update && sudo apt-get -y full-upgrade

# admin user with sudo (skip if already created)
sudo adduser admin
sudo usermod -aG sudo admin
```

From **your local machine**, install an SSH key so you do not need a password for SSH:

```bash
ssh-keygen -t ed25519            # if you don't have a key yet
ssh-copy-id admin@IP-SERVER
ssh admin@IP-SERVER              # verify login works without an SSH password
```

`sudo` may still prompt for a password; deployment scripts allocate a terminal so passwords can be entered interactively. Only after SSH key authentication is confirmed working should you disable SSH password authentication (`PasswordAuthentication no` in `/etc/ssh/sshd_config.d/`). Never close your existing SSH session before testing a new one.

If the server uses an SSH port other than 22, the installer detects it and allows it in `ufw`.

## 3. Cloud provider firewall

In addition to `ufw` on the server (configured by the installer), many cloud providers have their own firewalls (security groups, network firewalls). Open the following ports there:

| Port | Purpose | Required |
|---|---|---|
| 22/tcp (or your custom SSH port) | SSH | yes |
| 80/tcp | Caddy: certificate challenges and HTTP-to-HTTPS redirects | yes, when using a domain |
| 443/tcp | Caddy: application and panel HTTPS (domain mode) | yes, when using a domain |
| panel port, default 8443/tcp | panel at `https://IP:8443` (IP mode) | yes, when without a domain |
| 20000-20999/tcp | application IP access for testing | optional, for testing via IP |
| 5432/tcp | external PostgreSQL access (DBeaver and similar tools) | optional |

Port 5432 only accepts connections from Docker networks and networks explicitly allowed in the panel; opening it in your cloud firewall does not expose databases to everyone, but do not open it unless needed.

## 4. DNS for panel domain

Skip this section if the panel will be accessed via `https://IP:8443`.

1. Create an **A** record: `panel.example.com` → server public IP.
2. **Cloudflare:** initially, set proxy status to **DNS only** (grey cloud) so Caddy can issue certificates. Once the panel is accessible over HTTPS, you may switch to **Proxied** with SSL/TLS set to **Full (strict)**.
3. Wait for DNS propagation (`dig +short panel.example.com` must return the server IP), then run the installer with `--domain`.

**Servers without a public IP** (behind NAT or in an office network): domains cannot be pointed directly. Use **Cloudflare Tunnel**: in Cloudflare Zero Trust, navigate to Networking → Tunnels, install `cloudflared` on the server, and add a Published application with Service URL `http://localhost:80` (Caddy). HTTPS is handled by Cloudflare. The panel remains accessible at `https://IP-LAN:8443` from the same local network.

**Local IP behind another server running nginx:** nginx keeps the certificates and forwards to Runova over HTTPS; follow [nginx in front of Runova](nginx-front-proxy.md) (it also explains `--trusted-proxy`, which makes the login limit and the activity log use each visitor's real IP).

## 5. Running the installation

Log in to the server via SSH, then run this **single command**:

```bash
# panel at https://panel.example.com (DNS already points to the server, see section 4)
curl -fsSL https://github.com/aribrilliantsyah/runova/releases/latest/download/get.sh | sudo bash -s -- --domain panel.example.com

# or panel at https://SERVER-IP:8443
curl -fsSL https://github.com/aribrilliantsyah/runova/releases/latest/download/get.sh | sudo bash -s -- --port 8443 --public-ip PUBLIC-SERVER-IP
```

The installer shows what it will do and asks **Continue? [y/N]**; type `y`. Each step then shows a spinner with the elapsed time, and the detailed output (apt and friends) goes to `/var/log/runova-install.log`. The first installation takes several minutes (apt installs Docker, Caddy, and PostgreSQL). If a step fails, the last 30 lines of that log are printed.

`get.sh` downloads the latest release from GitHub Releases, verifies `SHA256SUMS` for the archive and every contained file, then runs `install.sh`. All options after `--` are forwarded to the installer:

| Option | Meaning |
|---|---|
| `--domain <host>` | panel at `https://<host>` |
| `--port <n>` | panel at `https://<IP>:<n>` (default 8443; not 2019, 5432, 7070, 20000-20999) |
| `--public-ip <ip>` | server public IP (used in domain setup guide and database connection details) |
| `--trusted-proxy <ip/cidr>` | IP of an nginx in front of Runova whose `X-Forwarded-For` is believed; repeatable ([guide](nginx-front-proxy.md)) |
| `--skip-firewall` | do not touch `ufw` (firewall managed elsewhere) |
| `--yes` | non-interactive without prompts (for automation, e.g. cloud-init) |
| `--version x.y.z` | install specific version instead of latest (`get.sh` option) |

`--domain` and `--port` are mutually exclusive. Without either, it defaults to `--port 8443`.

**Note the admin password printed at the end.** That password is only displayed once. Example output:

```
Details: /var/log/runova-install.log
[1/13] Installing packages (docker.io, caddy, postgresql-18, ufw, jq, curl) ... done (1m54s)
[2/13] Creating user and folders ... done (0s)
...
[13/13] Verifying the installation (runova check) ... done (0s)

Runova 0.3.1 is ready.

Panel address: https://203.0.113.15:8443
Caddy creates the certificate itself, so the browser will show a warning; just continue.
Username     : admin
Password     : ••••••••••••••••••••
```

### More cautious: verify before running

```bash
V=0.3.1
curl -fsSLO https://github.com/aribrilliantsyah/runova/releases/download/v$V/runova-$V-linux-amd64.tar.gz
curl -fsSLO https://github.com/aribrilliantsyah/runova/releases/download/v$V/SHA256SUMS
sha256sum -c SHA256SUMS --ignore-missing
gh attestation verify runova-$V-linux-amd64.tar.gz -R aribrilliantsyah/runova   # optional, requires gh
tar xzf runova-$V-linux-amd64.tar.gz
cd runova-$V-linux-amd64
sudo ./install.sh --domain panel.example.com      # or --port 8443
```

### From source on your machine

To test unreleased changes, from a Runova source checkout (requires Go, `ssh`, `scp`):

```bash
scripts/deploy.sh admin@IP-SERVER --domain panel.example.com
```

This script verifies SSH, Ubuntu 26.04 amd64, and `sudo` on the server; builds a local release; copies it over; then runs the same installer. Additional options: `--version`, `-p/--ssh-port`, `-i/--identity`, and `RUNOVA_SSH_OPTS` (e.g. `RUNOVA_SSH_OPTS="-o ProxyJump=bastion"`).

The installer installs `docker.io`, `caddy`, `postgresql-18`, `ufw`, `jq`, and `curl` from Ubuntu apt; creates the `runova` user, encryption key, and systemd service; configures Caddy and PostgreSQL; opens ports in `ufw` (SSH is allowed before `ufw` is enabled); then executes `runova check`. A complete summary is in the [README](../README.md#installation).

## 6. After installation

1. Open the panel address printed by the installer. In IP mode, the certificate is self-issued by Caddy, so browsers will display a warning; proceed past it.
2. Log in as `admin` with the printed password, then change the admin password from the account menu.
3. Open the **Server** page and enter the server's **IP publik** (public IP). Without this, the domain setup guide in applications will only offer Cloudflare Tunnel.
4. On the **Pengguna** (Users) page, create users along with their quotas (number of applications, memory, CPU, databases).
5. Test deployment: log in as that user, upload a small Linux binary or `.jar`, complete the deploy wizard with **akses IP** (IP access) enabled, then navigate to `http://IP-SERVER:<port akses IP>`. If it opens, Docker, Caddy, and the deployment pipeline are working properly. The first `.jar` deployment pulls the `eclipse-temurin` image, requiring internet access and some time.

Lost admin password: `sudo -u runova env RUNOVA_DATA=/var/lib/runova runova admin reset-password admin`.

## Domain hosting for users

To allow users to assign addresses to their applications without managing DNS, register a wildcard domain. Example: domain `porto.devkriyanesia.my.id` in Cloudflare zone `devkriyanesia.my.id`; the panel remains at `runova.prodak.id` (the panel domain does not automatically become a hosting domain).

1. Fill in the server's **IP publik** (public IP) on the **Server** page (network settings).
2. In Cloudflare, zone `devkriyanesia.my.id` → DNS → Records → Add record: **Type** `A`, **Name** `*.porto`, **IPv4 address** server public IP, **Proxy status** **DNS only** (grey cloud), TTL Auto. Ports 80 and 443 must be open to the internet.
3. In Runova, navigate to **Server → Domain hosting**, enter `porto.devkriyanesia.my.id` (or `*.porto.devkriyanesia.my.id`), then click **Daftarkan** (Register). DNS status must show **DNS benar** (DNS correct); if not, the message explains the cause (no record yet, points to another IP, or Cloudflare proxy is active). A re-check button is available on the domain row.
4. Users select that domain in deploy wizard step 4 or the application's Domain tab, type a name (`cv-budi`), and the address `cv-budi.porto.devkriyanesia.my.id` is immediately used. Caddy issues certificates per subdomain (one per address) via HTTP/TLS-ALPN, so no Cloudflare API token is needed and no wildcard certificates are used.

**Let's Encrypt limits.** Let's Encrypt issues at most 50 new certificates per week for a single registered domain (`devkriyanesia.my.id`, including all its subdomains and the panel domain if sharing the same root). The Domain hosting card calculates an estimate of subdomains created in the last 7 days, showing a yellow warning starting at 40 and red at 50; this is Runova's estimate, not Let's Encrypt's internal count. If the limit is exceeded, new subdomain certificates are delayed until the following week.

**Cloudflare Proxied and Tunnel are not supported here:** Cloudflare Universal SSL certificates do not cover second-level subdomains such as `*.porto.devkriyanesia.my.id`.

Disabling domain hosting deactivates all of its subdomains and notifies their owners; deleting it is only possible after no subdomains are using it. Custom domains (e.g. `budi.com`) can only be added by admins.

## 7. Updates

The panel displays a banner for admins when a new release is available (disabled via **Server → Periksa pembaruan**). On the server:

```bash
sudo runova update            # to latest release, with confirmation prompt
sudo runova update --yes      # non-interactive
runova update --check         # check only: exit 0 up to date, 10 update available
```

`runova update` downloads `get.sh` from the target release, verifies its checksum against `SHA256SUMS`, then runs it; the result is identical to the `curl` installation command. The panel address remains unchanged. The installer is idempotent: binary and units are replaced, Runova is restarted; data, keys, admin accounts, applications, and panel address are untouched, and no new password is printed. Database schema is updated automatically; beforehand Runova copies `runova.db` to `/var/lib/runova/runova.db.bak-v<version>-<timestamp>`. Installed version: `runova version`.

Servers running Runova 0.2.0 or earlier do not yet have `runova update`; rerun the installation command (without `--domain`/`--port`, keeping the panel address):

```bash
curl -fsSL https://github.com/aribrilliantsyah/runova/releases/latest/download/get.sh | sudo bash -s --
```

### Rolling back to a previous version

The installer preserves the replaced binary in `/var/lib/runova/rollback/` (one version). If an update causes issues:

```bash
sudo runova rollback          # with confirmation prompt
```

The previous binary is restored. If the database was already migrated by the newer version, the pre-migration backup (`runova.db.bak-v<older schema>-*`) is restored: **panel data changes since the update (users, applications, domains, settings) are lost**, and the pre-rollback database is saved at `runova.db.before-rollback-<timestamp>`. Without a matching backup, `rollback` halts without making changes. Application containers and user PostgreSQL databases are untouched. Running `rollback` a second time rolls forward back to the original version.

## 8. Backups

What to back up:

| Item | Contents |
|---|---|
| `/etc/runova/secret.key` | **Encryption key.** All secrets (secret environment variables, database passwords) are encrypted with this key. **Without this key, secrets cannot be decrypted**, and there is no way to recover them. Store a copy in a separate location away from the server. |
| `/var/lib/runova` | Runova database (`runova.db`), uploaded artifacts, application data (`data/`), and daily database backups (`backup/`). |
| PostgreSQL | User databases. |

Example data directory backup (briefly stop Runova for `runova.db` consistency):

```bash
sudo systemctl stop runova
sudo tar czf /root/runova-data-$(date +%F).tar.gz -C / var/lib/runova etc/runova/secret.key
sudo systemctl start runova
```

Running applications are not interrupted; Runova reconnects when it starts up.

**PostgreSQL:** Runova already creates a `pg_dump` per database daily at 02:00 and retains it for 7 days in `/var/lib/runova/backup/<database>/`, but that resides on the same disk as the data. Copy that directory offsite, and/or create a full cluster dump:

```bash
sudo -u postgres pg_dumpall | gzip > /root/pg-semua-$(date +%F).sql.gz
```

Restoring to a new server: install Runova using the installer, stop `runova`, restore `/var/lib/runova` and `/etc/runova/secret.key` (ownership `runova:runova`), run `sudo systemctl start runova`, then restore databases from the **Database** page (backups) or `pg_restore`.

## 9. Troubleshooting

**First checks:**

```bash
sudo -u runova env RUNOVA_DATA=/var/lib/runova runova check
sudo journalctl -u runova -u caddy -u postgresql -n 100 --no-pager
```

`runova check` prints `ok` or `fail <name>: <reason>` for Docker, `docker-init`, Caddy, PostgreSQL, keys, data directories, and `pg_hba.d`; exits with code 1 if any check fails.

**Port 80/443 conflicts.** Installer or Caddy fails to start with `address already in use`. Find the process using them and stop it:

```bash
sudo ss -ltnp 'sport = :80 or sport = :443'
```

Usually pre-installed Apache or nginx: `sudo systemctl disable --now apache2` or `nginx`. Then `sudo systemctl restart caddy`.

**Certificate not yet issued.** Check `journalctl -u caddy`. Common causes: DNS does not point to the server yet (`dig +short domain`), port 80/443 is blocked in cloud firewall or `ufw`, Cloudflare record is still set to **Proxied** on first setup (switch to DNS only), or Let's Encrypt rate limits reached from too many attempts. The `failed to install root certificate ... sudo` message from Caddy in journal logs is harmless: Caddy is attempting to trust its internal certificate on the system.

**`ufw` locks out SSH.** The installer allows the SSH port prior to enabling `ufw` by inspecting `sshd -T`, `ss`, and `ssh.socket`. If you are still locked out, access the server via your hosting provider's web console and run:

```bash
sudo ufw allow <port-ssh>/tcp
sudo ufw status verbose
```

If the firewall is managed elsewhere, install with `--skip-firewall`.

**Application unreachable via IP:port.** Ensure ports 20000-20999 are open in `ufw` and cloud firewall, and that application IP access is enabled in the panel.

**Docker uses a network range conflicting with office network.** The installer moves Docker's default network pool to `172.31.0.0/16`, and Runova allocates `172.30.0.0/16` for user networks. If your network uses these ranges, configure `/etc/docker/daemon.json` and adjust `default-address-pools`.

**Installer halts midway.** Read the error message, resolve the issue, and rerun the same command; completed steps will be skipped.
