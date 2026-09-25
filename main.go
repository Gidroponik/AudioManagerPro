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

// instanceID guards against a second copy; test builds override it via
// -ldflags "-X main.instanceID=..." to run next to an installed copy.
var instanceID = "c4f1f0b2-6a7e-4d0e-9b8e-audiomanagerpro"

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
			UniqueId:               instanceID,
			OnSecondInstanceLaunch: func(options.SecondInstanceData) { app.show() },
		},
		Windows: &windows.Options{
			// A fully transparent window: the rounded card, its border and
			// shadow are drawn by CSS, so corners can be any radius and stay
			// anti-aliased (DWM only offers a fixed small rounding).
			WebviewIsTransparent:              true,
			WindowIsTranslucent:               true,
			BackdropType:                      windows.None,
			DisableFramelessWindowDecorations: true,
			Theme:                             windows.Dark,
		},
	})
	if err != nil {
		log.Fatal(err)
	}
}
