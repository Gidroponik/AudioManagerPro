package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"sync"
	"time"

	"AudioManagerPro/audio"

	"github.com/wailsapp/wails/v2/pkg/runtime"
)

// Window geometry in CSS pixels; must match frontend/dist/style.css.
const (
	winW = 560
	barH = 64
)

type App struct {
	ctx       context.Context
	audio     *audio.Manager
	store     *ConfigStore
	win       nativeWindow
	autostart bool

	winMu    sync.Mutex // serialises window animations
	expanded bool
	barRect  rect

	lastErr map[string]string // enforcer errors already reported, to avoid spamming

	updMu    sync.Mutex
	update   *UpdateInfo // newest release found, nil when up to date
	updating bool
}

func NewApp(m *audio.Manager, store *ConfigStore, autostart bool) *App {
	return &App{audio: m, store: store, autostart: autostart, lastErr: map[string]string{}}
}

func (a *App) startup(ctx context.Context) {
	a.ctx = ctx
	go runTray(a)
	go a.enforceLoop()
	go a.updateLoop()
}

// domReady places the collapsed bar and shows it, unless we were launched by
// autostart with "start minimized" enabled.
func (a *App) domReady(ctx context.Context) {
	cfg := a.store.Get()
	a.win.roundCorners()
	r := a.win.centeredRect(winW, barH)
	a.win.setRect(r)
	a.barRect = r
	runtime.WindowSetAlwaysOnTop(ctx, cfg.Settings.AlwaysOnTop)
	if cfg.Settings.ResetAppDevices && (cfg.Input.LockDevice || cfg.Output.LockDevice) {
		go a.clearAppOverrides()
	}
	if !(a.autostart && cfg.Settings.StartMinimized) {
		a.show()
	}
}

func (a *App) shutdown(ctx context.Context) {
	quitTray()
}

// ---------- window ----------

// show brings the window back from the tray with a short slide-up.
func (a *App) show() {
	a.winMu.Lock()
	defer a.winMu.Unlock()
	if a.win.isVisible() {
		runtime.WindowUnminimise(a.ctx)
		a.win.focus()
		return
	}
	r := a.win.rect()
	off := int32(14 * a.win.scale())
	a.win.setRect(rect{r.Left, r.Top + off, r.Right, r.Bottom + off})
	runtime.EventsEmit(a.ctx, "appear")
	runtime.WindowShow(a.ctx)
	a.win.focus()
	a.win.animate(r, 260*time.Millisecond)
}

func (a *App) toggleVisible() {
	if runtime.WindowIsNormal(a.ctx) && a.win.isVisible() {
		runtime.EventsEmit(a.ctx, "request-hide")
		return
	}
	a.show()
}

// Expand grows the window to height (CSS px), centred on screen. Used for the
// full panel and, with a smaller height, for the close dialog.
func (a *App) Expand(height int) {
	a.winMu.Lock()
	defer a.winMu.Unlock()
	if !a.expanded {
		a.barRect = a.win.rect()
		a.expanded = true
	}
	if target := a.win.centeredRect(winW, height); target != a.win.rect() {
		a.win.animate(target, 380*time.Millisecond)
	}
}

// Collapse shrinks the window back to the bar at its previous position.
func (a *App) Collapse() {
	a.winMu.Lock()
	defer a.winMu.Unlock()
	if !a.expanded {
		return
	}
	a.expanded = false
	if !a.win.isVisible() {
		a.win.setRect(a.barRect)
		return
	}
	a.win.animate(a.barRect, 320*time.Millisecond)
}

// HideToTray hides the window; the tray icon brings it back.
func (a *App) HideToTray() {
	runtime.WindowHide(a.ctx)
}

func (a *App) Quit() {
	runtime.Quit(a.ctx)
}

// ---------- devices ----------

type FlowView struct {
	Devices   []audio.Device `json:"devices"`
	Pref      FlowPref       `json:"pref"`
	DefaultID string         `json:"defaultId"`
}

func parseFlow(flow string) (audio.Flow, error) {
	switch flow {
	case "input":
		return audio.Input, nil
	case "output":
		return audio.Output, nil
	}
	return 0, fmt.Errorf("unknown flow %q", flow)
}

// GetFlow lists active devices of a flow plus the saved preference. A pinned
// device that is currently unplugged is included so the user still sees it.
func (a *App) GetFlow(flow string) (FlowView, error) {
	f, err := parseFlow(flow)
	if err != nil {
		return FlowView{}, err
	}
	all, err := a.audio.List(f, true)
	if err != nil {
		return FlowView{}, err
	}
	cfg := a.store.Get()
	v := FlowView{Pref: *cfg.pref(flow), Devices: []audio.Device{}}
	for _, d := range all {
		if d.State == "active" || d.ID == v.Pref.DeviceID {
			v.Devices = append(v.Devices, d)
		}
		if d.IsDefault {
			v.DefaultID = d.ID
		}
	}
	return v, nil
}

