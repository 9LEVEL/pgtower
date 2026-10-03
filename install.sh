#!/bin/sh
# pgtower installer — fetches the latest stable static binary from GitHub Releases.
#
#   curl -fsSL https://pgtower.dev/install | sh
#   curl -fsSL https://raw.githubusercontent.com/9level/pgtower/master/install.sh | sh
#
# The configuration belongs to the user who runs pgtower: it lives in
# ~/.config/pgtower/config.yml ($XDG_CONFIG_HOME/pgtower). Run through sudo,
# the installer sets it up for the invoking user ($SUDO_USER), not for root.
# A config.yml left in /opt/pgtower by older versions is moved there by
# pgtower itself on its first start.
#
# pgtower was called pgtui up to v0.9: an existing pgtui binary is upgraded in
# place (renamed, pgtui kept as a symlink). PGTUI_* overrides below are still
# honoured.
#
# Environment overrides:
#   PGTOWER_VERSION=vX.Y.Z            pin a version (default: latest release)
#   PGTOWER_INSTALL_DIR=/opt/bin      install directory (default: /usr/local/bin)
#   PGTOWER_CONFIG_DIR=/path          config directory to set up (default:
#                                     ~/.config/pgtower of the installing user)
#
# It does NOT compile: pgtower ships as a single static binary (CGO disabled), so
# it runs on any Linux distro (Debian, Ubuntu, Alpine, …) and macOS — only the
# OS and CPU architecture matter.

set -eu

REPO="9level/pgtower"
INSTALL_DIR="${PGTOWER_INSTALL_DIR:-${PGTUI_INSTALL_DIR:-/usr/local/bin}}"
CONFIG_DIR="${PGTOWER_CONFIG_DIR:-${PGTUI_CONFIG_DIR:-}}"
VERSION="${PGTOWER_VERSION:-${PGTUI_VERSION:-latest}}"

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
download "$BASE/$ASSET" "$tmp/pgtower" 2>/dev/null ||
	die "download failed — $VERSION has no $ASSET. Published releases: https://github.com/$REPO/releases"

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

# --- config: ~/.config/pgtower of the user who will run pgtower ----------------
# pgtower keeps config.yml in the home of whoever runs it and always saves it
# there. Through sudo (curl … | sudo sh) that is the invoking user, not root:
# everything below is created as that user, so it belongs to them.
home_of() {
	h=$(getent passwd "$1" 2>/dev/null | cut -d: -f6)
	[ -n "$h" ] || h=$(dscl . -read "/Users/$1" NFSHomeDirectory 2>/dev/null | awk '{print $2}')
	printf '%s' "$h"
}
TARGET_USER=$(id -un)
TARGET_HOME=${HOME:-}
RUN_AS=
if [ "$(id -u)" = 0 ] && [ -n "${SUDO_USER:-}" ] && [ "$SUDO_USER" != root ]; then
	TARGET_USER=$SUDO_USER
	TARGET_HOME=$(home_of "$SUDO_USER")
	RUN_AS=$SUDO_USER
fi
as_target() {
	if [ -n "$RUN_AS" ]; then sudo -u "$RUN_AS" "$@"; else "$@"; fi
}
xdg=${XDG_CONFIG_HOME:-}
if [ -z "$CONFIG_DIR" ]; then
	if [ -z "$RUN_AS" ] && [ "${xdg#/}" != "$xdg" ]; then # set and absolute
		CONFIG_DIR="$xdg/pgtower"
	elif [ -n "$TARGET_HOME" ]; then
		CONFIG_DIR="$TARGET_HOME/.config/pgtower"
	fi
fi
CONFIG_FILE="$CONFIG_DIR/config.yml"

