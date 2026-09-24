//go:build windows

// Package audio talks to the Windows Core Audio API (MMDevice API, endpoint
// volume and the undocumented IPolicyConfig used by the Sound control panel).
package audio

import (
	"fmt"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	ole32                = windows.NewLazySystemDLL("ole32.dll")
	procCoInitializeEx   = ole32.NewProc("CoInitializeEx")
	procCoCreateInstance = ole32.NewProc("CoCreateInstance")
	procCoTaskMemFree    = ole32.NewProc("CoTaskMemFree")
	procPropVariantClear = ole32.NewProc("PropVariantClear")
)

const (
	clsctxAll             = 0x17
	coinitApartmentThread = 0x2
	stgmRead              = 0
	sFalse                = 1
	rpcEChangedMode       = 0x80010106
)

var (
	clsidMMDeviceEnumerator = windows.GUID{Data1: 0xBCDE0395, Data2: 0xE52F, Data3: 0x467C, Data4: [8]byte{0x8E, 0x3D, 0xC4, 0x57, 0x92, 0x91, 0x69, 0x2E}}
	iidIMMDeviceEnumerator  = windows.GUID{Data1: 0xA95664D2, Data2: 0x9614, Data3: 0x4F35, Data4: [8]byte{0xA7, 0x46, 0xDE, 0x8D, 0xB6, 0x36, 0x17, 0xE6}}
	iidIAudioEndpointVolume = windows.GUID{Data1: 0x5CDF2C82, Data2: 0x841E, Data3: 0x4546, Data4: [8]byte{0x97, 0x22, 0x0C, 0xF7, 0x40, 0x78, 0x22, 0x9A}}
	clsidPolicyConfigClient = windows.GUID{Data1: 0x870AF99C, Data2: 0x171D, Data3: 0x4F9E, Data4: [8]byte{0xAF, 0x0D, 0xE6, 0x3D, 0xF4, 0x0C, 0x2B, 0xC9}}
	iidIPolicyConfig        = windows.GUID{Data1: 0xF8679F50, Data2: 0x850A, Data3: 0x41CF, Data4: [8]byte{0x9C, 0x72, 0x43, 0x0F, 0x29, 0x02, 0x90, 0xC8}}

	pkeyDeviceFriendlyName          = propertyKey{windows.GUID{Data1: 0xA45C254E, Data2: 0xDF1C, Data3: 0x4EFD, Data4: [8]byte{0x80, 0x20, 0x67, 0xD1, 0x46, 0xA8, 0x50, 0xE0}}, 14}
	pkeyDeviceDesc                  = propertyKey{windows.GUID{Data1: 0xA45C254E, Data2: 0xDF1C, Data3: 0x4EFD, Data4: [8]byte{0x80, 0x20, 0x67, 0xD1, 0x46, 0xA8, 0x50, 0xE0}}, 2}
	pkeyDeviceInterfaceFriendlyName = propertyKey{windows.GUID{Data1: 0x026E516E, Data2: 0xB814, Data3: 0x414B, Data4: [8]byte{0x83, 0xCD, 0x85, 0x6D, 0x6F, 0xEF, 0x48, 0x22}}, 2}
)

type propertyKey struct {
	fmtid windows.GUID
	pid   uint32
}

// propVariant mirrors PROPVARIANT (24 bytes on amd64).
type propVariant struct {
	vt       uint16
	reserved [6]byte
	val      *uint16 // pwszVal when vt == VT_LPWSTR
	val2     uintptr
}

const vtLPWSTR = 31

// comInterface is the memory layout of any COM interface: a vtable pointer.
type comInterface struct {
	vtbl *[32]uintptr
}

// comObject is a COM interface pointer.
type comObject = *comInterface

func (o *comInterface) call(method int, args ...uintptr) uintptr {
	r, _, _ := syscall.SyscallN(o.vtbl[method], append([]uintptr{uintptr(unsafe.Pointer(o))}, args...)...)
	return r
}

func (o *comInterface) release() {
	if o != nil {
		o.call(2)
	}
}

func hrErr(what string, hr uintptr) error {
	if int32(hr) < 0 {
		return fmt.Errorf("%s: HRESULT 0x%08X", what, uint32(hr))
	}
	return nil
}

func coInitialize() error {
	hr, _, _ := procCoInitializeEx.Call(0, coinitApartmentThread)
	if int32(hr) < 0 && uint32(hr) != rpcEChangedMode {
		return hrErr("CoInitializeEx", hr)
	}
	return nil
}

func coCreate(clsid, iid *windows.GUID) (comObject, error) {
	var obj comObject
	hr, _, _ := procCoCreateInstance.Call(uintptr(unsafe.Pointer(clsid)), 0, clsctxAll,
		uintptr(unsafe.Pointer(iid)), uintptr(unsafe.Pointer(&obj)))
	if err := hrErr("CoCreateInstance", hr); err != nil {
		return nil, err
	}
	return obj, nil
}

func takeCoString(p *uint16) string {
	if p == nil {
		return ""
	}
	s := windows.UTF16PtrToString(p)
	procCoTaskMemFree.Call(uintptr(unsafe.Pointer(p)))
	return s
}

func readStringProp(store comObject, key *propertyKey) string {
	var pv propVariant
	if hr := store.call(5, uintptr(unsafe.Pointer(key)), uintptr(unsafe.Pointer(&pv))); int32(hr) < 0 {
		return ""
	}
	defer procPropVariantClear.Call(uintptr(unsafe.Pointer(&pv)))
	if pv.vt != vtLPWSTR || pv.val == nil {
		return ""
	}
	return windows.UTF16PtrToString(pv.val)
}
