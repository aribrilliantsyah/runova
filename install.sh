#!/usr/bin/env bash
# Runova installer for Ubuntu Server 26.04 (amd64).
#
#   sudo ./install.sh [--domain panel.example.com | --port 8443] [--public-ip IP]
#                     [--trusted-proxy IP/CIDR] [--yes]
#
# Every step checks first whether it is already done, so running the same
# command again updates the binary and the unit and leaves data, keys and the
# admin alone. Only what needs root and OS packages happens here; the keys,
# the admin and the panel address are written by `runova` itself, run as the
# runova user so that the files it creates belong to the service.

# RUNOVA_ROOT prefixes every path, RUNOVA_SRC is the folder with the release
# files; both exist so the bats tests can run the steps in a scratch folder.
ROOT="${RUNOVA_ROOT:-}"
SRC_DIR="${RUNOVA_SRC:-$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)}"

SERVICE_USER=runova
ETC_DIR="$ROOT/etc/runova"
DATA_DIR="$ROOT/var/lib/runova"
ENV_FILE="$ETC_DIR/runova.env"
SECRET_KEY="$ETC_DIR/secret.key"
BIN="$ROOT/usr/local/bin/runova"
ROLLBACK_DIR="$DATA_DIR/rollback"
UNIT="$ROOT/etc/systemd/system/runova.service"
CADDYFILE="$ROOT/etc/caddy/Caddyfile"
CADDY_DROPIN="$ROOT/etc/systemd/system/caddy.service.d/runova.conf"
DOCKER_DAEMON_JSON="$ROOT/etc/docker/daemon.json"
PG_ETC="$ROOT/etc/postgresql/18/main"

DEFAULT_PORT=8443
DEFAULT_LISTEN=127.0.0.1:7070
DOCKER_POOL_BASE=172.31.0.0/16
RUNOVA_SUBNET=172.30.0.0/16
PACKAGES=(docker.io caddy postgresql-18 ufw jq curl)

DOMAIN=""
PORT=""
PUBLIC_IP=""
TRUSTED_PROXIES=()
ASSUME_YES=0
PANEL_EXPLICIT=0
ADMIN_PASSWORD=""
VERSION=""
SKIP_FIREWALL=0
PUT_CHANGED=0

# Progress display. Each step's output goes to LOG_FILE; the terminal gets one
# line per step, with a spinner while it runs when stdout is a terminal.
# RUNOVA_PROGRESS=tty|plain forces the mode (the tests use it).
LOG_FILE="${RUNOVA_LOG:-$ROOT/var/log/runova-install.log}"
LOG_TAIL_LINES=30
PROGRESS="${RUNOVA_PROGRESS:-auto}"
# UI_OUT and UI_ERR are the real terminal streams; run_steps points them at
# duplicates so that messages stay visible while a step's own output is logged.
UI_OUT=1
UI_ERR=2
USE_SPINNER=0
SPINNER_PID=""
TOTAL_STEPS=0
STEP_N=0
STEP_LABEL=""
STEP_START=0
STEP_ACTIVE=0
STEP_SKIPPED=0
MISSING=()

log() { printf '==> %s\n' "$*"; }

# clear_line wipes the spinner line so that a message does not land on it.
clear_line() {
	if [[ -n $SPINNER_PID ]]; then
		printf '\r\033[K' >&"$UI_OUT"
	fi
}

# notice prints to the terminal even while a step's output goes to the log.
notice() {
	clear_line
	printf '%s\n' "$*" >&"$UI_OUT"
}

warn() {
	clear_line
	printf 'Warning: %s\n' "$*" >&"$UI_ERR"
}

die() {
	fail_step
	printf 'Error: %s\n' "$*" >&"$UI_ERR"
	exit 1
}

# fmt_elapsed SECONDS VAR: stores "42s" or "1m12s" in VAR.
fmt_elapsed() {
	if (($1 >= 60)); then
		printf -v "$2" '%dm%02ds' $(($1 / 60)) $(($1 % 60))
	else
		printf -v "$2" '%ds' "$1"
	fi
}

utf8_locale() {
	[[ ${LC_ALL:-${LC_CTYPE:-${LANG:-}}} == *[Uu][Tt][Ff]-8* || ${LC_ALL:-${LC_CTYPE:-${LANG:-}}} == *[Uu][Tt][Ff]8* ]]
}

