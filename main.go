package main

import (
	"embed"
	"log"
	"os"
	"slices"

	"AudioManagerPro/audio"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
	"github.com/wailsapp/wails/v2/pkg/options/windows"
)

//go:embed all:frontend/dist
var assets embed.FS

func main() {
	afterUpdate(os.Args[1:])

	manager, err := audio.NewManager()
	if err != nil {
		log.Fatalf("audio: %v", err)
	}
	app := NewApp(manager, NewConfigStore(), slices.Contains(os.Args[1:], "--autostart"))

	err = wails.Run(&options.App{
		Title:            "AudioManagerPro",
		Width:            winW,
		Height:           barH,
		Frameless:        true,
		DisableResize:    true,
		StartHidden:      true, // shown from domReady once positioned
		BackgroundColour: &options.RGBA{R: 0, G: 0, B: 0, A: 0},
		AssetServer:      &assetserver.Options{Assets: assets},
		OnStartup:        app.startup,
		OnDomReady:       app.domReady,
		OnShutdown:       app.shutdown,
		Bind:             []interface{}{app},
		SingleInstanceLock: &options.SingleInstanceLock{
			UniqueId:               "c4f1f0b2-6a7e-4d0e-9b8e-audiomanagerpro",
			OnSecondInstanceLaunch: func(options.SecondInstanceData) { app.show() },
		},
		Windows: &windows.Options{
			// Keep frameless decorations: Windows 11 then draws the native
			// rounded corners and drop shadow around the bar.
			Theme: windows.Dark,
		},
	})
	if err != nil {
		log.Fatal(err)
	}
}
