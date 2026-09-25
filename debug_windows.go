//go:build ampdebug

package main

import (
	"os"
	"time"

	"github.com/wailsapp/wails/v2/pkg/runtime"
)

// Debug builds (-tags ampdebug) open the panel named in AMP_PANEL on start,
// so the UI can be checked without clicking on the desktop.
func init() {
	debugHook = func(a *App) {
		if p := os.Getenv("AMP_PANEL"); p != "" {
			go func() {
				time.Sleep(1500 * time.Millisecond)
				runtime.EventsEmit(a.ctx, "debug-open", p)
			}()
		}
	}
}