# spinner_loop PREFIX runs in the background until it is killed.
spinner_loop() {
	local prefix=$1 i=0 t frames
	if utf8_locale; then
		frames=('⠋' '⠙' '⠹' '⠸' '⠼' '⠴' '⠦' '⠧' '⠇' '⠏')
	else
		frames=('|' '/' '-' "\\")
	fi
	while :; do
		fmt_elapsed $((SECONDS - STEP_START)) t
		printf '\r\033[K%s %s  %s' "$prefix" "${frames[i % ${#frames[@]}]}" "$t" >&"$UI_OUT"
		i=$((i + 1))
		sleep 0.1
	done
}

spinner_start() {
	((USE_SPINNER)) || return 0
	spinner_loop "[$STEP_N/$TOTAL_STEPS] $STEP_LABEL" &
	SPINNER_PID=$!
}

spinner_stop() {
	if [[ -n $SPINNER_PID ]]; then
		kill "$SPINNER_PID" 2>/dev/null || true
		wait "$SPINNER_PID" 2>/dev/null || true
		SPINNER_PID=""
		printf '\r\033[K' >&"$UI_OUT"
	fi
}

# finish_step done|failed prints the step's final line.
finish_step() {
	local t line
	fmt_elapsed $((SECONDS - STEP_START)) t
	line="[$STEP_N/$TOTAL_STEPS] $STEP_LABEL ..."
	if [[ $1 == "done" ]] && ((STEP_SKIPPED)); then
		line+=" already done"
	else
		line+=" $1 ($t)"
	fi
	printf '%s\n' "$line" >&"$UI_OUT"
	STEP_ACTIVE=0
}

# fail_step ends the running step as failed: the spinner stops, the final line
# says "failed", and the end of the log follows. It does nothing between steps.
fail_step() {
	((STEP_ACTIVE)) || return 0
	spinner_stop
	finish_step failed
	if [[ -s $LOG_FILE ]]; then
		printf '\nLast %d lines of %s:\n' "$LOG_TAIL_LINES" "$LOG_FILE" >&"$UI_ERR"
		tail -n "$LOG_TAIL_LINES" "$LOG_FILE" >&"$UI_ERR" || true
		printf '\nFull log: %s\n\n' "$LOG_FILE" >&"$UI_ERR"
	fi
}

# run_step FUNCTION LABEL runs one step with its output in the log. A failure
# exits through set -e or die; on_exit then reports it via fail_step.
run_step() {
	STEP_N=$((STEP_N + 1))
	STEP_LABEL=$2
	STEP_START=$SECONDS
	STEP_ACTIVE=1
	STEP_SKIPPED=0
	spinner_start
	"$1" >>"$LOG_FILE" 2>&1
	spinner_stop
	finish_step "done"
}

usage() {
	cat <<'EOF'
Usage: sudo ./install.sh [options]

  --domain <host>    serve the panel at https://<host> (automatic HTTPS)
  --port <n>         serve the panel at https://<server IP>:<n> (default without options: 8443)
  --public-ip <ip>   the server's public IP, stored in the Runova settings
  --trusted-proxy <ip/cidr>
                     a reverse proxy in front of Runova (such as nginx) whose
                     X-Forwarded-For is believed; repeat for several
  --yes, -y          do not ask questions (for installs without a terminal)
  --skip-firewall    do not touch ufw (for servers whose firewall is managed elsewhere)
  --help, -h         show this help

--domain and --port cannot be combined. When reinstalling without either,
the existing panel address is left unchanged.
EOF
}

