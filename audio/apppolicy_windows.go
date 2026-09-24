//go:build windows

package audio

import (
	"unsafe"

	"golang.org/x/sys/windows"
)

// Per-application device overrides ("Settings → Sound → Volume mixer → app
// output/input device") live behind the undocumented WinRT factory
// Windows.Media.Internal.AudioPolicyConfig. An app with an override ignores
// the system default, so clearing them makes every app follow our choice.

var (
	combase                   = windows.NewLazySystemDLL("combase.dll")
	procRoGetActivationFactor = combase.NewProc("RoGetActivationFactory")
	procWindowsCreateString   = combase.NewProc("WindowsCreateString")
	procWindowsDeleteString   = combase.NewProc("WindowsDeleteString")

	// The interface id changed in Windows 10 21H2 (build 21390).
	iidAudioPolicyConfigFactoryNew = windows.GUID{Data1: 0xAB3D4648, Data2: 0xE242, Data3: 0x459F, Data4: [8]byte{0xB0, 0x2F, 0x54, 0x1C, 0x70, 0x30, 0x63, 0x24}}
	iidAudioPolicyConfigFactoryOld = windows.GUID{Data1: 0x2A59116D, Data2: 0x6C4F, Data3: 0x45E0, Data4: [8]byte{0xA7, 0x4F, 0x70, 0x7E, 0x3F, 0xEF, 0x92, 0x58}}
)

const methodClearAllPersistedApplicationDefaultEndpoints = 27

func (s *session) appPolicy() (comObject, error) {
	if s.apps != nil {
		return s.apps, nil
	}
	name, _ := windows.UTF16FromString("Windows.Media.Internal.AudioPolicyConfig")
	var hs uintptr
	hr, _, _ := procWindowsCreateString.Call(uintptr(unsafe.Pointer(&name[0])), uintptr(len(name)-1), uintptr(unsafe.Pointer(&hs)))
	if err := hrErr("WindowsCreateString", hr); err != nil {
		return nil, err
	}
	defer procWindowsDeleteString.Call(hs)

	iid := &iidAudioPolicyConfigFactoryNew
	if maj, _, build := windows.RtlGetNtVersionNumbers(); maj == 10 && build&0xFFFF < 21390 {
		iid = &iidAudioPolicyConfigFactoryOld
	}
	var f comObject
	hr, _, _ = procRoGetActivationFactor.Call(hs, uintptr(unsafe.Pointer(iid)), uintptr(unsafe.Pointer(&f)))
	if err := hrErr("RoGetActivationFactory(AudioPolicyConfig)", hr); err != nil {
		return nil, err
	}
	s.apps = f
	return f, nil
}

// ClearAppOverrides removes every per-application input/output device
// override, so all programs use the system default devices.
func (m *Manager) ClearAppOverrides() (err error) {
	m.run(func(s *session) {
		var f comObject
		if f, err = s.appPolicy(); err != nil {
			return
		}
		err = hrErr("ClearAllPersistedApplicationDefaultEndpoints", f.call(methodClearAllPersistedApplicationDefaultEndpoints))
	})
	return
}
