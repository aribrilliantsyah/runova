<p align="center">
  <img src="docs/brand/runova-app-icon.svg" width="128" height="128" alt="Runova">
</p>

<h1 align="center">Runova</h1>

<p align="center"><b>English</b> · <a href="README.id.md">Bahasa Indonesia</a></p>

<p align="center">
  Run your finished apps on your own server.<br>
  Upload a <code>.jar</code> or a Linux binary, and Runova starts it, gives it a domain with HTTPS, and keeps it running.
</p>

<p align="center">
  <a href="https://github.com/aribrilliantsyah/runova/releases/latest"><img src="https://img.shields.io/github/v/release/aribrilliantsyah/runova?label=release" alt="Latest release"></a>
  <a href="https://github.com/aribrilliantsyah/runova/actions/workflows/ci.yml"><img src="https://github.com/aribrilliantsyah/runova/actions/workflows/ci.yml/badge.svg?branch=main" alt="CI"></a>
  <a href="LICENSE"><img src="https://img.shields.io/badge/license-Apache--2.0-blue" alt="Apache-2.0 License"></a>
</p>

<p align="center">
  <img src="docs/screenshots/light/desktop-apps.png" alt="Application list in Runova: account quota, application cards with language logos, status, domain, and IP access" width="860">
</p>

## What you get

- **Upload a build, it runs.** Drop a `.jar` (Java, Kotlin, Scala, …) or a Linux binary (Go, Rust, C++, .NET, …). Runova reads the file, picks the right runtime, and starts it. No source code, no build step.
- **Your own domain, HTTPS included.** Give an app an address like `billing.yourcompany.com`, or let your team pick names under a domain you registered once. Certificates are automatic.
- **A database for every user.** PostgreSQL is installed for you. Each user creates databases in the panel, and the connection details are filled into the app automatically.
- **Files and queries in the browser.** Browse, upload and download an app's files, look at tables, and run SQL without installing anything.
- **Everyone stays in their lane.** Each app runs in its own container with memory, CPU and disk limits. Users only see their own apps, and the admin sets quotas.
- **See what happened.** Deploy steps with timings, live logs, a terminal, rollback to an earlier version, and an activity log.
- **One command to update.** `sudo runova update`, and `sudo runova rollback` if you change your mind.

## A quick look

### Light mode

<table>
  <tr>
    <td width="50%"><img src="docs/screenshots/light/desktop-deploy.png" alt="Deploy page: the uploaded file is detected, Java version and port are chosen, a summary on the right shows everything is ready"><br><sub><b>Deploy.</b> Upload a file, check what was detected, press Deploy.</sub></td>
    <td width="50%"><img src="docs/screenshots/light/desktop-deployments.png" alt="Deployment details with a step-by-step timeline and durations"><br><sub><b>Deploy timeline.</b> Every step with its duration, and a clear reason when something fails.</sub></td>
  </tr>
  <tr>
    <td><img src="docs/screenshots/light/desktop-app.png" alt="Application page: controls, domain, logs and resources"><br><sub><b>App page.</b> Controls, domain, logs and resource use in one place.</sub></td>
    <td><img src="docs/screenshots/light/desktop-files.png" alt="File manager in the Files tab with folders, saved folders and file sizes"><br><sub><b>File manager.</b> Browse and manage what your app saves.</sub></td>
  </tr>
  <tr>
    <td><img src="docs/screenshots/light/desktop-database-tables.png" alt="Database page with the table browser showing rows of an invoices table"><br><sub><b>Database tables.</b> Look inside a database without a separate tool.</sub></td>
    <td><img src="docs/screenshots/light/desktop-database-query.png" alt="SQL query editor with a result grid"><br><sub><b>Query.</b> Run SQL and download the result as CSV.</sub></td>
  </tr>
  <tr>
    <td><img src="docs/screenshots/light/desktop-activity.png" alt="Activity log with search and filters by actor and action"><br><sub><b>Activity.</b> Who did what and when, with search and filters.</sub></td>
    <td><img src="docs/screenshots/light/desktop-server-hosting.png" alt="Server page with domain hosting: registered domains and the DNS record to create"><br><sub><b>Server and domains.</b> Register a domain once; the panel shows the exact DNS record.</sub></td>
  </tr>
</table>

On a phone the panel switches to a bottom navigation bar:

<table>
  <tr>
    <td width="25%"><img src="docs/screenshots/light/mobile-apps.png" alt="Application list on mobile"></td>
    <td width="25%"><img src="docs/screenshots/light/mobile-app.png" alt="Application page on mobile"></td>
    <td width="25%"><img src="docs/screenshots/light/mobile-deploy.png" alt="Deploy page on mobile"></td>
    <td width="25%"><img src="docs/screenshots/light/mobile-database-query.png" alt="SQL query result on mobile"></td>
  </tr>
</table>

<details>
<summary><b>Dark mode (click to open)</b></summary>
<br>

