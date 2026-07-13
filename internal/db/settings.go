package db

import (
	"context"
	"fmt"
	"strconv"
	"strings"
)

// tuningSettingNames are the GUCs the advisor reads to build recommendations.
var tuningSettingNames = []string{
	"shared_buffers",
	"effective_cache_size",
	"work_mem",
	"maintenance_work_mem",
	"max_connections",
}

// RawSetting is a GUC's current value and unit as reported by pg_settings.
type RawSetting struct {
	Setting string
	Unit    string
}

// ReadSettings returns the named GUCs from pg_settings as name -> value/unit.
func ReadSettings(ctx context.Context, p Pinger, names []string) (map[string]RawSetting, error) {
	rows, err := p.Query(ctx, `select name, setting, coalesce(unit, '')
		from pg_settings where name = any($1)`, names)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make(map[string]RawSetting, len(names))
	for rows.Next() {
		var name, setting, unit string
		if err := rows.Scan(&name, &setting, &unit); err != nil {
			return nil, err
		}
		out[name] = RawSetting{Setting: setting, Unit: unit}
	}
	return out, rows.Err()
}

// ReadTuningSettings reads the curated set of GUCs the advisor needs.
func ReadTuningSettings(ctx context.Context, p Pinger) (map[string]RawSetting, error) {
	return ReadSettings(ctx, p, tuningSettingNames)
}

// Setting is a full pg_settings row for the ALTER SYSTEM editor.
type Setting struct {
	Name           string
	Setting        string
	Unit           string
	Context        string // internal|postmaster|sighup|superuser|backend|user|...
	VarType        string // bool|integer|real|enum|string
	Category       string
	ShortDesc      string
	MinVal         string
	MaxVal         string
	EnumVals       []string
	BootVal        string
	ResetVal       string
	PendingRestart bool
}

// Changeable reports whether the GUC can be changed at all (internal settings
// are compile-time and cannot).
func (s Setting) Changeable() bool { return s.Context != "internal" }

// NeedsRestart reports whether changing the GUC only takes effect after a full
// server restart (context = postmaster).
func (s Setting) NeedsRestart() bool { return s.Context == "postmaster" }

