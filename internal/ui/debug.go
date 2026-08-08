package ui

import (
	"fmt"
	"os"
	"sync"
	"time"
)

var (
	dbgOnce sync.Once
	dbgFile *os.File
	dbgOn   bool
)

// dbg writes a line to the file pointed to by PGTUI_DEBUG (if set). Useful for
// debugging the UI, since stdout is busy with the alt-screen.
func dbg(format string, args ...any) {
	dbgOnce.Do(func() {
		path := os.Getenv("PGTUI_DEBUG")
		if path == "" {
			return
		}
		// 0600: the log can contain keystrokes, keep it owner-only.
		f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
		if err != nil {
			return
		}
		dbgFile = f
		dbgOn = true
	})
	if !dbgOn {
		return
	}
	fmt.Fprintf(dbgFile, time.Now().Format("15:04:05.000")+" "+format+"\n", args...)
	_ = dbgFile.Sync()
}
