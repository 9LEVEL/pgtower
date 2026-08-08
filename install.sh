#!/bin/sh
# pgtui installer — fetches the latest stable static binary from GitHub Releases.
#
#   curl -fsSL https://raw.githubusercontent.com/9level/pgtui/master/install.sh | sh
#
# Environment overrides:
#   PGTUI_VERSION=v0.5.0            pin a version (default: latest release)
#   PGTUI_INSTALL_DIR=/opt/bin      install directory (default: /usr/local/bin)
#   PGTUI_CONFIG_DIR=/opt/pgtui     config directory to create (default: /opt/pgtui)
#
# It does NOT compile: pgtui ships as a single static binary (CGO disabled), so
# it runs on any Linux distro (Debian, Ubuntu, Alpine, …) and macOS — only the
# OS and CPU architecture matter.

set -eu

REPO="9level/pgtui"
INSTALL_DIR="${PGTUI_INSTALL_DIR:-/usr/local/bin}"
CONFIG_DIR="${PGTUI_CONFIG_DIR:-/opt/pgtui}"
VERSION="${PGTUI_VERSION:-latest}"

# Colors only when stderr is a terminal.
if [ -t 2 ]; then
	B=$(printf '\033[1m'); G=$(printf '\033[32m'); Y=$(printf '\033[33m'); R=$(printf '\033[31m'); N=$(printf '\033[0m')
else
	B=; G=; Y=; R=; N=
fi
say()  { printf '%s %s\n' "${B}▸${N}" "$*" >&2; }
ok()   { printf '%s %s\n' "${G}✓${N}" "$*" >&2; }
warn() { printf '%s %s\n' "${Y}!${N}" "$*" >&2; }
die()  { printf '%s %s\n' "${R}✗${N}" "$*" >&2; exit 1; }

# --- downloader (curl or wget) ---
if command -v curl >/dev/null 2>&1; then
	fetch()    { curl -fsSL "$1"; }
	download() { curl -fsSL -o "$2" "$1"; }
	HAVE_CURL=1
elif command -v wget >/dev/null 2>&1; then
	fetch()    { wget -qO- "$1"; }
	download() { wget -qO "$2" "$1"; }
	HAVE_CURL=0
else
	die "need curl or wget to download."
fi

# --- detect OS / architecture ---
os=$(uname -s)
arch=$(uname -m)
case "$os" in
	Linux)  OS=linux ;;
	Darwin) OS=darwin ;;
	*) die "unsupported OS '$os'. pgtui ships for Linux and macOS. Build from source: https://github.com/$REPO#install" ;;
esac
case "$arch" in
	x86_64 | amd64)  ARCH=amd64 ;;
	aarch64 | arm64) ARCH=arm64 ;;
	*) die "unsupported CPU '$arch' (need x86_64 or arm64). Build from source: https://github.com/$REPO#install" ;;
esac
say "Target: ${B}$OS/$ARCH${N} — static binary, so any $OS distro works (Debian, Ubuntu, Alpine, …)."

# --- resolve the version ---
if [ "$VERSION" = "latest" ]; then
	say "Finding the latest release…"
	if [ "$HAVE_CURL" = 1 ]; then
		# Follow the /releases/latest redirect (no API rate limit). No -f: a
		# 404 (no releases yet) must fall through to the friendly message below.
		VERSION=$(curl -sSLI -o /dev/null -w '%{url_effective}' "https://github.com/$REPO/releases/latest" 2>/dev/null | sed -E 's#.*/tag/##')
	else
		VERSION=$(fetch "https://api.github.com/repos/$REPO/releases/latest" 2>/dev/null | grep -m1 '"tag_name"' | sed -E 's/.*"tag_name": *"([^"]+)".*/\1/' || true)
	fi
fi
case "$VERSION" in
	v*) : ;;
	*) die "no published release found. Ask the maintainer to cut one (make release VERSION=vX.Y.Z), or build from source: https://github.com/$REPO#install" ;;
esac
ok "Version: ${B}$VERSION${N}"

ASSET="pgtui-$VERSION-$OS-$ARCH"
BASE="https://github.com/$REPO/releases/download/$VERSION"

# --- download to a temp dir ---
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT INT TERM
say "Downloading $ASSET…"
download "$BASE/$ASSET" "$tmp/pgtui" ||
	die "download failed — no asset '$ASSET' for $VERSION. See https://github.com/$REPO/releases"