valid_domain() {
	local d=$1
	((${#d} <= 253)) || return 1
	[[ $d =~ ^[0-9.]+$ ]] && return 1
	[[ $d =~ ^([a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?\.)+[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$ ]]
}

valid_port() {
	local p=$1
	[[ $p =~ ^[0-9]+$ ]] || return 1
	((p >= 1024 && p <= 65535)) || return 1
	# 2019 is Caddy's admin API, 5432 PostgreSQL, 7070 Runova itself, 20000-20999 the app IP ports.
	((p != 2019 && p != 5432 && p != 7070 && (p < 20000 || p > 20999)))
}

valid_ip() {
	local ip=$1 octet
	if [[ $ip =~ ^([0-9]{1,3})\.([0-9]{1,3})\.([0-9]{1,3})\.([0-9]{1,3})$ ]]; then
		for octet in "${BASH_REMATCH[@]:1}"; do
			((10#$octet <= 255)) || return 1
		done
		return 0
	fi
	[[ $ip == *:* && $ip =~ ^[0-9a-fA-F:.]+$ ]]
}

# valid_proxy accepts an IP or an IP/prefix, but not a prefix of length 0,
# which would make every client a trusted proxy.
valid_proxy() {
	local addr=${1%%/*} bits=""
	[[ $1 == */* ]] && bits=${1#*/}
	valid_ip "$addr" || return 1
	[[ $1 == */* ]] || return 0
	[[ $bits =~ ^[0-9]+$ ]] || return 1
	if [[ $addr == *:* ]]; then
		((10#$bits >= 1 && 10#$bits <= 128))
	else
		((10#$bits >= 1 && 10#$bits <= 32))
	fi
}

set_option() {
	local name=$1 value=$2
	case "$name" in
	--domain)
		value=${value,,}
		valid_domain "$value" || die "Invalid domain: $value"
		DOMAIN=$value
		;;
	--port)
		valid_port "$value" || die "Invalid panel port: $value (use 1024-65535, but not 2019, 5432, 7070, or 20000-20999)"
		PORT=$value
		;;
	--public-ip)
		valid_ip "$value" || die "Invalid public IP: $value"
		PUBLIC_IP=$value
		;;
	--trusted-proxy)
		valid_proxy "$value" || die "Invalid trusted proxy: $value (use an IP or IP/prefix, not 0.0.0.0/0)"
		TRUSTED_PROXIES+=("$value")
		;;
	esac
}

parse_args() {
	DOMAIN="" PORT="" PUBLIC_IP="" TRUSTED_PROXIES=() ASSUME_YES=0 PANEL_EXPLICIT=0 SKIP_FIREWALL=0
	while (($# > 0)); do
		case "$1" in
		--domain | --port | --public-ip | --trusted-proxy)
			(($# >= 2)) || die "Option $1 needs a value"
			set_option "$1" "$2"
			shift 2
			;;
		--domain=* | --port=* | --public-ip=* | --trusted-proxy=*)
			set_option "${1%%=*}" "${1#*=}"
			shift
			;;
		--yes | -y)
			ASSUME_YES=1
			shift
			;;
		--skip-firewall)
			SKIP_FIREWALL=1
			shift
			;;
		-h | --help)
			usage
			exit 0
			;;
		--plan)
			print_plan
			exit 0
			;;
		*) die "Unknown option: $1 (see --help)" ;;
		esac
	done
	if [[ -n $DOMAIN && -n $PORT ]]; then
		die "--domain and --port cannot be combined: the panel is reached by domain or by IP:port"
	fi
	if [[ -n $DOMAIN || -n $PORT ]]; then
		PANEL_EXPLICIT=1
	fi
}

check_os() {
	local id version arch
	# shellcheck disable=SC1091
	id=$(. "$ROOT/etc/os-release" 2>/dev/null && printf '%s' "${ID:-}")
	# shellcheck disable=SC1091
	version=$(. "$ROOT/etc/os-release" 2>/dev/null && printf '%s' "${VERSION_ID:-}")
	if [[ $id != ubuntu || $version != 26.04 ]]; then
		die "Runova only supports Ubuntu Server 26.04 (detected: ${id:-unknown} ${version})"
	fi
	arch=$(uname -m)
	[[ $arch == x86_64 ]] || die "Runova only supports the amd64 (x86_64) architecture; detected: $arch"
}

print_plan() {
	cat <<'EOF'
The installer will install docker.io, caddy, postgresql-18, ufw, jq, and curl from
Ubuntu's apt, create the "runova" user, configure Caddy and PostgreSQL, open ports
in ufw (and enable ufw if it is not active; SSH is allowed first), and start Runova.
EOF
}

confirm_plan() {
	local answer
	if ((ASSUME_YES)); then
		return 0
	fi
	[[ -t 0 ]] || die "No terminal to ask on; add --yes for an unattended install"
	print_plan
	read -r -p "Continue? [y/N] " answer
	[[ $answer == [yY]* ]] || die "Canceled"
}

# wait_for TRIES COMMAND...: runs the command once a second until it succeeds.
wait_for() {
	local tries=$1 i
	shift
	for ((i = 0; i < tries; i++)); do
		if "$@" >/dev/null 2>&1; then
			return 0
		fi
		sleep 1
	done
	return 1
}

# put_file DEST MODE: writes stdin to DEST through a temporary file in the same
# folder and sets PUT_CHANGED to 1 when the content differs from what was there.
put_file() {
	local dest=$1 mode=$2 tmp
	tmp=$(mktemp "$dest.new.XXXXXX")
	cat >"$tmp"
	chmod "$mode" "$tmp"
	if [[ -f $dest ]] && cmp -s "$tmp" "$dest"; then
		rm -f "$tmp"
		chmod "$mode" "$dest"
		PUT_CHANGED=0
	else
		mv -f "$tmp" "$dest"
		PUT_CHANGED=1
	fi
}

# as_runova SUBCOMMAND...: runs the installed binary as the service user.
as_runova() {
	(cd / && runuser -u "$SERVICE_USER" -- env "RUNOVA_DATA=$DATA_DIR" "RUNOVA_SECRET_KEY=$SECRET_KEY" "$BIN" "$@")
}

package_installed() {
	[[ $(dpkg-query -W -f="\${Status}" "$1" 2>/dev/null) == "install ok installed" ]]
}

# missing_packages fills MISSING with the packages that are not installed yet.
missing_packages() {
	local p
	MISSING=()
	for p in "${PACKAGES[@]}"; do
		if ! package_installed "$p"; then
			MISSING+=("$p")
		fi
	done
}

step_packages() {
	missing_packages
	if ((${#MISSING[@]} == 0)); then
		log "Packages already installed"
		STEP_SKIPPED=1
		return 0
	fi
	log "Installing packages: ${MISSING[*]}"
	apt-get update
	DEBIAN_FRONTEND=noninteractive apt-get install -y "${MISSING[@]}"
}

step_user_dirs() {
	local groups
	getent group docker >/dev/null || die "The docker group does not exist; is the docker.io package installed?"
	if groups=$(id -nG "$SERVICE_USER" 2>/dev/null); then
		case " $groups " in
		*" docker "*) ;;
		*) usermod -aG docker "$SERVICE_USER" ;;
		esac
	else
		log "Creating user $SERVICE_USER"
		useradd --system --user-group --groups docker --home-dir /var/lib/runova \
			--no-create-home --shell /usr/sbin/nologin "$SERVICE_USER"
	fi
	# The folders belong to runova before `runova keygen` and `runova admin create`
	# run, or the key and the database would end up owned by root.
	install -d -m 0750 "$ETC_DIR" "$DATA_DIR"
	chown "$SERVICE_USER:$SERVICE_USER" "$ETC_DIR" "$DATA_DIR"
}

# save_rollback keeps the installed binary, when it is another version than the
# one being installed, in rollback/ for `runova rollback`. Only the one version
# before this one is kept. A failure only costs the rollback, never the install.
save_rollback() {
	local old tmp
	[[ -x $BIN ]] || return 0
	old=$("$BIN" version 2>/dev/null) || return 0
	old=${old#runova }
	[[ -n $old && $old != "$VERSION" ]] || return 0
	if ! { install -d -m 0755 "$ROLLBACK_DIR" &&
		tmp=$(mktemp "$ROLLBACK_DIR/runova.new.XXXXXX") &&
		install -m 0755 "$BIN" "$tmp" &&
		mv -f "$tmp" "$ROLLBACK_DIR/runova" &&
		printf '%s\n' "$old" >"$ROLLBACK_DIR/VERSION"; }; then
		warn "Could not keep binary $old for runova rollback"
		return 0
	fi
	log "Binary runova $old kept for runova rollback"
}

step_binary() {
	local src="$SRC_DIR/runova" tmp
	[[ -f $src ]] || die "$src not found; run install.sh from the extracted release folder"
	VERSION=$("$src" version) || die "$src cannot run on this server"
	VERSION=${VERSION#runova }
	install -d -m 0755 "$(dirname "$BIN")"
	save_rollback
	tmp=$(mktemp "$BIN.new.XXXXXX")
	if ! install -m 0755 "$src" "$tmp"; then
		rm -f "$tmp"
		die "Could not copy the binary to $(dirname "$BIN")"
	fi
	mv -f "$tmp" "$BIN"
	log "Binary runova $VERSION installed at /usr/local/bin/runova"
}

step_secret() {
	if [[ -e $SECRET_KEY ]]; then
		log "Encryption key already exists"
		STEP_SKIPPED=1
		return 0
	fi
	log "Creating the encryption key"
	as_runova keygen
}

step_env_file() {
	if [[ -e $ENV_FILE ]]; then
		log "Configuration $ENV_FILE already exists, left unchanged"
		STEP_SKIPPED=1
	else
		log "Writing $ENV_FILE"
		put_file "$ENV_FILE" 0640 <<EOF
RUNOVA_LISTEN=$DEFAULT_LISTEN
RUNOVA_DATA=/var/lib/runova
RUNOVA_SECRET_KEY=/etc/runova/secret.key
EOF
	fi
	chown "root:$SERVICE_USER" "$ENV_FILE"
	chmod 0640 "$ENV_FILE"
}

# enable_docker makes Docker start at boot: the app containers and Runova
# (which starts what the reboot stopped) depend on it. The apt package enables
# it on Ubuntu, but nothing here may rely on that. containerd is enabled only
# when its unit exists. `systemctl enable` is idempotent.
enable_docker() {
	systemctl enable docker
	if systemctl cat containerd.service >/dev/null 2>&1; then
		systemctl enable containerd
	fi
}

step_docker_daemon() {
	local merged
	enable_docker
	install -d -m 0755 "$(dirname "$DOCKER_DAEMON_JSON")"
	if [[ -s $DOCKER_DAEMON_JSON ]]; then
		jq -e 'type == "object"' "$DOCKER_DAEMON_JSON" >/dev/null 2>&1 ||
			die "$DOCKER_DAEMON_JSON is not a valid JSON object; fix it and run again"
		if jq -e '."default-address-pools" != null' "$DOCKER_DAEMON_JSON" >/dev/null; then
			if ! jq -e --arg base "$DOCKER_POOL_BASE" '."default-address-pools" | any(.base == $base)' "$DOCKER_DAEMON_JSON" >/dev/null; then
				warn "$DOCKER_DAEMON_JSON already has its own default-address-pools; left as is. Make sure they do not overlap $RUNOVA_SUBNET (the Runova user network)."
			fi
			return 0
		fi
		merged=$(jq --arg base "$DOCKER_POOL_BASE" '. + {"default-address-pools": [{"base": $base, "size": 24}]}' "$DOCKER_DAEMON_JSON")
	else
		merged=$(jq -n --arg base "$DOCKER_POOL_BASE" '{"default-address-pools": [{"base": $base, "size": 24}]}')
	fi
	put_file "$DOCKER_DAEMON_JSON" 0644 <<<"$merged"
	if ((PUT_CHANGED)); then
		log "Docker default network moved to $DOCKER_POOL_BASE; restarting Docker"
		systemctl restart docker
	fi
}

caddyfile_wanted() {
	printf '{\n\tadmin localhost:2019\n}\n'
}

# caddyfile_pristine FILE: true when FILE is the Caddyfile the package shipped.
caddyfile_pristine() {
	local want have
	want=$(dpkg-query -W -f="\${Conffiles}\n" caddy 2>/dev/null | awk '$1 == "/etc/caddy/Caddyfile" { print $2 }')
	[[ -n $want ]] || return 1
	have=$(md5sum "$1" | awk '{ print $1 }')
	[[ $have == "$want" ]]
}

step_caddy() {
	local changed=0 backup
	install -d -m 0755 "$(dirname "$CADDYFILE")" "$(dirname "$CADDY_DROPIN")"
	if [[ -f $CADDYFILE ]] && ! caddyfile_wanted | cmp -s - "$CADDYFILE" && ! caddyfile_pristine "$CADDYFILE"; then
		if ((!ASSUME_YES)); then
			die "$CADDYFILE has been modified and is not the package default. Runova configures Caddy through its API and needs a Caddyfile that only contains '{ admin localhost:2019 }'. Move your sites elsewhere first, or run again with --yes to back it up to $CADDYFILE.runova-backup and replace it."
		fi
		backup="$CADDYFILE.runova-backup"
		if [[ -e $backup ]]; then
			backup="$backup.$(date +%s)"
		fi
		cp -p "$CADDYFILE" "$backup"
		warn "Old Caddyfile copied to $backup"
	fi
	put_file "$CADDYFILE" 0644 < <(caddyfile_wanted)
	changed=$((changed | PUT_CHANGED))
	# --resume keeps the configuration Runova sends through the API across a Caddy restart.
	put_file "$CADDY_DROPIN" 0644 <<'EOF'
[Service]
ExecStart=
ExecStart=/usr/bin/caddy run --environ --resume --config /etc/caddy/Caddyfile
EOF
	changed=$((changed | PUT_CHANGED))
	systemctl daemon-reload
	systemctl enable caddy
	if ((changed)); then
		systemctl restart caddy
	else
		systemctl start caddy
	fi
}

step_postgres() {
	local conf_changed=0 hba_changed=0 hba="$PG_ETC/pg_hba.conf" hba_d="$PG_ETC/pg_hba.d"
	[[ -f $hba ]] || die "$hba does not exist; the PostgreSQL 18/main cluster has not been created"

	install -d -m 0755 "$PG_ETC/conf.d"
	put_file "$PG_ETC/conf.d/runova.conf" 0644 <<'EOF'
# Managed by the Runova installer.
listen_addresses = '*'
EOF
	conf_changed=$PUT_CHANGED

	# Runova writes its rules here; the include goes last so the stock rules keep their place.
	install -d -m 0770 "$hba_d"
	chown "postgres:$SERVICE_USER" "$hba_d"
	if [[ ! -e $hba_d/runova.conf ]]; then
		cat >"$hba_d/runova.conf" <<EOF
# Managed by Runova; changes are overwritten.
host all all 127.0.0.1/32 scram-sha-256
host all all $RUNOVA_SUBNET scram-sha-256
EOF
		chown "postgres:$SERVICE_USER" "$hba_d/runova.conf"
		chmod 0664 "$hba_d/runova.conf"
	fi
	# pg_hba.conf has no single-quote syntax (unlike postgresql.conf): quotes would become part of the folder name.
	if ! grep -Eq '^[[:space:]]*include_dir[[:space:]]+"?pg_hba\.d"?[[:space:]]*$' "$hba"; then
		printf '\n# Runova\ninclude_dir pg_hba.d\n' >>"$hba"
		hba_changed=1
	fi

	systemctl enable postgresql
	if ((conf_changed)); then
		systemctl restart postgresql
	else
		systemctl start postgresql
		if ((hba_changed)); then
			systemctl reload postgresql
		fi
	fi

	wait_for 30 pg_query 'SELECT 1' || die "PostgreSQL did not respond within 30 seconds"
	pg_script <<'EOF'
DO $$
BEGIN
	IF NOT EXISTS (SELECT FROM pg_roles WHERE rolname = 'runova') THEN
		CREATE ROLE runova LOGIN CREATEDB CREATEROLE;
	ELSE
		ALTER ROLE runova LOGIN CREATEDB CREATEROLE;
	END IF;
END
$$;
GRANT EXECUTE ON FUNCTION pg_reload_conf() TO runova;
EOF
}

# pg_query SQL and pg_script run psql as the postgres user over the local socket.
pg_query() {
	(cd / && runuser -u postgres -- psql -X -At -v ON_ERROR_STOP=1 -d postgres -c "$1")
}

pg_script() {
	(cd / && runuser -u postgres -- psql -X -q -v ON_ERROR_STOP=1 -d postgres -f -)
}

# sshd_ports prints every TCP port sshd listens on, one per line (22 if none is found).
sshd_ports() {
	local ports
	ports=$(
		{
			sshd -T 2>/dev/null | awk '$1 == "port" { print $2 }' || true
			ss -H -ltnp 2>/dev/null | awk '/"sshd"/ { n = split($4, a, ":"); print a[n] }' || true
			# With socket activation the port comes from ssh.socket, not from sshd_config.
			systemctl show ssh.socket -p Listen 2>/dev/null | grep -oE ':[0-9]+ \(Stream\)' | tr -dc '0-9\n' || true
		} | sort -un
	)
	printf '%s\n' "${ports:-22}"
}

step_firewall() {
	local p status
	if ((SKIP_FIREWALL)); then
		notice "ufw skipped (--skip-firewall); open SSH, 80, 443, 20000-20999, 5432 (optional), and the panel port yourself"
		return 0
	fi
	for p in $(sshd_ports); do
		ufw allow "$p/tcp"
	done
	ufw allow 80/tcp
	ufw allow 443/tcp
	ufw allow 20000:20999/tcp
	ufw allow 5432/tcp
	if [[ -n $PORT ]]; then
		ufw allow "$PORT/tcp"
	fi
	status=$(ufw status)
	if [[ $status == *"Status: inactive"* ]]; then
		log "Enabling ufw (SSH is already allowed)"
		ufw --force enable
	fi
}

step_panel() {
	local args=() rc=0 proxy
	if ((!PANEL_EXPLICIT)); then
		# Without --domain/--port a rerun keeps the address chosen at the first install.
		as_runova admin exists || rc=$?
		case $rc in
		0) log "Panel address left unchanged" ;;
		1) PORT=$DEFAULT_PORT ;;
		*) die "Could not read the Runova database (code $rc)" ;;
		esac
	fi
	if [[ -n $DOMAIN ]]; then
		args=(--domain "$DOMAIN")
	elif [[ -n $PORT ]]; then
		args=(--port "$PORT")
	fi
	if [[ -n $PUBLIC_IP ]]; then
		args+=(--public-ip "$PUBLIC_IP")
	fi
	for proxy in "${TRUSTED_PROXIES[@]}"; do
		args+=(--trusted-proxy "$proxy")
	done
	if ((${#args[@]} > 0)); then
		as_runova panel set "${args[@]}"
	fi
}

# step_admin creates the first admin before the service starts, so that the
# service's start-up reconciliation gives that admin a PostgreSQL role.
step_admin() {
	local rc=0 out
	as_runova admin exists || rc=$?
	case $rc in
	0)
		log "Admin already exists"
		STEP_SKIPPED=1
		return 0
		;;
	1) ;;
	*) die "Could not read the Runova database (code $rc)" ;;
	esac
	out=$(as_runova admin create admin) || die "Could not create the admin"
	ADMIN_PASSWORD=$(printf '%s\n' "$out" | sed -n 's/^Password: //p')
	[[ -n $ADMIN_PASSWORD ]] || die "The admin was created but the password could not be read from the runova output"
}

panel_listen() {
	local listen
	listen=$(sed -n 's/^RUNOVA_LISTEN=//p' "$ENV_FILE" 2>/dev/null | tail -n 1)
	listen=${listen:-$DEFAULT_LISTEN}
	if [[ $listen == :* ]]; then
		listen=127.0.0.1$listen
	fi
	printf '%s\n' "$listen"
}

step_service() {
	[[ -f $SRC_DIR/runova.service ]] || die "$SRC_DIR/runova.service not found"
	install -d -m 0755 "$(dirname "$UNIT")"
	put_file "$UNIT" 0644 <"$SRC_DIR/runova.service"
	systemctl daemon-reload
	systemctl enable runova
	# Restart, not start: a running service must pick up the new binary.
	systemctl restart runova
	wait_for 30 curl -s -o /dev/null "http://$(panel_listen)/api/me" ||
		die "The runova service did not respond within 30 seconds. See: journalctl -u runova -n 50"
}

step_verify() {
	as_runova check || die "The runova check failed (see the 'fail' lines above). See also: journalctl -u runova -n 50"
}

run_steps() {
	local list entry label
	local -a steps=(
		"step_packages|"
		"step_user_dirs|Creating user and folders"
		"step_binary|Installing the runova binary"
		"step_secret|Creating the encryption key"
		"step_env_file|Writing the configuration"
		"step_docker_daemon|Configuring Docker"
		"step_caddy|Configuring Caddy"
		"step_postgres|Configuring PostgreSQL"
		"step_panel|Setting the panel address"
		"step_admin|Creating the admin user"
		"step_firewall|Configuring the firewall"
		"step_service|Starting the Runova service"
		"step_verify|Verifying the installation (runova check)"
	)
	TOTAL_STEPS=${#steps[@]}
	STEP_N=0

	install -d -m 0755 "$(dirname "$LOG_FILE")"
	(umask 077 && : >"$LOG_FILE")
	chmod 0600 "$LOG_FILE"
	exec {UI_OUT}>&1 {UI_ERR}>&2
	case $PROGRESS in
	tty) USE_SPINNER=1 ;;
	plain) USE_SPINNER=0 ;;
	*) if [[ -t 1 ]]; then USE_SPINNER=1; else USE_SPINNER=0; fi ;;
	esac
	# Ctrl+C and kill go through exit, so on_exit stops the spinner.
	trap 'exit 130' INT
	trap 'exit 143' TERM
	notice "Details: $LOG_FILE"

	for entry in "${steps[@]}"; do
		label=${entry#*|}
		if [[ -z $label ]]; then
			# Only the package step has a label that depends on the machine.
			missing_packages
			if ((${#MISSING[@]} == 0)); then
				label="Checking packages"
			else
				list=$(printf '%s, ' "${MISSING[@]}")
				label="Installing packages (${list%, })"
			fi
		fi
		run_step "${entry%%|*}" "$label"
	done
}

detect_ip() {
	ip -4 -o route get 1.1.1.1 2>/dev/null | awk '{ for (i = 1; i < NF; i++) if ($i == "src") { print $(i + 1); exit } }' || true
}

is_private_ip() {
	[[ $1 =~ ^(10\.|127\.|169\.254\.|192\.168\.|172\.(1[6-9]|2[0-9]|3[01])\.|100\.(6[4-9]|[7-9][0-9]|1[01][0-9]|12[0-7])\.) ]]
}

summary() {
	local ip public=0 host
	ip=${PUBLIC_IP:-$(detect_ip)}
	if [[ -n $PUBLIC_IP ]] || { [[ -n $ip ]] && ! is_private_ip "$ip"; }; then
		public=1
	fi

	printf '\nRunova %s is ready.\n' "${VERSION:-}"
	if [[ -n $ADMIN_PASSWORD ]]; then
		if [[ -n $DOMAIN ]]; then
			printf '\nPanel address: https://%s\n' "$DOMAIN"
			printf 'Point the DNS of %s at this server'"'"'s IP; Caddy issues the HTTPS certificate itself.\n' "$DOMAIN"
		else
			host=${ip:-<server IP>}
			printf '\nPanel address: https://%s:%s\n' "$host" "$PORT"
			printf 'Caddy creates the certificate itself, so the browser will show a warning; just continue.\n'
		fi
		printf 'Username     : admin\n'
		printf 'Password     : %s\n' "$ADMIN_PASSWORD"
		printf '\nThis password is shown only once. If you lose it: sudo -u runova env RUNOVA_DATA=/var/lib/runova runova admin reset-password admin\n'
	else
		printf 'Runova was updated. Data, keys, admin, and panel address are unchanged.\n'
	fi
	if ((!public)); then
		printf '\nThis server does not seem to have a public IP (IP: %s). The panel is only reachable from the same network.\n' "${ip:-not detected}"
		printf 'To give an app a domain without a public IP, use a Cloudflare Tunnel (see the README).\n'
		printf 'If the server has a public IP behind NAT, run again with --public-ip <ip>.\n'
	fi
}

INSTALL_DONE=0

on_exit() {
	local status=$?
	spinner_stop
	if ((status != 0 && !INSTALL_DONE)); then
		fail_step
		printf '\nThe installation stopped before finishing. Fix the problem above and run the same command again; steps that are already done are skipped.\n' >&"$UI_ERR"
	fi
}

main() {
	parse_args "$@"
	((EUID == 0)) || die "Run as root: sudo ./install.sh"
	check_os
	confirm_plan
	trap on_exit EXIT
	run_steps
	INSTALL_DONE=1
	summary
}

# `source install.sh --lib` defines the functions and runs nothing (for the tests).
if [[ ${1:-} == --lib ]]; then
	# shellcheck disable=SC2317
	return 0 2>/dev/null || exit 0
fi
set -euo pipefail
main "$@"
