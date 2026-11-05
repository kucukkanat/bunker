#!/bin/sh
# Bunker installer.
#
#   curl -fsSL https://raw.githubusercontent.com/kucukkanat/bunker/master/install.sh | sh
#
# Downloads the right Bunker binary for this machine from GitHub Releases,
# checks its SHA-256 checksum and installs it. When run as root on Linux with
# systemd it also sets Bunker up as a service and prints the setup code.
#
# Options (pass them after "sh -s --" when piping):
#   --version vX.Y.Z   install a specific release (default: latest)
#   --no-service       only install the binary, do not create a service
#   --dir DIR          install the binary into DIR
#   -h, --help         show this help
#
# Environment: BUNKER_VERSION, BUNKER_INSTALL_DIR, BUNKER_NO_SERVICE=1,
# BUNKER_URL, BUNKER_LISTEN, BUNKER_SETUP_CODE (written into /etc/bunker/bunker.env),
# BUNKER_DOWNLOAD_URL (a mirror holding the release files and checksums.txt).

set -eu

REPO="kucukkanat/bunker"
VERSION="${BUNKER_VERSION:-latest}"
INSTALL_DIR="${BUNKER_INSTALL_DIR:-}"
NO_SERVICE="${BUNKER_NO_SERVICE:-0}"
DATA_DIR="/var/lib/bunker"
ENV_FILE="/etc/bunker/bunker.env"
UNIT_FILE="/etc/systemd/system/bunker.service"

if [ -t 1 ]; then
	BOLD=$(printf '\033[1m'); DIM=$(printf '\033[2m'); RED=$(printf '\033[31m')
	GREEN=$(printf '\033[32m'); RESET=$(printf '\033[0m')
else
	BOLD=""; DIM=""; RED=""; GREEN=""; RESET=""
fi

say() { printf '%s\n' "$*"; }
step() { printf '%s==>%s %s\n' "$BOLD" "$RESET" "$*"; }
die() { printf '%serror:%s %s\n' "$RED" "$RESET" "$*" >&2; exit 1; }

usage() {
	cat <<'EOF'
Bunker installer

  curl -fsSL https://raw.githubusercontent.com/kucukkanat/bunker/master/install.sh | sh
  curl -fsSL https://raw.githubusercontent.com/kucukkanat/bunker/master/install.sh | sh -s -- [options]

Options:
  --version vX.Y.Z   install a specific release (default: latest)
  --no-service       only install the binary, do not create a systemd service
  --dir DIR          install the binary into DIR
  -h, --help         show this help

Run as root on Linux with systemd to also install and start the service.
EOF
	exit 0
}

while [ $# -gt 0 ]; do
	case "$1" in
	--version) [ $# -ge 2 ] || die "--version needs a value"; VERSION="$2"; shift 2 ;;
	--version=*) VERSION="${1#*=}"; shift ;;
	--no-service) NO_SERVICE=1; shift ;;
	--dir) [ $# -ge 2 ] || die "--dir needs a value"; INSTALL_DIR="$2"; shift 2 ;;
	--dir=*) INSTALL_DIR="${1#*=}"; shift ;;
	-h | --help) usage ;;
	*) die "unknown option: $1 (try --help)" ;;
	esac
done

case "$VERSION" in
latest | v*) ;;
*) VERSION="v$VERSION" ;;
esac

# ---------------------------------------------------------------- platform
detect_platform() {
	os=$(uname -s)
	case "$os" in
	Linux) OS=linux ;;
	Darwin) OS=darwin ;;
	MINGW* | MSYS* | CYGWIN*) die "on Windows, run in PowerShell: irm https://raw.githubusercontent.com/$REPO/master/install.ps1 | iex" ;;
	*) die "unsupported operating system: $os" ;;
	esac
	arch=$(uname -m)
	case "$arch" in
	x86_64 | amd64) ARCH=amd64 ;;
	aarch64 | arm64) ARCH=arm64 ;;
	*) die "unsupported CPU architecture: $arch (Bunker ships amd64 and arm64)" ;;
	esac
	# Apple Silicon running a Rosetta shell still wants the arm64 binary.
	if [ "$OS" = darwin ] && [ "$ARCH" = amd64 ] &&
		[ "$(sysctl -n sysctl.proc_translated 2>/dev/null || echo 0)" = 1 ]; then
		ARCH=arm64
	fi
	ASSET="bunker-$OS-$ARCH"
}