func (a *App) roles() []audio.Role {
	r := []audio.Role{audio.RoleConsole, audio.RoleMultimedia}
	if a.store.Get().Settings.IncludeComms {
		r = append(r, audio.RoleCommunications)
	}
	return r
}

// SetPreferred pins a device for the flow and makes it the default right now.
func (a *App) SetPreferred(flow, id, name string) error {
	if _, err := parseFlow(flow); err != nil {
		return err
	}
	if err := a.audio.SetDefault(id, a.roles()...); err != nil {
		return err
	}
	if a.store.Get().Settings.ResetAppDevices {
		a.clearAppOverrides()
	}
	return a.store.Update(func(c *Config) {
		p := c.pref(flow)
		p.DeviceID, p.DeviceName = id, name
		if p.LockVolume {
			_ = a.audio.SetVolume(id, p.Volume)
		}
	})
}

func (a *App) SetLockDevice(flow string, on bool) error {
	return a.store.Update(func(c *Config) { c.pref(flow).LockDevice = on })
}

func (a *App) SetLockVolume(flow string, on bool) error {
	return a.store.Update(func(c *Config) { c.pref(flow).LockVolume = on })
}

// SetVolume stores the pinned volume and applies it to the target device.
func (a *App) SetVolume(flow string, percent int) error {
	f, err := parseFlow(flow)
	if err != nil {
		return err
	}
	var id string
	err = a.store.Update(func(c *Config) {
		p := c.pref(flow)
		p.Volume = percent
		id = p.DeviceID
	})
	if err != nil {
		return err
	}
	if id == "" {
		if id, err = a.audio.DefaultID(f, audio.RoleConsole); err != nil {
			return err
		}
	}
	return a.audio.SetVolume(id, percent)
}

// ---------- hide devices ----------

type HiddenView struct {
	Output    []audio.Device `json:"output"`
	Input     []audio.Device `json:"input"`
	HiddenIDs []string       `json:"hiddenIds"`
	Pinned    []string       `json:"pinned"`    // locked devices: cannot be hidden
	Preferred []string       `json:"preferred"` // devices chosen in this app: kept by "hide all"
}

func (a *App) GetAllDevices() (HiddenView, error) {
	out, err := a.audio.List(audio.Output, true)
	if err != nil {
		return HiddenView{}, err
	}
	in, err := a.audio.List(audio.Input, true)
	if err != nil {
		return HiddenView{}, err
	}
	cfg := a.store.Get()
	v := HiddenView{Output: out, Input: in, HiddenIDs: []string{}, Pinned: []string{}, Preferred: []string{}}
	for _, h := range cfg.Hidden {
		v.HiddenIDs = append(v.HiddenIDs, h.ID)
	}
	for _, p := range []FlowPref{cfg.Input, cfg.Output} {
		if p.DeviceID == "" {
			continue
		}
		v.Preferred = append(v.Preferred, p.DeviceID)
		if p.LockDevice {
			v.Pinned = append(v.Pinned, p.DeviceID)
		}
	}
	return v, nil
}

// SetDeviceHidden disables (hidden=true) or re-enables an endpoint system-wide.
func (a *App) SetDeviceHidden(id, name, flow string, hidden bool) error {
	if err := a.audio.SetVisible(id, !hidden); err != nil {
		return err
	}
	return a.store.Update(func(c *Config) {
		kept := c.Hidden[:0]
		for _, h := range c.Hidden {
			if h.ID != id {
				kept = append(kept, h)
			}
		}
		c.Hidden = kept
		if hidden {
			c.Hidden = append(c.Hidden, HiddenDevice{ID: id, Name: name, Flow: flow})
		}
	})
}

// HideAllDevices hides every endpoint of a flow except the current system
// defaults and the device pinned in this app. Returns how many were hidden.
func (a *App) HideAllDevices(flow string) (int, error) {
	f, err := parseFlow(flow)
	if err != nil {
		return 0, err
	}
	devs, err := a.audio.List(f, true)
	if err != nil {
		return 0, err
	}
	cfg := a.store.Get()
	keep := cfg.pref(flow).DeviceID
	var errs []error
	n := 0
	for _, d := range devs {
		if d.State == "disabled" || d.IsDefault || d.IsDefaultComm || d.ID == keep {
			continue
		}
		if err := a.SetDeviceHidden(d.ID, d.Name, flow, true); err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", d.Name, err))
			continue
		}
		n++
	}
	return n, errors.Join(errs...)
}

// RestoreAllHidden re-enables every endpoint this app has hidden.
func (a *App) RestoreAllHidden() (int, error) {
	var errs []error
	restored := 0
	for _, h := range a.store.Get().Hidden {
		if err := a.audio.SetVisible(h.ID, true); err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", h.Name, err))
			continue
		}
		restored++
		_ = a.store.Update(func(c *Config) {
			kept := c.Hidden[:0]
			for _, x := range c.Hidden {
				if x.ID != h.ID {
					kept = append(kept, x)
				}
			}
			c.Hidden = kept
		})
	}
	return restored, errors.Join(errs...)
}

// ---------- settings ----------

