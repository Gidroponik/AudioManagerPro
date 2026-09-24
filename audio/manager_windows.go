//go:build windows

package audio

import (
	"errors"
	"math"
	"runtime"
	"sort"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
)

// Flow is the direction of an audio endpoint.
type Flow int

const (
	Output Flow = 0 // eRender
	Input  Flow = 1 // eCapture
)

func (f Flow) String() string {
	if f == Input {
		return "input"
	}
	return "output"
}

// Role is an ERole value.
type Role int

const (
	RoleConsole        Role = 0
	RoleMultimedia     Role = 1
	RoleCommunications Role = 2
)

const (
	stateActive    = 0x1
	stateDisabled  = 0x2
	stateNotPresnt = 0x4
	stateUnplugged = 0x8
)

const eNotFound = 0x80070490

// ErrNoDefault is returned when a flow has no default endpoint at all.
var ErrNoDefault = errors.New("no default device")

// Device describes one audio endpoint.
type Device struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	Description   string `json:"description"`
	Adapter       string `json:"adapter"`
	Flow          string `json:"flow"`
	State         string `json:"state"` // active | disabled | unplugged
	IsDefault     bool   `json:"isDefault"`
	IsDefaultComm bool   `json:"isDefaultComm"`
	Volume        int    `json:"volume"` // 0..100, -1 when unavailable
	Muted         bool   `json:"muted"`
}

// Manager owns a dedicated OS thread with COM initialised; every Core Audio
// call is marshalled onto that thread.
type Manager struct {
	jobs chan func(*session)
}

type session struct {
	enum   comObject
	policy comObject // IPolicyConfig
	apps   comObject // IAudioPolicyConfigFactory
}

// NewManager starts the COM worker thread.
func NewManager() (*Manager, error) {
	m := &Manager{jobs: make(chan func(*session))}
	ready := make(chan error)
	go func() {
		runtime.LockOSThread()
		if err := coInitialize(); err != nil {
			ready <- err
			return
		}
		enum, err := coCreate(&clsidMMDeviceEnumerator, &iidIMMDeviceEnumerator)
		if err != nil {
			ready <- err
			return
		}
		s := &session{enum: enum}
		ready <- nil
		for job := range m.jobs {
			job(s)
		}
	}()
	if err := <-ready; err != nil {
		return nil, err
	}
	return m, nil
}

func (m *Manager) run(fn func(*session)) {
	done := make(chan struct{})
	m.jobs <- func(s *session) {
		defer close(done)
		fn(s)
	}
	<-done
}

func (s *session) policyConfig() (comObject, error) {
	if s.policy == nil {
		p, err := coCreate(&clsidPolicyConfigClient, &iidIPolicyConfig)
		if err != nil {
			return nil, err
		}
		s.policy = p
	}
	return s.policy, nil
}

func (s *session) getDevice(id string) (comObject, error) {
	p, err := windows.UTF16PtrFromString(id)
	if err != nil {
		return nil, err
	}
	var dev comObject
	if err := hrErr("GetDevice", s.enum.call(5, uintptr(unsafe.Pointer(p)), uintptr(unsafe.Pointer(&dev)))); err != nil {
		return nil, err
	}
	return dev, nil
}

func (s *session) defaultID(flow Flow, role Role) (string, error) {
	var dev comObject
	hr := s.enum.call(4, uintptr(flow), uintptr(role), uintptr(unsafe.Pointer(&dev)))
	if uint32(hr) == eNotFound {
		return "", ErrNoDefault
	}
	if err := hrErr("GetDefaultAudioEndpoint", hr); err != nil {
		return "", err
	}
	defer dev.release()
	return deviceID(dev)
}

func deviceID(dev comObject) (string, error) {
	var p *uint16
	if err := hrErr("GetId", dev.call(5, uintptr(unsafe.Pointer(&p)))); err != nil {
		return "", err
	}
	return takeCoString(p), nil
}

func endpointVolume(dev comObject) (comObject, error) {
	var vol comObject
	hr := dev.call(3, uintptr(unsafe.Pointer(&iidIAudioEndpointVolume)), clsctxAll, 0, uintptr(unsafe.Pointer(&vol)))
	if err := hrErr("Activate(IAudioEndpointVolume)", hr); err != nil {
		return nil, err
	}
	return vol, nil
}

func readVolume(dev comObject) (int, bool, error) {
	vol, err := endpointVolume(dev)
	if err != nil {
		return -1, false, err
	}
	defer vol.release()
	var level float32
	if err := hrErr("GetMasterVolumeLevelScalar", vol.call(9, uintptr(unsafe.Pointer(&level)))); err != nil {
		return -1, false, err
	}
	var muted int32
	vol.call(15, uintptr(unsafe.Pointer(&muted)))
	return int(math.Round(float64(level) * 100)), muted != 0, nil
}