# ---------------------------------------------------------------- download
fetch() { # fetch URL FILE
	if command -v curl >/dev/null 2>&1; then
		curl -fsSL --retry 3 -o "$2" "$1"
	elif command -v wget >/dev/null 2>&1; then
		wget -q -O "$2" "$1"
	else
		die "need curl or wget to download Bunker"
	fi
}

first_word() { printf '%s\n' "$1"; }

sha256() { # prints the hex digest; uses only shell builtins to parse
	if command -v sha256sum >/dev/null 2>&1; then
		out=$(sha256sum "$1")
	elif command -v shasum >/dev/null 2>&1; then
		out=$(shasum -a 256 "$1")
	elif command -v openssl >/dev/null 2>&1; then
		out=$(openssl dgst -sha256 -r "$1")
	else
		die "need sha256sum, shasum or openssl to verify the download"
	fi
	# shellcheck disable=SC2086 # split on whitespace on purpose
	first_word $out
}

checksum_for() { # checksum_for FILE NAME: look NAME up in a sha256sum-style list
	while read -r sum file _; do
		if [ "$file" = "$2" ] || [ "$file" = "*$2" ]; then
			printf '%s\n' "$sum"
			return 0
		fi
	done <"$1"
	return 1
}

download() {
	if [ -n "${BUNKER_DOWNLOAD_URL:-}" ]; then # mirror / air-gapped installs
		base="${BUNKER_DOWNLOAD_URL%/}"
	elif [ "$VERSION" = latest ]; then
		base="https://github.com/$REPO/releases/latest/download"
	else
		base="https://github.com/$REPO/releases/download/$VERSION"
	fi
	TMP=$(mktemp -d 2>/dev/null || mktemp -d -t bunker)
	trap 'rm -rf "$TMP"' EXIT INT TERM
	step "Downloading $ASSET ($VERSION)"
	fetch "$base/$ASSET" "$TMP/bunker" || die "download failed: $base/$ASSET"
	fetch "$base/checksums.txt" "$TMP/checksums.txt" || die "could not download checksums.txt"
	want=$(checksum_for "$TMP/checksums.txt" "$ASSET") || die "no checksum for $ASSET in checksums.txt"
	got=$(sha256 "$TMP/bunker")
	[ "$want" = "$got" ] || die "checksum mismatch for $ASSET (expected $want, got $got)"
	say "    ${DIM}sha256 $got ok${RESET}"
	chmod 0755 "$TMP/bunker"
}

# ----------------------------------------------------------------- install
is_root() { [ "$(id -u)" -eq 0 ]; }

install_binary() {
	SUDO=""
	if [ -z "$INSTALL_DIR" ]; then
		if is_root || [ -w /usr/local/bin ]; then
			INSTALL_DIR=/usr/local/bin
		elif command -v sudo >/dev/null 2>&1 && sudo -n true 2>/dev/null; then
			INSTALL_DIR=/usr/local/bin
			SUDO=sudo
		else
			INSTALL_DIR="$HOME/.local/bin"
		fi
	elif ! [ -w "$INSTALL_DIR" ] && ! is_root && [ -d "$INSTALL_DIR" ]; then
		SUDO=sudo
	fi
	step "Installing to $INSTALL_DIR/bunker"
	$SUDO mkdir -p "$INSTALL_DIR"
	$SUDO cp "$TMP/bunker" "$INSTALL_DIR/bunker.new"
	$SUDO mv -f "$INSTALL_DIR/bunker.new" "$INSTALL_DIR/bunker"
	BIN="$INSTALL_DIR/bunker"
	installed=$("$BIN" version 2>/dev/null || echo bunker)
	say "    ${DIM}$installed${RESET}"
	case ":$PATH:" in
	*":$INSTALL_DIR:"*) ;;
	*) say "    note: $INSTALL_DIR is not on your PATH. Add it with:"
		say "      export PATH=\"$INSTALL_DIR:\$PATH\"" ;;
	esac
}