type SettingsView struct {
	Settings Settings `json:"settings"`
	Autorun  bool     `json:"autorun"`
	Elevated bool     `json:"elevated"`
	Version  string   `json:"version"`
}

func (a *App) GetSettings() SettingsView {
	return SettingsView{
		Settings: a.store.Get().Settings,
		Autorun:  autorunEnabled(),
		Elevated: isElevated(),
		Version:  version,
	}
}

func (a *App) SetSetting(key string, on bool) error {
	switch key {
	case "autorun":
		return setAutorun(on)
	case "alwaysOnTop":
		runtime.WindowSetAlwaysOnTop(a.ctx, on)
	}
	return a.store.Update(func(c *Config) {
		switch key {
		case "startMinimized":
			c.Settings.StartMinimized = on
		case "includeComms":
			c.Settings.IncludeComms = on
		case "alwaysOnTop":
			c.Settings.AlwaysOnTop = on
		case "resetAppDevices":
			c.Settings.ResetAppDevices = on
		}
	})
}

// ---------- enforcer ----------

func (a *App) enforceLoop() {
	t := time.NewTicker(time.Second)
	defer t.Stop()
	for range t.C {
		a.enforce()
	}
}

func (a *App) enforce() {
	cfg := a.store.Get()
	for _, f := range []audio.Flow{audio.Output, audio.Input} {
		p := *cfg.pref(f.String())
		if p.LockDevice && p.DeviceID != "" {
			a.enforceDevice(f, p, cfg.Settings.IncludeComms)
		}
		if p.LockVolume {
			a.enforceVolume(f, p)
		}
	}
}

func (a *App) enforceDevice(f audio.Flow, p FlowPref, comms bool) {
	cur, _ := a.audio.DefaultID(f, audio.RoleConsole)
	drift := cur != p.DeviceID
	if comms {
		c, _ := a.audio.DefaultID(f, audio.RoleCommunications)
		drift = drift || c != p.DeviceID
	}
	if !drift {
		delete(a.lastErr, "dev"+f.String())
		return
	}
	if err := a.audio.SetDefault(p.DeviceID, a.roles()...); err != nil {
		// Typically the device is unplugged; report once, keep retrying quietly.
		if a.lastErr["dev"+f.String()] != err.Error() {
			a.lastErr["dev"+f.String()] = err.Error()
			log.Printf("restore %s device: %v", f, err)
		}
		return
	}
	delete(a.lastErr, "dev"+f.String())
	if a.store.Get().Settings.ResetAppDevices {
		a.clearAppOverrides()
	}
	a.notify(fmt.Sprintf("Возвращено устройство: %s", p.DeviceName))
}

// clearAppOverrides drops per-app device choices so every program follows
// the system default. Failures are only logged: the default itself is set.
func (a *App) clearAppOverrides() {
	if err := a.audio.ClearAppOverrides(); err != nil {
		log.Printf("clear per-app devices: %v", err)
	}
}

func (a *App) enforceVolume(f audio.Flow, p FlowPref) {
	id := p.DeviceID
	if id == "" {
		var err error
		if id, err = a.audio.DefaultID(f, audio.RoleConsole); err != nil {
			return
		}
	}
	v, err := a.audio.Volume(id)
	if err != nil || v == p.Volume {
		return
	}
	if err := a.audio.SetVolume(id, p.Volume); err == nil {
		kind := "звука"
		if f == audio.Input {
			kind = "микрофона"
		}
		a.notify(fmt.Sprintf("Громкость %s возвращена: %d%% → %d%%", kind, v, p.Volume))
	}
}

func (a *App) notify(msg string) {
	log.Print(msg)
	if a.ctx != nil {
		runtime.EventsEmit(a.ctx, "notice", msg)
	}
}

// ---------- updates ----------

// updateLoop checks GitHub shortly after start and then every 6 hours.
func (a *App) updateLoop() {
	time.Sleep(5 * time.Second)
	for {
		if info, err := a.CheckUpdate(); err != nil {
			log.Printf("update check: %v", err)
		} else if info != nil {
			runtime.EventsEmit(a.ctx, "update-available", info)
		}
		time.Sleep(6 * time.Hour)
	}
}

// CheckUpdate asks GitHub for a newer release; nil means up to date.
func (a *App) CheckUpdate() (*UpdateInfo, error) {
	info, err := checkUpdate()
	if err != nil {
		return nil, err
	}
	a.updMu.Lock()
	a.update = info
	a.updMu.Unlock()
	return info, nil
}

// InstallUpdate downloads and applies the pending update, then restarts.
func (a *App) InstallUpdate() error {
	a.updMu.Lock()
	info := a.update
	if info == nil || a.updating {
		a.updMu.Unlock()
		return errors.New("нет доступного обновления")
	}
	a.updating = true
	a.updMu.Unlock()
	defer func() {
		a.updMu.Lock()
		a.updating = false
		a.updMu.Unlock()
	}()

	err := installUpdate(info, func(pct int) { runtime.EventsEmit(a.ctx, "update-progress", pct) })
	if err != nil {
		return err
	}
	runtime.Quit(a.ctx)
	return nil
}
