// Package update checks GitHub Releases for a newer pgtower and can replace the
// running binary in place. Everything is best-effort and offline-safe: a failed
// network call never breaks the app, it just means "no update offered".
package update

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

// Repo is the canonical GitHub "owner/name" pgtower is released from.
const Repo = "9level/pgtower"

// httpGet issues a GET with a pgtower User-Agent (GitHub requires one) bound to
// the caller's context.
func httpGet(ctx context.Context, url string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "pgtower-updater")
	req.Header.Set("Accept", "application/vnd.github+json")
	return http.DefaultClient.Do(req)
}

// Latest returns the newest published release tag (e.g. "v0.5.0").
func Latest(ctx context.Context, repo string) (string, error) {
	resp, err := httpGet(ctx, "https://api.github.com/repos/"+repo+"/releases/latest")
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("github: %s", resp.Status)
	}
	var body struct {
		TagName string `json:"tag_name"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&body); err != nil {
		return "", err
	}
	if body.TagName == "" {
		return "", fmt.Errorf("github: empty tag_name")
	}
	return body.TagName, nil
}

// parseVersion extracts the numeric (major, minor, patch) from a "vX.Y.Z" tag,
// ignoring any pre-release/build suffix. ok is false for "dev" or garbage.
func parseVersion(v string) ([3]int, bool) {
	var out [3]int
	s := strings.TrimSpace(v)
	s = strings.TrimPrefix(strings.TrimPrefix(s, "v"), "V")
	// drop pre-release / build metadata (1.2.3-rc1, 1.2.3+meta)
	if i := strings.IndexAny(s, "-+"); i >= 0 {
		s = s[:i]
	}
	if s == "" {
		return out, false
	}
	parts := strings.Split(s, ".")
	if len(parts) == 0 || len(parts) > 3 {
		return out, false
	}
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 {
			return out, false
		}
		out[i] = n
	}
	return out, true
}

// IsRelease reports whether v is a real version tag (so it makes sense to check
// for updates — dev/source builds are skipped).
func IsRelease(v string) bool {
	_, ok := parseVersion(v)
	return ok
}

// IsNewer reports whether latest is strictly greater than current. If either is
// unparseable (e.g. current == "dev"), it returns false — never nag a build we
// can't reason about.
func IsNewer(latest, current string) bool {
	l, ok1 := parseVersion(latest)
	c, ok2 := parseVersion(current)
	if !ok1 || !ok2 {
		return false
	}
	for i := 0; i < 3; i++ {
		if l[i] != c[i] {
			return l[i] > c[i]
		}
	}
	return false
}

// AssetName is the release asset filename for the running platform, matching the
// names produced by the Makefile and consumed by install.sh.
func AssetName(version string) string {
	return fmt.Sprintf("pgtower-%s-%s-%s", version, runtime.GOOS, runtime.GOARCH)
}

// ManualError means pgtower could not replace its own binary (usually because the
// install directory needs root). It carries a ready-to-paste instruction.
type ManualError struct {
	Dir  string
	Repo string
}

func (e *ManualError) Error() string { return "automatic update needs elevated permissions" }

// Instructions returns a human-facing block explaining how to finish the update
// by hand.
func (e *ManualError) Instructions() string {
	return fmt.Sprintf(
		"Automatic update needs write access to %s (try running as root).\n\n"+
			"Update with the installer:\n"+
			"  curl -fsSL https://raw.githubusercontent.com/%s/master/install.sh | sh\n\n"+
			"Or grab the binary from:\n"+
			"  https://github.com/%s/releases/latest",
		e.Dir, e.Repo, e.Repo)
}

// SelfUpdate downloads the release binary for `version`, verifies its SHA256 and
// atomically replaces the running executable. It returns *ManualError when the
// install directory is not writable, so the caller can show manual steps.
func SelfUpdate(ctx context.Context, repo, version string) error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		exe = resolved
	}
	dir := filepath.Dir(exe)

	asset := AssetName(version)
	base := fmt.Sprintf("https://github.com/%s/releases/download/%s", repo, version)

	// A temp file in the SAME directory guarantees the final rename is atomic
	// (same filesystem) and doubles as the writability probe.
	tmp := filepath.Join(dir, ".pgtower.update.tmp")
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755)
	if err != nil {
		return &ManualError{Dir: dir, Repo: repo}
	}
	cleanup := func() { _ = f.Close(); _ = os.Remove(tmp) }

	if err := downloadTo(ctx, base+"/"+asset, f); err != nil {
		cleanup()
		return err
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(tmp)
		return err
	}

	// Verify against SHA256SUMS when the release publishes it.
	if sums, err := fetch(ctx, base+"/SHA256SUMS"); err == nil {
		if want := sumFor(sums, asset); want != "" {
			got, err := sha256File(tmp)
			if err != nil {
				_ = os.Remove(tmp)
				return err
			}
			if !strings.EqualFold(want, got) {
				_ = os.Remove(tmp)
				return fmt.Errorf("checksum mismatch — refusing to install")
			}
		}
	}

	_ = os.Chmod(tmp, 0o755)
	if err := os.Rename(tmp, exe); err != nil {
		_ = os.Remove(tmp)
		return &ManualError{Dir: dir, Repo: repo}
	}
	return nil
}

// downloadTo streams url into w, failing on a non-200 status.
func downloadTo(ctx context.Context, url string, w io.Writer) error {
	resp, err := httpGet(ctx, url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download %s: %s", filepath.Base(url), resp.Status)
	}
	_, err = io.Copy(w, resp.Body)
	return err
}

func fetch(ctx context.Context, url string) (string, error) {
	resp, err := httpGet(ctx, url)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("%s: %s", filepath.Base(url), resp.Status)
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	return string(b), err
}

// sumFor finds the checksum for asset in a "sha256  filename" listing.
func sumFor(sums, asset string) string {
	sc := bufio.NewScanner(strings.NewReader(sums))
	for sc.Scan() {
		fields := strings.Fields(sc.Text())
		if len(fields) == 2 && fields[1] == asset {
			return fields[0]
		}
	}
	return ""
}

func sha256File(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// --- "never suggest updates" preference (a marker file in the user config dir) ---

func markerPath() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "pgtower", "no-update-check"), nil
}

// OptedOut reports whether the user asked to never be prompted again.
func OptedOut() bool {
	p, err := markerPath()
	if err != nil {
		return false
	}
	_, err = os.Stat(p)
	return err == nil
}

// OptOut persists the "never suggest updates" choice.
func OptOut() error {
	p, err := markerPath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	return os.WriteFile(p, []byte(time.Now().UTC().Format(time.RFC3339)+"\n"), 0o644)
}
