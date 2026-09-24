#!/bin/sh
# pgtower installer — fetches the latest stable static binary from GitHub Releases.
#
#   curl -fsSL https://pgtower.dev | sh
#   curl -fsSL https://raw.githubusercontent.com/9level/pgtower/master/install.sh | sh
#
# pgtower was called pgtui up to v0.9: an existing pgtui install is upgraded in
# place (binary renamed, pgtui kept as a symlink, /opt/pgtui moved to
# /opt/pgtower). PGTUI_* overrides below are still honoured.
#
# Environment overrides:
#   PGTOWER_VERSION=vX.Y.Z            pin a version (default: latest release)
#   PGTOWER_INSTALL_DIR=/opt/bin      install directory (default: /usr/local/bin)
#   PGTOWER_CONFIG_DIR=/opt/pgtower     config directory to create (default: /opt/pgtower)
#
# It does NOT compile: pgtower ships as a single static binary (CGO disabled), so
# it runs on any Linux distro (Debian, Ubuntu, Alpine, …) and macOS — only the
# OS and CPU architecture matter.

set -eu

REPO="9level/pgtower"
INSTALL_DIR="${PGTOWER_INSTALL_DIR:-${PGTUI_INSTALL_DIR:-/usr/local/bin}}"
CONFIG_DIR="${PGTOWER_CONFIG_DIR:-${PGTUI_CONFIG_DIR:-/opt/pgtower}}"
VERSION="${PGTOWER_VERSION:-${PGTUI_VERSION:-latest}}"
LEGACY_CONFIG_DIR="/opt/pgtui"

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
	*) die "unsupported OS '$os'. pgtower ships for Linux and macOS. Build from source: https://github.com/$REPO#install" ;;
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

ASSET="pgtower-$VERSION-$OS-$ARCH"
BASE="https://github.com/$REPO/releases/download/$VERSION"

# --- download to a temp dir ---
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT INT TERM
say "Downloading $ASSET…"
if ! download "$BASE/$ASSET" "$tmp/pgtower" 2>/dev/null; then
	# Releases before v0.10 were published as pgtui-* (the project's old name).
	ASSET="pgtui-$VERSION-$OS-$ARCH"
	download "$BASE/$ASSET" "$tmp/pgtower" ||
		die "download failed — no pgtower or pgtui asset for $VERSION. See https://github.com/$REPO/releases"
	warn "$VERSION predates the rename: installing it as pgtower anyway"
fi

# --- verify checksum when SHA256SUMS is published ---
if download "$BASE/SHA256SUMS" "$tmp/SHA256SUMS" 2>/dev/null; then
	want=$(awk -v a="$ASSET" '$2 == a {print $1}' "$tmp/SHA256SUMS")
	if [ -n "$want" ]; then
		if command -v sha256sum >/dev/null 2>&1; then
			got=$(sha256sum "$tmp/pgtower" | awk '{print $1}')
		else
			got=$(shasum -a 256 "$tmp/pgtower" | awk '{print $1}')
		fi
		[ "$want" = "$got" ] || die "checksum mismatch — refusing to install (want $want, got $got)"
		ok "Checksum verified"
	else
		warn "checksum for $ASSET not listed — skipping integrity check"
	fi
else
	warn "no SHA256SUMS for this release — skipping integrity check"
fi

chmod +x "$tmp/pgtower"

# --- install (sudo only if needed) ---
say "Installing to $INSTALL_DIR…"
if mkdir -p "$INSTALL_DIR" 2>/dev/null && [ -w "$INSTALL_DIR" ]; then
	mv "$tmp/pgtower" "$INSTALL_DIR/pgtower"
elif command -v sudo >/dev/null 2>&1; then
	warn "$INSTALL_DIR needs root — using sudo"
	sudo mkdir -p "$INSTALL_DIR" && sudo mv "$tmp/pgtower" "$INSTALL_DIR/pgtower"
else
	die "cannot write to $INSTALL_DIR and sudo not found. Retry with: PGTOWER_INSTALL_DIR=\"\$HOME/.local/bin\" sh"
