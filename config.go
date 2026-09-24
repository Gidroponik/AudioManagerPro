package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
)

// FlowPref is what the user pinned for one direction (input or output).
type FlowPref struct {
	DeviceID   string `json:"deviceId"`
	DeviceName string `json:"deviceName"`
	LockDevice bool   `json:"lockDevice"` // restore DeviceID whenever the system switches away
	Volume     int    `json:"volume"`
	LockVolume bool   `json:"lockVolume"` // restore Volume whenever something changes it
}

// HiddenDevice is an endpoint this app disabled, remembered so it can be restored.
type HiddenDevice struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Flow string `json:"flow"`
}

type Settings struct {
	StartMinimized bool `json:"startMinimized"` // on autostart go straight to the tray
	IncludeComms   bool `json:"includeComms"`   // also pin the "communications" default
	AlwaysOnTop    bool `json:"alwaysOnTop"`
	// Drop per-app device overrides so every program uses the pinned device.
	ResetAppDevices bool `json:"resetAppDevices"`
}

type Config struct {
	Input    FlowPref       `json:"input"`
	Output   FlowPref       `json:"output"`
	Hidden   []HiddenDevice `json:"hidden"`
	Settings Settings       `json:"settings"`
}

func defaultConfig() Config {
	return Config{
		Input:    FlowPref{Volume: 80},
		Output:   FlowPref{Volume: 50},
		Settings: Settings{StartMinimized: true, IncludeComms: true, ResetAppDevices: true},
	}
}

// ConfigStore persists Config as JSON under %APPDATA%\AudioManagerPro.
type ConfigStore struct {
	mu   sync.Mutex
	path string
	cfg  Config
}

func NewConfigStore() *ConfigStore {
	dir, err := os.UserConfigDir()
	if err != nil {
		dir = "."
	}
	s := &ConfigStore{path: filepath.Join(dir, "AudioManagerPro", "config.json"), cfg: defaultConfig()}
	if data, err := os.ReadFile(s.path); err == nil {
		_ = json.Unmarshal(data, &s.cfg)
	}
	return s
}

// Get returns a copy of the current config.
func (s *ConfigStore) Get() Config {
	s.mu.Lock()
	defer s.mu.Unlock()
	c := s.cfg
	c.Hidden = append([]HiddenDevice(nil), s.cfg.Hidden...)
	return c
}

// Update mutates the config under lock and saves it.
func (s *ConfigStore) Update(fn func(*Config)) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	fn(&s.cfg)
	data, err := json.MarshalIndent(s.cfg, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}

func (c *Config) pref(flow string) *FlowPref {
	if flow == "input" {
		return &c.Input
	}
	return &c.Output
}
