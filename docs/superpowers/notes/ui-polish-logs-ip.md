# UI polish: deployment trace, logs, IP access, errors, deploy page

Branch `feat/server-page-layout`. Briefs: `.brief-server-layout.md`, `.brief-ui-v2.md` (tasks 1-6, 8, 9; task 7 Activity moved to the list-pages worktree).

## Decisions

- **Port bug (production `starboom`, "tidak menerima koneksi di port 38398").** 38398 was the *host* port (30000-39999, `allocHostPort`), not the app's. The container only got `PORT`; Spring Boot reads `SERVER_PORT`, so a JAR whose port was not resolvable from its files listened on its own default while Runova waited on the published one. Fix: JAR containers also get `SERVER_PORT=<internal port>` (Spring's env binding outranks `application.properties`). Health errors now name the *container* port. Test: `TestContainerSpecJarGetsServerPort` (failed before the fix).
- **Deployment trace** stored on the deployment row, migration `0003_deploy_trace.sql`: `steps` (JSON timeline), `problem` (JSON diagnosis), `container_log` (JSON lines). Chosen over a new table: one row per deployment already exists, is pruned with it (`keepDeployments`), and isolation is the app's (owner/admin via `Service.load`). Bounds: 2000 lines / 256 KiB of log, 4 KiB of problem detail.
- **Steps** come from the real code path (`internal/apps/trace.go`): `artifact`, `image`, `prepare` (network, data dir, host port, env), `stop_old` (stop-start only, when something served), `create`, `start`, `health` (seconds and attempts, saved at most every 2 s), `route` (activate + Caddy), `retire` (zero-downtime only). A failure marks the running step failed and the rest skipped.
- **Diagnosis** (`diagnose`/`exitProblem`): plain title + hint + technical detail + exit code. Recognised: health timeout (with the port seen in the log, e.g. `Tomcat started on port 9090`), OOMKilled/137, `UnsupportedClassVersionError` (→ "Butuh Java N"), port in use, Spring config errors, Caddy refusal, image pull, create/start errors. Kinds: `deploy`, `deploy_kept_old` (zero-downtime failure; old version keeps serving, app stays Berjalan), `health_timeout`, `exited`, `crash_loop`, `exited_clean`, `stopped_user`, `container_lost`. Start/Restart clears the active deployment's problem.
- **Logs always available.** `GET /api/apps/{id}/deployments/{did}/log?tail=` returns the live container's output while it exists, otherwise the saved copy (`source: container|saved|none`). Containers removed by a failed deploy, a retire, or a stop-start keep their output first (`removeContainerOf` → `keepLog`). `GET /api/apps/{id}` adds `latest` (newest deployment) so the panel can describe deploys in progress and failures.
- **IP access host.** `/api/info` adds `ip_access_host` + `ip_access_local`; `/api/server` adds `host.local_ip` and `host.ip_access`. Pure `ipAccess(publicIP, ifaces)` in `internal/api/ipaccess.go`: the public IP when an interface owns it, else the first private non-loopback, non-link-local address outside the Docker/Runova networks (172.17/16, 172.31/16, 172.30/16). `public_ip` keeps its meaning (Cek DNS, domain guide, DB connection). Panel: `components/iphost.js`, LAN tag + tooltip.
- **Broken red badge** root cause: the alert-box rule `.err{display:flex;padding:9px 12px;…}` (and `.err span{color:text}`) matched every `.chip.err` / `.dot.err`. Scoped to `div.err`.
- **One log viewer** `components/logview.js` for tab Log and deployment detail: wrap (default on, remembered), search with highlight and next/previous, level filter, line numbers, app timestamp folded into the tooltip, stack traces folded, copy, download `.log`, jump to first error.
- **Deploy page** (`pages/deploy.js`): upload first; then one review page with the name on top, detected kind/Java/port, address, database, environment (required first, optional folded), size presets Kecil/Sedang/Besar within quota + "Atur sendiri", "Lanjutan" (IP access, JVM options). The rail lists what is missing (click jumps to it) and Deploy says how many items remain. After Deploy the page opens `#/apps/<id>/deployments/<did>` (live timeline + log) and moves to Umum when the deployment is active (`sessionStorage runova.follow`). APIs and validations unchanged.
- **Icons:** `package` replaces `boxes` for the Aplikasi concept (nav, bottom nav, crumbs, stats, search, database chips). `boxes` stays in the sprite because `pages/activity.js` (owned by the list-pages session) still uses it; remove it there and from `web/icons.txt` when that lands. Sprite regenerated with `go run ./cmd/genicons` (pinned lucide-static 1.65.0); also added `wrap-text`, `chevron-up`, `arrow-down-to-line`, `circle-dashed`.

## Fake backends (`RUNOVA_FAKE_BACKENDS=1`)

Set the app's environment variable `RUNOVA_FAKE_FAIL`:

| value | simulates |
|---|---|
| `health` | port never opens (a JAR logs `Tomcat started on port 9090`) → health timeout after 15 s |
| `slow` | port opens after 8 s (progress screenshots) |
| `exit` | Spring start-up failure with stack trace, exit 1 |
| `oom` | exit 137, OOMKilled |
| `crash` | healthy, then six die events → crash loop |

Zero-downtime failure while the old version serves: set deploy mode `zero`, add `RUNOVA_FAKE_FAIL=health`, deploy again. Hosting domains `nat.*` answer "Record menunjuk ke 117.54.12.173 …" until the public IP is `117.54.12.173`.

## Not changed

- Activity page and `/api/activity`, Users page, `/api/users`, `/api/usage` (other session).
- `failure_log` stays (text summary) for compatibility with the existing API consumers.
- Deploy mode default (stop-start).
