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

// dbg escreve uma linha no arquivo apontado por PGTUI_DEBUG (se definido).
// Útil para depurar a UI, já que stdout está ocupado pelo alt-screen.
func dbg(format string, args ...any) {
	dbgOnce.Do(func() {
		path := os.Getenv("PGTUI_DEBUG")
		if path == "" {
			return
		}
		f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
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