# Config from older versions, in the system directories pgtower still reads:
# never seed an empty config.yml that would hide it.
OLD_CONFIG=
if [ -z "${PGTOWER_CONFIG_DIR:-${PGTUI_CONFIG_DIR:-}}" ]; then
	for f in /opt/pgtower/config.yml /opt/pgtower/config.yaml /opt/pgtower/.env \
		/opt/pgtui/config.yml /opt/pgtui/config.yaml /opt/pgtui/.env \
		/etc/pgtower/config.yml /etc/pgtower/config.yaml; do
		if [ -e "$f" ]; then OLD_CONFIG=$f; break; fi
	done
fi

CONFIG_FIX=
if [ -z "$CONFIG_DIR" ]; then
	warn "could not find the home directory of $TARGET_USER — skipping the config; pgtower creates it on its first save"
elif as_target test -e "$CONFIG_FILE"; then
	if ! as_target test -r "$CONFIG_FILE"; then
		CONFIG_FIX="sudo chown $TARGET_USER $CONFIG_FILE"
		warn "$CONFIG_FILE is not readable by $TARGET_USER, so pgtower cannot use it. Fix:  $CONFIG_FIX"
	elif ! grep -q '^version:' "$CONFIG_FILE" 2>/dev/null; then
		ok "Config present: $CONFIG_FILE — pgtower upgrades it to the multi-server format on first run (original kept as config.yml.v1.bak)"
	else
		ok "Config already present: $CONFIG_FILE (left untouched)"
	fi
elif [ -n "$OLD_CONFIG" ]; then
	owner=$(ls -ld "$OLD_CONFIG" | awk '{print $3}')
	perms=$(ls -ld "$OLD_CONFIG" | cut -c5-10)
	case "$OLD_CONFIG" in
	*/.env)
		ok "Legacy $OLD_CONFIG found — pgtower imports it into config.yml on first run (original kept as .env.v1.bak)" ;;
	/etc/*)
		ok "System-wide config $OLD_CONFIG found — pgtower reads it; your own changes are saved to $CONFIG_FILE" ;;
	*)
		if [ "$owner" = "$TARGET_USER" ] && [ "$perms" = "------" ]; then
			ok "Found your config in $OLD_CONFIG — pgtower moves it to $CONFIG_FILE on its first start (original kept as $(basename "$OLD_CONFIG").moved.bak)"
		else
			warn "$OLD_CONFIG belongs to $owner or is shared, so pgtower only reads it and saves your changes to $CONFIG_FILE."
			warn "If it holds your own servers, make it yours and pgtower moves it on its next start:  sudo chown $TARGET_USER $OLD_CONFIG && sudo chmod 600 $OLD_CONFIG"
		fi ;;
	esac
else
	say "Setting up $CONFIG_DIR for $TARGET_USER…"
	if as_target mkdir -p "$CONFIG_DIR" 2>/dev/null && as_target sh -c 'umask 077 && cat > "$1"' sh "$CONFIG_FILE" <<'YML'
# pgtower configuration — https://github.com/9level/pgtower
#
# Managed by pgtower: the Servers screen (press S) saves here. Hand edits
# are fine, but comments other than this header are not preserved.
# Environment variables (DATABASE_URL, PGTOWER_*) still take precedence.
# All keys are documented in config.yml.example.

version: 2
connections: []
YML
	then
		ok "Starter config written: $CONFIG_FILE"
	else
		CONFIG_FIX="sudo mkdir -p $CONFIG_DIR && sudo chown $TARGET_USER $CONFIG_DIR"
		warn "cannot create $CONFIG_FILE as $TARGET_USER. Fix:  $CONFIG_FIX"
	fi
fi

if [ -n "$CONFIG_FIX" ]; then
	printf '\n%s Installed, but not ready yet: run  %s  then %s and press %s to add your servers.\n' "${Y}!${N}" "${B}$CONFIG_FIX${N}" "${B}pgtower${N}" "${B}S${N}" >&2
else
	printf '\n%s Ready. Run %s and press %s to add your servers.\n' "${G}✓${N}" "${B}pgtower${N}" "${B}S${N}" >&2
fi
printf '  One-off without saving anything:  DATABASE_URL=%s pgtower\n' "'postgres://user:pass@host:5432/postgres'" >&2
