package db_test

import (
	"strings"
	"testing"

	"github.com/9level/pgtui/internal/db"
)

func TestBuildTuningInput(t *testing.T) {
	// shared_buffers is commonly "16384" with unit "8kB" -> 128 MB.
	m := map[string]db.RawSetting{
		"shared_buffers":       {Setting: "16384", Unit: "8kB"},
		"effective_cache_size": {Setting: "524288", Unit: "8kB"}, // 4 GB
		"work_mem":             {Setting: "4096", Unit: "kB"},    // 4 MB
		"maintenance_work_mem": {Setting: "65536", Unit: "kB"},   // 64 MB
		"max_connections":      {Setting: "100", Unit: ""},
	}
	in := db.BuildTuningInput(m, 8<<30, 4)
	if in.SharedBuffers != 128<<20 {
		t.Errorf("shared_buffers = %d, want %d (128 MB)", in.SharedBuffers, int64(128)<<20)
	}
	if in.EffectiveCache != 4<<30 {
		t.Errorf("effective_cache_size = %d, want 4 GB", in.EffectiveCache)
	}
	if in.WorkMem != 4<<20 || in.MaintWorkMem != 64<<20 || in.MaxConnections != 100 {
		t.Errorf("input = %+v", in)
	}
}

func TestBuildAlterSystem(t *testing.T) {
	if g := db.BuildAlterSystemSet("work_mem", "8MB"); g != `ALTER SYSTEM SET "work_mem" = '8MB'` {
		t.Errorf("BuildAlterSystemSet = %q", g)
	}
	if g := db.BuildAlterSystemReset("work_mem"); g != `ALTER SYSTEM RESET "work_mem"` {
		t.Errorf("BuildAlterSystemReset = %q", g)
	}
}

func TestSettingContext(t *testing.T) {
	if (db.Setting{Context: "internal"}).Changeable() {
		t.Error("internal setting should not be changeable")
	}
	if !(db.Setting{Context: "sighup"}).Changeable() {
		t.Error("sighup setting should be changeable")
	}
	if !(db.Setting{Context: "postmaster"}).NeedsRestart() {
		t.Error("postmaster setting needs restart")
	}
	if (db.Setting{Context: "user"}).NeedsRestart() {
		t.Error("user setting does not need restart")
	}
}

func TestValidateSettingValue(t *testing.T) {
	boolS := db.Setting{VarType: "bool"}
	if err := db.ValidateSettingValue(boolS, "on"); err != nil {
		t.Errorf("bool on: %v", err)
	}
	if err := db.ValidateSettingValue(boolS, "maybe"); err == nil {
		t.Error("bool maybe should fail")
	}

	enumS := db.Setting{VarType: "enum", EnumVals: []string{"replica", "logical", "minimal"}}
	if err := db.ValidateSettingValue(enumS, "logical"); err != nil {
		t.Errorf("enum logical: %v", err)
	}
	if err := db.ValidateSettingValue(enumS, "nope"); err == nil {
		t.Error("enum nope should fail")
	}

	intS := db.Setting{VarType: "integer", MinVal: "64", MaxVal: "1000"}
	if err := db.ValidateSettingValue(intS, "128"); err != nil {
		t.Errorf("int 128: %v", err)
	}
	if err := db.ValidateSettingValue(intS, "10"); err == nil {
		t.Error("int 10 below min should fail")
	}
	if err := db.ValidateSettingValue(intS, "5000"); err == nil {
		t.Error("int 5000 above max should fail")
	}
	if err := db.ValidateSettingValue(intS, "8MB"); err != nil {
		t.Errorf("unit-bearing 8MB should pass (server validates): %v", err)
	}
	if err := db.ValidateSettingValue(intS, ""); err == nil {
		t.Error("empty should fail")
	}
	if err := db.ValidateSettingValue(intS, "abc"); err == nil {
		t.Error("non-numeric should fail")
	}
}

func TestRecommend(t *testing.T) {
	gb := int64(1) << 30
	byName := func(recs []db.TuningRec) map[string]db.TuningRec {
		m := map[string]db.TuningRec{}
		for _, r := range recs {
			m[r.Name] = r
		}
		return m
	}

	// 8 GB / 4 CPUs, tiny shared_buffers and maintenance_work_mem.
	r := byName(db.Recommend(db.TuningInput{
		RAMBytes: 8 * gb, CPUs: 4, MaxConnections: 100,
		SharedBuffers: 128 << 20, EffectiveCache: 4 * gb, WorkMem: 4 << 20, MaintWorkMem: 64 << 20,
	}))
	if g := r["shared_buffers"]; g.Verdict != db.VerdictWarn || g.Recommended != "2.0 GB" {
		t.Errorf("shared_buffers = %+v (want warn, 2.0 GB)", g)
	}
	if g := r["effective_cache_size"]; g.Verdict != db.VerdictOK {
		t.Errorf("effective_cache_size = %+v (want ok)", g)
	}
	if g := r["maintenance_work_mem"]; g.Verdict != db.VerdictWarn || g.Recommended != "512.0 MB" {
		t.Errorf("maintenance_work_mem = %+v (want warn, 512.0 MB)", g)
	}
	if g := r["max_connections"]; g.Verdict != db.VerdictOK { // 100 == 4*25, not over
		t.Errorf("max_connections = %+v (want ok)", g)
	}

	// work_mem risk: little RAM, big work_mem, many connections -> warn.
	risky := byName(db.Recommend(db.TuningInput{
		RAMBytes: gb, CPUs: 2, MaxConnections: 200, SharedBuffers: 256 << 20, WorkMem: 64 << 20,
	}))
	if g := risky["work_mem"]; g.Verdict != db.VerdictWarn || !strings.Contains(g.Note, "exhaust RAM") {
		t.Errorf("work_mem risk = %+v", g)
	}
	if g := risky["max_connections"]; g.Verdict != db.VerdictWarn { // 200 > 2*25
		t.Errorf("max_connections should warn for 200 on 2 CPUs: %+v", g)
	}

	// effective_cache_size <= shared_buffers warns regardless of RAM.
	ec := byName(db.Recommend(db.TuningInput{SharedBuffers: gb, EffectiveCache: 512 << 20}))
	if g := ec["effective_cache_size"]; g.Verdict != db.VerdictWarn {
		t.Errorf("effective_cache_size should warn when <= shared_buffers: %+v", g)
	}

	// RAM unknown -> memory recs are info with a hint.
	unknown := byName(db.Recommend(db.TuningInput{MaxConnections: 100, SharedBuffers: 128 << 20}))
	if g := unknown["shared_buffers"]; g.Verdict != db.VerdictInfo || !strings.Contains(g.Note, "PGTUI_HOST_RAM_MB") {
		t.Errorf("unknown-RAM shared_buffers = %+v", g)
	}
}