# ----------------------------------------------------------------- service
want_service() {
	[ "$NO_SERVICE" != 1 ] && [ "$OS" = linux ] && is_root &&
		command -v systemctl >/dev/null 2>&1 && [ -d /run/systemd/system ]
}

write_unit() {
	cat >"$UNIT_FILE" <<EOF
# Installed by https://github.com/$REPO/blob/master/install.sh
[Unit]
Description=Bunker - internal apps anyone can build
Documentation=https://kucukkanat.github.io/bunker/
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
User=bunker
Group=bunker
EnvironmentFile=-$ENV_FILE
Environment=BUNKER_DATA_DIR=$DATA_DIR
ExecStart=$BIN serve
Restart=on-failure
RestartSec=3
TimeoutStopSec=20
LimitNOFILE=65536

# Hardening
NoNewPrivileges=yes
ProtectSystem=strict
ProtectHome=yes
PrivateTmp=yes
PrivateDevices=yes
ProtectKernelTunables=yes
ProtectKernelModules=yes
ProtectKernelLogs=yes
ProtectControlGroups=yes
ProtectClock=yes
ProtectHostname=yes
ProtectProc=invisible
ProcSubset=pid
RestrictNamespaces=yes
RestrictRealtime=yes
RestrictSUIDSGID=yes
LockPersonality=yes
MemoryDenyWriteExecute=yes
RemoveIPC=yes
UMask=0077
RestrictAddressFamilies=AF_INET AF_INET6 AF_UNIX
SystemCallArchitectures=native
SystemCallFilter=@system-service
SystemCallFilter=~@privileged
SystemCallErrorNumber=EPERM
CapabilityBoundingSet=CAP_NET_BIND_SERVICE
AmbientCapabilities=CAP_NET_BIND_SERVICE
ReadWritePaths=$DATA_DIR
StateDirectory=bunker
StateDirectoryMode=0700

[Install]
WantedBy=multi-user.target
EOF
}

write_env() {
	if [ -f "$ENV_FILE" ]; then
		say "    ${DIM}keeping existing $ENV_FILE${RESET}"
		return
	fi
	mkdir -p "$(dirname "$ENV_FILE")"
	if [ -n "${BUNKER_URL:-}" ]; then
		url_line="BUNKER_URL=$BUNKER_URL"
	else
		url_line="#BUNKER_URL=https://bunker.example.com"
	fi
	if [ -n "${BUNKER_SETUP_CODE:-}" ]; then
		code_line="BUNKER_SETUP_CODE=$BUNKER_SETUP_CODE"
	else
		code_line="#BUNKER_SETUP_CODE="
	fi
	listen="${BUNKER_LISTEN:-:8080}"
	listen_line="BUNKER_LISTEN=$listen"
	tls_line="#BUNKER_TLS=auto"
	case "${BUNKER_URL:-}" in
	https://localhost* | https://127.*) ;;
	https://*)
		if [ -z "${BUNKER_LISTEN:-}" ]; then
			# A real domain and no proxy: Bunker gets HTTPS certificates itself
			# (ports 80 and 443 must be reachable from the internet).
			tls_line="BUNKER_TLS=auto"
			listen_line="#BUNKER_LISTEN=:8080"
		fi
		;;
	esac
	umask 077
	cat >"$ENV_FILE" <<EOF
# Bunker settings. Edit, then: systemctl restart bunker
# All options: https://kucukkanat.github.io/bunker/

# Public URL people use to reach Bunker (needed for sign-in redirects).
# With an https URL and no proxy, Bunker fetches certificates itself.
$url_line

# Automatic HTTPS with Let's Encrypt (needs ports 80 + 443 and DNS pointing here).
$tls_line

# Address to listen on (plain HTTP) when a proxy terminates TLS.
$listen_line

# Fixed first-run setup code (otherwise one is generated and logged).
$code_line
EOF
	chmod 0640 "$ENV_FILE"
	chgrp bunker "$ENV_FILE" 2>/dev/null || true
}

