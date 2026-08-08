package db_test

import (
	"testing"

	"github.com/9level/pgtui/internal/db"
)

func TestBuildHBALine(t *testing.T) {
	got := db.BuildHBALine(db.HBARuleInput{
		Type: "host", Database: "all", User: "all", Address: "0.0.0.0/0", Method: "scram-sha-256",
	})
	if got != "host all all 0.0.0.0/0 scram-sha-256" {
		t.Errorf("host line = %q", got)
	}
	if got := db.BuildHBALine(db.HBARuleInput{Type: "local", Database: "all", User: "all", Method: "peer"}); got != "local all all peer" {
		t.Errorf("local line = %q", got)
	}
}

func TestHBALineOps(t *testing.T) {
	content := "line1\nline2\nline3\n"

	if g := db.HBAAppendLine(content, "line4"); g != "line1\nline2\nline3\nline4\n" {
		t.Errorf("append = %q", g)
	}
	if g := db.HBAAppendLine("", "only"); g != "only\n" {
		t.Errorf("append to empty = %q", g)
	}

	g, err := db.HBAReplaceLine(content, 2, "LINE2")
	if err != nil || g != "line1\nLINE2\nline3\n" {
		t.Errorf("replace = %q err=%v", g, err)
	}
	g, err = db.HBADeleteLine(content, 1)
	if err != nil || g != "line2\nline3\n" {
		t.Errorf("delete = %q err=%v", g, err)
	}

	if _, err := db.HBAReplaceLine(content, 99, "x"); err == nil {
		t.Error("replace out of range should fail")
	}
	if _, err := db.HBADeleteLine(content, 0); err == nil {
		t.Error("delete out of range should fail")
	}
}