fi

ok "Installed $("$INSTALL_DIR/pgtower" --version 2>/dev/null || echo "pgtower $VERSION") → $INSTALL_DIR/pgtower"

# --- pgtui → pgtower: an old binary becomes a symlink, so scripts keep working ---
LEGACY_BIN="$INSTALL_DIR/pgtui"
if [ -e "$LEGACY_BIN" ] && [ ! -L "$LEGACY_BIN" ]; then
	if ln -sf pgtower "$LEGACY_BIN" 2>/dev/null || { command -v sudo >/dev/null 2>&1 && sudo ln -sf pgtower "$LEGACY_BIN"; }; then
		ok "pgtui is now pgtower — $LEGACY_BIN points to the new binary (remove it whenever you like)"
	else
		warn "could not replace the old $LEGACY_BIN — remove it by hand; the command is now pgtower"
	fi
fi

# PATH hint.
case ":$PATH:" in
	*":$INSTALL_DIR:"*) : ;;
	*) warn "$INSTALL_DIR is not on your PATH — add:  export PATH=\"$INSTALL_DIR:\$PATH\"" ;;
esac

# --- config directory: create it and seed a commented config.yml if absent ----
CONFIG_FILE="$CONFIG_DIR/config.yml"
# pgtui kept its config in /opt/pgtui: move it rather than start empty.
if [ "$CONFIG_DIR" = "/opt/pgtower" ] && [ -d "$LEGACY_CONFIG_DIR" ] && [ ! -e "$CONFIG_DIR" ]; then
	if mv "$LEGACY_CONFIG_DIR" "$CONFIG_DIR" 2>/dev/null || { command -v sudo >/dev/null 2>&1 && sudo mv "$LEGACY_CONFIG_DIR" "$CONFIG_DIR"; }; then
		ok "Moved $LEGACY_CONFIG_DIR → $CONFIG_DIR (your servers are kept)"
	else
		warn "could not move $LEGACY_CONFIG_DIR — pgtower will move it on first run"
	fi
fi
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

if [ -d "$CONFIG_DIR" ] && [ ! -f "$CONFIG_FILE" ] && [ ! -f "$CONFIG_DIR/.env" ] && [ ! -d "$LEGACY_CONFIG_DIR" ]; then
	tmpl=$(mktemp)
	cat > "$tmpl" <<'YML'
# pgtower configuration — https://github.com/9level/pgtower
#
# Managed by pgtower: the Servers screen (press S) saves here. Hand edits
# are fine, but comments other than this header are not preserved.
# Environment variables (DATABASE_URL, PGTOWER_*) still take precedence.
# All keys are documented in config.yml.example.

version: 2
connections: []
YML
	if [ -n "$CSUDO" ]; then
		$CSUDO install -m 0600 "$tmpl" "$CONFIG_FILE" 2>/dev/null
	else
		install -m 0600 "$tmpl" "$CONFIG_FILE" 2>/dev/null
	fi
	rm -f "$tmpl"
	[ -f "$CONFIG_FILE" ] && ok "Starter config written: $CONFIG_FILE"
elif [ -f "$CONFIG_FILE" ] && ! grep -q '^version:' "$CONFIG_FILE" 2>/dev/null; then
	ok "Config present: $CONFIG_FILE — pgtower upgrades it to the multi-server format on first run (original kept as config.yml.v1.bak)"
elif [ -f "$CONFIG_DIR/.env" ]; then
	ok "Legacy $CONFIG_DIR/.env found — pgtower imports it into config.yml on first run (original kept as .env.v1.bak)"
elif [ -f "$CONFIG_FILE" ]; then
	ok "Config already present: $CONFIG_FILE (left untouched)"
fi

printf '\n%s Ready. Run %s and press %s to add your servers.\n' "${G}✓${N}" "${B}pgtower${N}" "${B}S${N}" >&2
printf '  One-off without saving anything:  DATABASE_URL=%s pgtower\n' "'postgres://user:pass@host:5432/postgres'" >&2