create_user() {
	if id bunker >/dev/null 2>&1; then
		return
	fi
	nologin=$(command -v nologin || echo /usr/sbin/nologin)
	if command -v useradd >/dev/null 2>&1; then
		useradd --system --home-dir "$DATA_DIR" --no-create-home --shell "$nologin" bunker
	elif command -v adduser >/dev/null 2>&1; then
		adduser -S -D -H -h "$DATA_DIR" -s "$nologin" bunker 2>/dev/null ||
			adduser --system --group --home "$DATA_DIR" --no-create-home bunker
	else
		die "cannot create the bunker user (no useradd or adduser)"
	fi
}

wait_for_setup_code() {
	i=0
	while [ $i -lt 30 ]; do
		if [ -s "$DATA_DIR/setup-code.txt" ]; then
			cat "$DATA_DIR/setup-code.txt"
			return 0
		fi
		if systemctl is-active --quiet bunker && "$BIN" healthcheck >/dev/null 2>&1 &&
			[ $i -ge 5 ]; then
			return 1 # running, but already set up
		fi
		i=$((i + 1))
		sleep 1
	done
	return 1
}

setup_service() {
	step "Setting up the bunker systemd service"
	create_user
	mkdir -p "$DATA_DIR"
	chown bunker:bunker "$DATA_DIR"
	chmod 0700 "$DATA_DIR"
	write_env
	write_unit
	systemctl daemon-reload
	systemctl enable bunker >/dev/null 2>&1
	systemctl restart bunker
	say "    ${DIM}service started: systemctl status bunker | journalctl -u bunker -f${RESET}"

	url=$(sed -n 's/^BUNKER_URL=//p' "$ENV_FILE" | tail -n 1)
	if [ -z "$url" ]; then
		host=$(hostname -f 2>/dev/null || hostname)
		# shellcheck disable=SC2046 # split on whitespace on purpose
		ip=$(first_word $(hostname -I 2>/dev/null))
		url="http://${ip:-$host}:8080"
	fi
	if code=$(wait_for_setup_code); then
		say ""
		say "  ${GREEN}${BOLD}Bunker is running.${RESET} Finish setup in your browser:"
		say ""
		say "    Open:  ${BOLD}$url/setup${RESET}"
		say "    Code:  ${BOLD}$code${RESET}"
		say ""
		say "  Then connect Microsoft Entra ID (about 2 minutes)."
		say "  For HTTPS, put Bunker behind Caddy or your load balancer and set"
		say "  BUNKER_URL in $ENV_FILE."
	elif systemctl is-active --quiet bunker; then
		say ""
		say "  ${GREEN}${BOLD}Bunker is running${RESET} at $url"
	else
		say ""
		say "  ${RED}Bunker did not start.${RESET} See: journalctl -u bunker -n 50"
		exit 1
	fi
}

main() {
	detect_platform
	download
	install_binary
	if want_service; then
		setup_service
	else
		say ""
		say "  ${GREEN}${BOLD}Bunker is installed.${RESET}"
		say ""
		say "    Try it locally:   bunker dev"
		say "    Run the server:   bunker serve   (prints a setup URL and code)"
		if [ "$OS" = linux ] && [ "$NO_SERVICE" != 1 ] && ! is_root; then
			say ""
			say "  To install it as a service, run the installer as root:"
			say "    curl -fsSL https://raw.githubusercontent.com/$REPO/master/install.sh | sudo sh"
		fi
	fi
}

main