// List returns the endpoints of a flow. Disabled and unplugged endpoints are
// included only when all is true.
func (m *Manager) List(flow Flow, all bool) (devices []Device, err error) {
	m.run(func(s *session) {
		mask := uintptr(stateActive)
		if all {
			mask |= stateDisabled | stateUnplugged
		}
		var coll comObject
		if err = hrErr("EnumAudioEndpoints", s.enum.call(3, uintptr(flow), mask, uintptr(unsafe.Pointer(&coll)))); err != nil {
			return
		}
		defer coll.release()

		defConsole, _ := s.defaultID(flow, RoleConsole)
		defComm, _ := s.defaultID(flow, RoleCommunications)

		var count uint32
		coll.call(3, uintptr(unsafe.Pointer(&count)))
		for i := uint32(0); i < count; i++ {
			var dev comObject
			if int32(coll.call(4, uintptr(i), uintptr(unsafe.Pointer(&dev)))) < 0 {
				continue
			}
			d := describe(dev, flow)
			dev.release()
			if d.ID == "" {
				continue
			}
			d.IsDefault = d.ID == defConsole
			d.IsDefaultComm = d.ID == defComm
			devices = append(devices, d)
		}
	})
	sort.SliceStable(devices, func(i, j int) bool {
		if devices[i].State != devices[j].State {
			return devices[i].State == "active"
		}
		return strings.ToLower(devices[i].Name) < strings.ToLower(devices[j].Name)
	})
	return devices, err
}

func describe(dev comObject, flow Flow) Device {
	d := Device{Flow: flow.String(), Volume: -1}
	d.ID, _ = deviceID(dev)

	var state uint32
	dev.call(6, uintptr(unsafe.Pointer(&state)))
	switch {
	case state&stateActive != 0:
		d.State = "active"
	case state&stateDisabled != 0:
		d.State = "disabled"
	default:
		d.State = "unplugged"
	}

	var store comObject
	if int32(dev.call(4, stgmRead, uintptr(unsafe.Pointer(&store)))) >= 0 {
		d.Name = readStringProp(store, &pkeyDeviceFriendlyName)
		d.Description = readStringProp(store, &pkeyDeviceDesc)
		d.Adapter = readStringProp(store, &pkeyDeviceInterfaceFriendlyName)
		store.release()
	}
	if d.Name == "" {
		d.Name = d.Description
	}
	if d.State == "active" {
		d.Volume, d.Muted, _ = readVolume(dev)
	}
	return d
}

// DefaultID returns the id of the current default endpoint for a flow/role.
func (m *Manager) DefaultID(flow Flow, role Role) (id string, err error) {
	m.run(func(s *session) { id, err = s.defaultID(flow, role) })
	return
}

// SetDefault makes id the default endpoint for the given roles.
func (m *Manager) SetDefault(id string, roles ...Role) (err error) {
	m.run(func(s *session) {
		var pc comObject
		if pc, err = s.policyConfig(); err != nil {
			return
		}
		p, e := windows.UTF16PtrFromString(id)
		if e != nil {
			err = e
			return
		}
		for _, r := range roles {
			if err = hrErr("SetDefaultEndpoint", pc.call(13, uintptr(unsafe.Pointer(p)), uintptr(r))); err != nil {
				return
			}
		}
	})
	return
}

// Volume returns the master volume (0..100) of an endpoint.
func (m *Manager) Volume(id string) (vol int, err error) {
	m.run(func(s *session) {
		dev, e := s.getDevice(id)
		if e != nil {
			vol, err = -1, e
			return
		}
		defer dev.release()
		vol, _, err = readVolume(dev)
	})
	return
}

// SetVolume sets the master volume (0..100) of an endpoint.
func (m *Manager) SetVolume(id string, percent int) (err error) {
	percent = max(0, min(100, percent))
	m.run(func(s *session) {
		dev, e := s.getDevice(id)
		if e != nil {
			err = e
			return
		}
		defer dev.release()
		vol, e := endpointVolume(dev)
		if e != nil {
			err = e
			return
		}
		defer vol.release()
		level := math.Float32bits(float32(percent) / 100)
		err = hrErr("SetMasterVolumeLevelScalar", vol.call(7, uintptr(level), 0))
	})
	return
}

// SetVisible enables (true) or disables (false) an endpoint system-wide —
// exactly what "Disable" in the Sound control panel does. Disabled endpoints
// disappear from every application.
func (m *Manager) SetVisible(id string, visible bool) (err error) {
	m.run(func(s *session) {
		var pc comObject
		if pc, err = s.policyConfig(); err != nil {
			return
		}
		p, e := windows.UTF16PtrFromString(id)
		if e != nil {
			err = e
			return
		}
		v := uintptr(0)
		if visible {
			v = 1
		}
		err = hrErr("SetEndpointVisibility", pc.call(14, uintptr(unsafe.Pointer(p)), v))
	})
	return
}
