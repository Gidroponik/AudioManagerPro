package main

import (
	_ "embed"
	"runtime"

	"github.com/energye/systray"
)

//go:embed build/windows/icon.ico
var trayIcon []byte

// runTray owns the notification-area icon. It runs its own message loop on a
// dedicated OS thread, independent of the Wails UI thread.
func runTray(a *App) {
	runtime.LockOSThread()
	systray.Run(func() {
		systray.SetIcon(trayIcon)
		systray.SetTitle("AudioManagerPro")
		systray.SetTooltip("AudioManagerPro")
		systray.SetOnClick(func(systray.IMenu) { a.toggleVisible() })
		systray.SetOnDClick(func(systray.IMenu) { a.show() })

		open := systray.AddMenuItem("Открыть", "Показать панель")
		open.Click(a.show)
		systray.AddSeparator()
		quit := systray.AddMenuItem("Выход", "Закрыть AudioManagerPro")
		quit.Click(a.Quit)
	}, nil)
}

func quitTray() {
	systray.Quit()
}
