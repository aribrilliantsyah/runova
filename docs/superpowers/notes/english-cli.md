# English CLI and installer progress

## Decisions

- All terminal output of `install.sh`, `get.sh`, `scripts/deploy.sh`, `scripts/release.sh` and `cmd/runova/**` is English. Panel/API strings (`internal/api`, `internal/apps`, `web/**`) stay Indonesian.
- `Username: …` / `Password: …` lines of `runova admin create|reset-password` are unchanged; `install.sh` and `test/e2e/run.sh` parse `^Password: `. The installer summary keeps `Password     : …` (system test parses it).
- `runova db version` now prints `Database schema: N` and `Highest schema of this binary: N`. `rollback.go` reads that line from the **previous** binary (kept in `rollback/`), which for v0.3.0 and older prints `Skema tertinggi binary ini: N`. `highestSchemaLine` accepts both; tests: `TestHighestSchemaLine`, `TestRollbackReadsOldAndNewDBVersionOutput` (fake old binary printing Indonesian).
- Other parsers checked: `runova update` runs the *target release's* `get.sh`, which is fine either way (only exit code is used). `runova check` lines (`ok`/`fail`) were already English and are unchanged. `--yes` / `Continue? [y/N]` accepts `y`, `ya`, `yes`.
- Installer progress: `run_steps` runs 13 steps through `run_step`. Each step's stdout/stderr goes to `/var/log/runova-install.log` (0600, truncated per run; path printed once as `Details: …`). Messages the user must see use `notice` / `warn` / `die`, which write to saved duplicates of the terminal (`UI_OUT`, `UI_ERR`, via `exec {fd}>&1`).
- Spinner: background bash loop, braille frames in a UTF-8 locale, `|/-\` otherwise, `\r` + `ESC[K` redraw every 0.1s with elapsed time. Enabled when stdout is a TTY; `RUNOVA_PROGRESS=tty|plain` forces it (tests). Without a TTY: only the final `[n/13] … done (Ns)` lines, no `\r`.
- Failure: `die` and `on_exit` call `fail_step`: spinner killed, `… failed (Ns)`, last 30 log lines, log path. Exit code is preserved. `INT`/`TERM` traps `exit 130/143`, so `on_exit` kills the spinner on Ctrl+C.
- Steps already satisfied set `STEP_SKIPPED=1` and print `already done` (packages, key, config, admin).
- `get.sh` uses `curl --progress-bar` when stdout is a TTY, silent otherwise (`download`).

## Example installer output (for README / docs/install-ubuntu.md)

```
Details: /var/log/runova-install.log
[1/13] Installing packages (docker.io, caddy, postgresql-18, ufw, jq, curl) ⠹  1m12s
[1/13] Installing packages (docker.io, caddy, postgresql-18, ufw, jq, curl) ... done (1m54s)
[2/13] Creating user and folders ... done (0s)
[3/13] Installing the runova binary ... done (0s)
[4/13] Creating the encryption key ... done (0s)
[5/13] Writing the configuration ... done (0s)
[6/13] Configuring Docker ... done (1s)
[7/13] Configuring Caddy ... done (1s)
[8/13] Configuring PostgreSQL ... done (4s)
[9/13] Setting the panel address ... done (0s)
[10/13] Creating the admin user ... done (0s)
[11/13] Configuring the firewall ... done (2s)
[12/13] Starting the Runova service ... done (1s)
[13/13] Verifying the installation (runova check) ... done (0s)

Runova 0.0.9-rec is ready.

Panel address: https://172.17.0.4:8443
Caddy creates the certificate itself, so the browser will show a warning; just continue.
Username     : admin
Password     : <random>

This password is shown only once. If you lose it: sudo -u runova env RUNOVA_DATA=/var/lib/runova runova admin reset-password admin
```

Prompt: `Continue? [y/N]`. Rerun: `[1/13] Checking packages ... already done`, final line `Runova was updated. Data, keys, admin, and panel address are unchanged.` On failure: `[7/13] Configuring Caddy ... failed (1s)`, `Last 30 lines of /var/log/runova-install.log:`, the log, `Error: …`, `The installation stopped before finishing. …`.

## Koordinator: instalasi interaktif di sudo-rs (Ubuntu 26.04)

- **Bug (ada sejak v0.1.0):** `curl … | sudo bash -s` lalu menjawab `y` membuat installer berhenti diam (proses `T`, stopped) di Ubuntu 26.04. Bawaan `sudo` di Ubuntu 26.04 adalah **sudo-rs** 0.2.13 (`/etc/alternatives/sudo` → `/usr/lib/cargo/bin/sudo`; `sudo.ws` 1.9.17 ikut terpasang tetapi tidak dipakai). sudo-rs menjalankan perintah di pty sendiri; proses `bash` yang membaca skrip dari pipe adalah grup foreground pty itu, jadi child (`install.sh`) yang membaca dari `/dev/tty` mendapat SIGTTIN dan berhenti. Uji sebelumnya memakai `--yes` atau sudo klasik, jadi tidak menangkapnya.
- Matriks yang diukur (container Ubuntu 26.04 baru, login `su - deploy`, password sudo, jawaban di pty): child dengan `</dev/tty` → sudo klasik OK, sudo-rs berhenti; `setsid -w … </dev/tty` → sudo-rs OK, sudo klasik berhenti; **membaca jawaban di proses bash itu sendiri (`read </dev/tty`) lalu child tanpa membaca terminal → keduanya OK**, dan stdout child tetap terminal (spinner jalan).
- **Perbaikan:** `get.sh` sendiri yang menampilkan rencana (`install.sh --plan`), bertanya `Continue? [y/N]` dari `/dev/tty`, lalu menjalankan `install.sh … --yes </dev/null`. Tanpa terminal dan tanpa `--yes` → berhenti dengan pesan, tanpa memasang apa pun.
- **Bukti:** `test/get.bats` (bertanya di pty, jawaban `y`/`n`, tanpa terminal ditolak, installer tidak menerima terminal di stdin); instalasi sungguhan lewat `curl … | sudo bash -s -- --port 8443` dengan password dan `y` di pty, dari rilis lokal, berhasil di sudo-rs dan sudo klasik (`runova` 4 layanan aktif).
