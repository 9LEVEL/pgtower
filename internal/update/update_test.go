package update

import (
	"runtime"
	"testing"
)

func TestIsNewer(t *testing.T) {
	cases := []struct {
		latest, current string
		want            bool
	}{
		{"v0.6.0", "v0.5.0", true},
		{"v0.5.1", "v0.5.0", true},
		{"v1.0.0", "v0.9.9", true},
		{"v0.5.0", "v0.5.0", false}, // equal
		{"v0.4.0", "v0.5.0", false}, // older
		{"v0.5.0", "dev", false},    // never nag a dev build
		{"garbage", "v0.5.0", false},
		{"v0.5.0", "", false},
		{"0.6.0", "0.5.0", true},        // tolerate missing 'v'
		{"v0.6.0-rc1", "v0.5.0", true},  // pre-release suffix ignored
		{"v0.5.0", "v0.5.0-rc1", false}, // 0.5.0 == 0.5.0 after stripping
		{"v0.5.10", "v0.5.2", true},     // numeric, not lexical
	}
	for _, c := range cases {
		if got := IsNewer(c.latest, c.current); got != c.want {
			t.Errorf("IsNewer(%q, %q) = %v, want %v", c.latest, c.current, got, c.want)
		}
	}
}

func TestIsRelease(t *testing.T) {
	for _, v := range []string{"v0.5.0", "0.5.0", "v1.2.3-rc1"} {
		if !IsRelease(v) {
			t.Errorf("IsRelease(%q) = false, want true", v)
		}
	}
	for _, v := range []string{"dev", "", "nope", "v"} {
		if IsRelease(v) {
			t.Errorf("IsRelease(%q) = true, want false", v)
		}
	}
}

func TestAssetName(t *testing.T) {
	got := AssetName("v0.5.0")
	want := "pgtui-v0.5.0-" + runtime.GOOS + "-" + runtime.GOARCH
	if got != want {
		t.Errorf("AssetName = %q, want %q", got, want)
	}
}

func TestSumFor(t *testing.T) {
	sums := "aaa  pgtui-v0.5.0-linux-amd64\nbbb  pgtui-v0.5.0-darwin-arm64\n"
	if got := sumFor(sums, "pgtui-v0.5.0-darwin-arm64"); got != "bbb" {
		t.Errorf("sumFor = %q, want bbb", got)
	}
	if got := sumFor(sums, "missing"); got != "" {
		t.Errorf("sumFor(missing) = %q, want empty", got)
	}
}