<table>
  <tr>
    <td width="50%"><img src="docs/screenshots/dark/desktop-deploy.png" alt="Deploy page: the uploaded file is detected, Java version and port are chosen, a summary on the right shows everything is ready (dark mode)"><br><sub><b>Deploy.</b> Upload a file, check what was detected, press Deploy.</sub></td>
    <td width="50%"><img src="docs/screenshots/dark/desktop-deployments.png" alt="Deployment details with a step-by-step timeline and durations (dark mode)"><br><sub><b>Deploy timeline.</b> Every step with its duration, and a clear reason when something fails.</sub></td>
  </tr>
  <tr>
    <td><img src="docs/screenshots/dark/desktop-app.png" alt="Application page: controls, domain, logs and resources (dark mode)"><br><sub><b>App page.</b> Controls, domain, logs and resource use in one place.</sub></td>
    <td><img src="docs/screenshots/dark/desktop-files.png" alt="File manager in the Files tab with folders, saved folders and file sizes (dark mode)"><br><sub><b>File manager.</b> Browse and manage what your app saves.</sub></td>
  </tr>
  <tr>
    <td><img src="docs/screenshots/dark/desktop-database-tables.png" alt="Database page with the table browser showing rows of an invoices table (dark mode)"><br><sub><b>Database tables.</b> Look inside a database without a separate tool.</sub></td>
    <td><img src="docs/screenshots/dark/desktop-database-query.png" alt="SQL query editor with a result grid (dark mode)"><br><sub><b>Query.</b> Run SQL and download the result as CSV.</sub></td>
  </tr>
  <tr>
    <td><img src="docs/screenshots/dark/desktop-activity.png" alt="Activity log with search and filters by actor and action (dark mode)"><br><sub><b>Activity.</b> Who did what and when, with search and filters.</sub></td>
    <td><img src="docs/screenshots/dark/desktop-server-hosting.png" alt="Server page with domain hosting: registered domains and the DNS record to create (dark mode)"><br><sub><b>Server and domains.</b> Register a domain once; the panel shows the exact DNS record.</sub></td>
  </tr>
</table>

On a phone the panel switches to a bottom navigation bar:

<table>
  <tr>
    <td width="25%"><img src="docs/screenshots/dark/mobile-apps.png" alt="Application list on mobile (dark mode)"></td>
    <td width="25%"><img src="docs/screenshots/dark/mobile-app.png" alt="Application page on mobile (dark mode)"></td>
    <td width="25%"><img src="docs/screenshots/dark/mobile-deploy.png" alt="Deploy page on mobile (dark mode)"></td>
    <td width="25%"><img src="docs/screenshots/dark/mobile-database-query.png" alt="SQL query result on mobile (dark mode)"></td>
  </tr>
</table>

</details>

## Install

**You need:**

- A server with **Ubuntu Server 26.04 LTS** (64-bit, `amd64`) that has no Docker, Caddy or PostgreSQL set up yet. 2 CPU cores, 4 GB RAM and 40 GB disk is the minimum.
- A user that can run `sudo`, and internet access from the server.
- *Optional:* a domain name, if you want the panel at `https://panel.yourcompany.com`. Without one, the panel opens at `https://<server IP>:8443`.

**Steps:**

1. Log in to the server over SSH.
2. Copy and paste **one** of these:

   ```bash
   # Panel on your IP address (simplest, nothing to set up first)
   curl -fsSL https://github.com/aribrilliantsyah/runova/releases/latest/download/get.sh | sudo bash -s -- --port 8443

   # Panel on your own domain (point the domain's DNS A record to the server first)
   curl -fsSL https://github.com/aribrilliantsyah/runova/releases/latest/download/get.sh | sudo bash -s -- --domain panel.example.com
   ```

3. The installer shows what it is about to do and asks **Continue? [y/N]**. Type `y`. The first run takes a few minutes while it installs Docker, Caddy and PostgreSQL from Ubuntu's own repositories.
4. At the end it prints the panel address, the username `admin` and a **password, shown only once**. Copy it.
5. Open the address in your browser and log in. With the IP address option the browser shows a certificate warning, because the server makes its own certificate; continue past it.
6. Change the admin password from the account menu, open **Server** and fill in the server's public IP, then create your first user on **Pengguna**.

Forgot the password? `sudo -u runova env RUNOVA_DATA=/var/lib/runova runova admin reset-password admin`

Opening the firewall at your cloud provider, setting up DNS, putting Runova behind nginx, and fixing problems are covered in the **[full installation guide](docs/install-ubuntu.md)** and **[nginx in front of Runova](docs/nginx-front-proxy.md)**.

## Update

On the server:

```bash
sudo runova update      # asks for confirmation, then installs the newest release
sudo runova rollback    # went wrong? go back to the previous version
```

<p><img src="docs/screenshots/light/desktop-update-banner.png" alt="Update banner below the top bar, shown to admins when a new version is out" width="720"></p>

Admins also see a banner in the panel when a new version is out. Your apps, users, data and panel address are left untouched. More detail in [Operations](docs/operations.md).

## Learn more

- [Installing on Ubuntu](docs/install-ubuntu.md): server specs, firewall, DNS, backups, troubleshooting
- [nginx in front of Runova](docs/nginx-front-proxy.md): for servers behind another web server
- [Domains and HTTPS](docs/domains.md): shared domain hosting, custom domains, Cloudflare
- [Operations](docs/operations.md): updates, rollback, installer details, uninstall
- [Architecture](docs/architecture.md): how isolation, quotas, databases and files work; what can run
- [Development](docs/development.md): run it locally, tests, releasing
- [Specification](docs/spec.md) and [Changelog](CHANGELOG.md)

## License and credits

Runova is released under the [Apache License 2.0](LICENSE); see also [`NOTICE`](NOTICE). Third-party assets in the panel keep their own licenses (`web/vendor/licenses/`).

- UI icons: [Lucide](https://lucide.dev), ISC license.
- Programming language logos: [Devicon](https://devicon.dev), MIT license. Trademarks belong to their respective owners.
- Fonts: [IBM Plex Sans and IBM Plex Mono](https://www.ibm.com/plex/), SIL Open Font License.
- Runova logo: a launching rocket with a window shaped like a server rack slot, custom designed for this project. Master files and variants are in `docs/brand/`; the app icon (`runova-app-icon.svg`, 1024 px PNG) puts it on a macOS-style ink squircle.