// ListAllSettings returns every GUC from pg_settings, ordered by name.
func ListAllSettings(ctx context.Context, p Pinger) ([]Setting, error) {
	rows, err := p.Query(ctx, `
		select name, setting, coalesce(unit,''), context, vartype,
		       coalesce(category,''), coalesce(short_desc,''),
		       coalesce(min_val,''), coalesce(max_val,''),
		       coalesce(enumvals,'{}'), coalesce(boot_val,''), coalesce(reset_val,''),
		       pending_restart
		from pg_settings
		order by name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Setting
	for rows.Next() {
		var s Setting
		if err := rows.Scan(&s.Name, &s.Setting, &s.Unit, &s.Context, &s.VarType,
			&s.Category, &s.ShortDesc, &s.MinVal, &s.MaxVal, &s.EnumVals,
			&s.BootVal, &s.ResetVal, &s.PendingRestart); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// BuildAlterSystemSet builds ALTER SYSTEM SET name = 'value'. The value is
// quoted as a literal, which Postgres coerces for any GUC type.
func BuildAlterSystemSet(name, value string) string {
	return "ALTER SYSTEM SET " + QuoteIdent(name) + " = " + QuoteLiteral(value)
}

// BuildAlterSystemReset builds ALTER SYSTEM RESET name (reverts to the default).
func BuildAlterSystemReset(name string) string {
	return "ALTER SYSTEM RESET " + QuoteIdent(name)
}

// ReloadConf asks the server to reload configuration (pg_reload_conf), applying
// sighup-context changes without a restart.
func ReloadConf(ctx context.Context, p Pinger) error {
	var ok bool
	return p.QueryRow(ctx, "select pg_reload_conf()").Scan(&ok)
}

// ValidateSettingValue checks a proposed value against a Setting's type and
// bounds. It is pure/auditable. Values carrying a unit suffix (e.g. "8MB") for
// numeric GUCs are left for the server to validate.
func ValidateSettingValue(s Setting, value string) error {
	value = strings.TrimSpace(value)
	if value == "" {
		return fmt.Errorf("value is required")
	}
	switch s.VarType {
	case "bool":
		switch strings.ToLower(value) {
		case "on", "off", "true", "false", "yes", "no", "1", "0":
			return nil
		default:
			return fmt.Errorf("must be a boolean (on/off)")
		}
	case "enum":
		for _, e := range s.EnumVals {
			if e == value {
				return nil
			}
		}
		return fmt.Errorf("must be one of: %s", strings.Join(s.EnumVals, ", "))
	case "integer", "real":
		if hasUnitSuffix(value) {
			return nil // "8MB", "500ms" — the server validates units
		}
		f, err := strconv.ParseFloat(value, 64)
		if err != nil {
			return fmt.Errorf("must be a number")
		}
		if min, err := strconv.ParseFloat(s.MinVal, 64); err == nil && f < min {
			return fmt.Errorf("below minimum %s", s.MinVal)
		}
		if max, err := strconv.ParseFloat(s.MaxVal, 64); err == nil && f > max {
			return fmt.Errorf("above maximum %s", s.MaxVal)
		}
		return nil
	}
	return nil // string: accept anything
}

// hasUnitSuffix reports whether the value looks like a number with a unit
// suffix (e.g. "8MB", "500ms"): it must start with a digit/sign/dot and end in
// a letter. "abc" is not unit-bearing (it's just invalid).
func hasUnitSuffix(v string) bool {
	if len(v) < 2 {
		return false
	}
	first, last := v[0], v[len(v)-1]
	startsNum := (first >= '0' && first <= '9') || first == '+' || first == '-' || first == '.'
	endsAlpha := (last >= 'a' && last <= 'z') || (last >= 'A' && last <= 'Z')
	return startsNum && endsAlpha
}

// memUnitBytes converts a pg_settings memory unit ("kB", "MB", "8kB", ...) to
// the number of bytes one increment represents. ok=false for non-memory units.
func memUnitBytes(unit string) (int64, bool) {
	unit = strings.TrimSpace(unit)
	if unit == "" {
		return 0, false
	}
	i := 0
	for i < len(unit) && unit[i] >= '0' && unit[i] <= '9' {
		i++
	}
	factor := int64(1)
	if i > 0 {
		f, err := strconv.ParseInt(unit[:i], 10, 64)
		if err != nil {
			return 0, false
		}
		factor = f
	}
	var base int64
	switch strings.ToLower(unit[i:]) {
	case "b":
		base = 1
	case "kb":
		base = 1 << 10
	case "mb":
		base = 1 << 20
	case "gb":
		base = 1 << 30
	case "tb":
		base = 1 << 40
	default:
		return 0, false
	}
	return factor * base, true
}

// settingBytes converts a memory GUC's (setting, unit) to bytes.
func settingBytes(setting, unit string) (int64, bool) {
	mult, ok := memUnitBytes(unit)
	if !ok {
		return 0, false
	}
	n, err := strconv.ParseInt(strings.TrimSpace(setting), 10, 64)
	if err != nil {
		return 0, false
	}
	return n * mult, true
}

// humanBytes formats a byte count as B/kB/MB/GB/TB (1024-based, one decimal).
func humanBytes(n int64) string {
	if n < 1<<10 {
		return fmt.Sprintf("%d B", n)
	}
	units := []string{"kB", "MB", "GB", "TB", "PB"}
	div, exp := int64(1<<10), 0
	for m := n / (1 << 10); m >= 1<<10 && exp < len(units)-1; m /= 1 << 10 {
		div *= 1 << 10
		exp++
	}
	return fmt.Sprintf("%.1f %s", float64(n)/float64(div), units[exp])
}

// Verdict rates a setting against its recommendation.
type Verdict string

const (
	VerdictOK   Verdict = "ok"
	VerdictWarn Verdict = "warn"
	VerdictInfo Verdict = "info"
)

// TuningInput is what the recommender needs. RAMBytes/CPUs == 0 means unknown
// (Postgres can't report the host's RAM/cores, so they come from config/env).
type TuningInput struct {
	RAMBytes       int64
	CPUs           int
	MaxConnections int
	SharedBuffers  int64
	EffectiveCache int64
	WorkMem        int64
	MaintWorkMem   int64
}

// TuningRec is one advisor row: what it is now, what's recommended, a verdict
// and a short explanation.
type TuningRec struct {
	Name        string
	Current     string
	Recommended string
	Verdict     Verdict
	Note        string
}

// BuildTuningInput assembles a TuningInput from raw pg_settings values plus the
// host RAM/cores. It is pure so it can be unit-tested.
func BuildTuningInput(m map[string]RawSetting, ramBytes int64, cpus int) TuningInput {
	in := TuningInput{RAMBytes: ramBytes, CPUs: cpus}
	in.SharedBuffers, _ = settingBytes(m["shared_buffers"].Setting, m["shared_buffers"].Unit)
	in.EffectiveCache, _ = settingBytes(m["effective_cache_size"].Setting, m["effective_cache_size"].Unit)
	in.WorkMem, _ = settingBytes(m["work_mem"].Setting, m["work_mem"].Unit)
	in.MaintWorkMem, _ = settingBytes(m["maintenance_work_mem"].Setting, m["maintenance_work_mem"].Unit)
	in.MaxConnections, _ = strconv.Atoi(m["max_connections"].Setting)
	return in
}

// rateFactor rates current against a target: ok within [0.5, 2]×, else warn.
func rateFactor(current, target int64) Verdict {
	if target <= 0 || current <= 0 {
		return VerdictInfo
	}
	r := float64(current) / float64(target)
	if r >= 0.5 && r <= 2.0 {
		return VerdictOK
	}
	return VerdictWarn
}

// Recommend produces the advisor rows. It is pure (no I/O) so the advice is
// fully unit-testable. When RAM/CPUs are unknown it still does the checks that
// don't need them and marks the rest as info with a hint.
func Recommend(in TuningInput) []TuningRec {
	ramKnown := in.RAMBytes > 0
	var recs []TuningRec

	// shared_buffers ~ 25% of RAM.
	sb := TuningRec{Name: "shared_buffers", Current: humanBytes(in.SharedBuffers)}
	if ramKnown {
		target := in.RAMBytes / 4
		sb.Recommended = humanBytes(target)
		sb.Verdict = rateFactor(in.SharedBuffers, target)
		sb.Note = "≈ 25% of RAM"
	} else {
		sb.Verdict, sb.Note = VerdictInfo, "set PGTUI_HOST_RAM_MB for a target (~25% of RAM)"
	}
	recs = append(recs, sb)

	// effective_cache_size ~ 66% of RAM and must exceed shared_buffers.
	ec := TuningRec{Name: "effective_cache_size", Current: humanBytes(in.EffectiveCache)}
	if ramKnown {
		ec.Recommended = humanBytes(in.RAMBytes * 2 / 3)
	}
	switch {
	case in.EffectiveCache > 0 && in.EffectiveCache <= in.SharedBuffers:
		ec.Verdict = VerdictWarn
		ec.Note = "should be well above shared_buffers (planner's view of OS cache)"
	case ramKnown:
		ec.Verdict = rateFactor(in.EffectiveCache, in.RAMBytes*2/3)
		ec.Note = "≈ 66% of RAM"
	default:
		ec.Verdict, ec.Note = VerdictInfo, "set PGTUI_HOST_RAM_MB for a target (~66% of RAM)"
	}
	recs = append(recs, ec)

	// work_mem — budget shared across connections; may be used several times per query.
	wm := TuningRec{Name: "work_mem", Current: humanBytes(in.WorkMem)}
	if ramKnown && in.MaxConnections > 0 {
		budget := in.RAMBytes - in.SharedBuffers
		if budget <= 0 {
			budget = in.RAMBytes
		}
		wm.Recommended = humanBytes(budget / int64(in.MaxConnections) / 4)
		if worst := in.WorkMem * int64(in.MaxConnections); worst > budget {
			wm.Verdict = VerdictWarn
			wm.Note = fmt.Sprintf("work_mem × max_connections = %s can exhaust RAM under load", humanBytes(worst))
		} else {
			wm.Verdict = VerdictOK
			wm.Note = "≈ (RAM − shared_buffers) / max_connections / 4"
		}
	} else {
		wm.Verdict, wm.Note = VerdictInfo, "needs RAM and max_connections for a target"
	}
	recs = append(recs, wm)

	// maintenance_work_mem ~ RAM/16, capped at 2 GB.
	mw := TuningRec{Name: "maintenance_work_mem", Current: humanBytes(in.MaintWorkMem)}
	if ramKnown {
		target := in.RAMBytes / 16
		if cap := int64(2) << 30; target > cap {
			target = cap
		}
		mw.Recommended = humanBytes(target)
		mw.Verdict = rateFactor(in.MaintWorkMem, target)
		mw.Note = "≈ RAM/16, capped at 2 GB"
	} else {
		mw.Verdict, mw.Note = VerdictInfo, "set PGTUI_HOST_RAM_MB for a target (~RAM/16)"
	}
	recs = append(recs, mw)

	// max_connections vs CPUs.
	mc := TuningRec{Name: "max_connections", Current: strconv.Itoa(in.MaxConnections)}
	switch {
	case in.CPUs > 0 && in.MaxConnections > in.CPUs*25:
		mc.Verdict = VerdictWarn
		mc.Note = fmt.Sprintf("high for %d CPUs — prefer a pooler (PgBouncer) over many direct connections", in.CPUs)
	case in.CPUs > 0:
		mc.Verdict = VerdictOK
		mc.Note = fmt.Sprintf("reasonable for %d CPUs", in.CPUs)
	default:
		mc.Verdict, mc.Note = VerdictInfo, "set PGTUI_HOST_CPUS to assess against cores"
	}
	recs = append(recs, mc)

	return recs
}