# --- verify checksum when SHA256SUMS is published ---
if download "$BASE/SHA256SUMS" "$tmp/SHA256SUMS" 2>/dev/null; then
	want=$(awk -v a="$ASSET" '$2 == a {print $1}' "$tmp/SHA256SUMS")
	if [ -n "$want" ]; then
		if command -v sha256sum >/dev/null 2>&1; then
			got=$(sha256sum "$tmp/pgtui" | awk '{print $1}')
		else
			got=$(shasum -a 256 "$tmp/pgtui" | awk '{print $1}')
		fi
		[ "$want" = "$got" ] || die "checksum mismatch — refusing to install (want $want, got $got)"
		ok "Checksum verified"
	else
		warn "checksum for $ASSET not listed — skipping integrity check"
	fi
else
	warn "no SHA256SUMS for this release — skipping integrity check"
fi

chmod +x "$tmp/pgtui"

# --- install (sudo only if needed) ---
say "Installing to $INSTALL_DIR…"
if mkdir -p "$INSTALL_DIR" 2>/dev/null && [ -w "$INSTALL_DIR" ]; then
	mv "$tmp/pgtui" "$INSTALL_DIR/pgtui"
elif command -v sudo >/dev/null 2>&1; then
	warn "$INSTALL_DIR needs root — using sudo"
	sudo mkdir -p "$INSTALL_DIR" && sudo mv "$tmp/pgtui" "$INSTALL_DIR/pgtui"
else
	die "cannot write to $INSTALL_DIR and sudo not found. Retry with: PGTUI_INSTALL_DIR=\"\$HOME/.local/bin\" sh"
fi

ok "Installed $("$INSTALL_DIR/pgtui" --version 2>/dev/null || echo "pgtui $VERSION") → $INSTALL_DIR/pgtui"

# PATH hint.
case ":$PATH:" in
	*":$INSTALL_DIR:"*) : ;;
	*) warn "$INSTALL_DIR is not on your PATH — add:  export PATH=\"$INSTALL_DIR:\$PATH\"" ;;
esac

# --- config directory: create it and seed a commented config.yml if absent ----
CONFIG_FILE="$CONFIG_DIR/config.yml"
say "Ensuring config directory $CONFIG_DIR…"
CSUDO=
if mkdir -p "$CONFIG_DIR" 2>/dev/null && [ -w "$CONFIG_DIR" ]; then
	:
elif command -v sudo >/dev/null 2>&1; then
	CSUDO=sudo
	$CSUDO mkdir -p "$CONFIG_DIR" 2>/dev/null || warn "could not create $CONFIG_DIR"
else
	warn "cannot create $CONFIG_DIR (no write access and no sudo)"
fi

if [ -d "$CONFIG_DIR" ] && [ ! -f "$CONFIG_FILE" ]; then
	tmpl=$(mktemp)
	cat > "$tmpl" <<'YML'
# pgtui configuration — https://github.com/9level/pgtui
#
# Precedence (highest first): environment variables (DATABASE_URL, PGTUI_*) >
# this file > built-in defaults. Everything here is optional and commented out.

# --- connection ---------------------------------------------------------------
# Either a full DSN…
# database_url: "postgres://user:pass@host:5432/postgres?sslmode=disable"

# …or the individual parts (ignored when database_url is set):
# host: 127.0.0.1
# port: 5432
# user: postgres
# password: ""
# database: postgres
# sslmode: disable

# --- behaviour ----------------------------------------------------------------
# Dashboard auto-refresh, in seconds.
# refresh_seconds: 5

# PBKDF2 rounds for SCRAM password resets (0 = built-in safe default).
# scram_iterations: 15000

# Host facts the tuning advisor cannot read over SQL (0 = unknown).
# host_ram_mb: 8192
# host_cpus: 4

# Check GitHub for a newer release on startup and offer to update (true/false).
# update_check: true
YML
	if [ -n "$CSUDO" ]; then $CSUDO cp "$tmpl" "$CONFIG_FILE" 2>/dev/null; else cp "$tmpl" "$CONFIG_FILE" 2>/dev/null; fi
	rm -f "$tmpl"
	[ -f "$CONFIG_FILE" ] && ok "Starter config written: $CONFIG_FILE"
elif [ -f "$CONFIG_FILE" ]; then
	ok "Config already present: $CONFIG_FILE (left untouched)"
fi

printf '\n%s Ready. Point it at your cluster and run:\n' "${G}✓${N}" >&2
printf '    export DATABASE_URL=%s\n' "'postgres://user:pass@host:5432/postgres?sslmode=disable'" >&2
printf '    %s   %s\n' "pgtui" "# or edit $CONFIG_FILE" >&2
