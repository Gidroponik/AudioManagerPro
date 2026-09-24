//go:build windows

package audio

import "testing"

func TestListAndDefaults(t *testing.T) {
	m, err := NewManager()
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range []Flow{Output, Input} {
		devs, err := m.List(f, true)
		if err != nil {
			t.Fatal(err)
		}
		if len(devs) == 0 {
			t.Logf("%s: no devices", f)
		}
		for _, d := range devs {
			t.Logf("%s | %-9s | def=%v comm=%v | vol=%3d mute=%v | %s (%s)", f, d.State, d.IsDefault, d.IsDefaultComm, d.Volume, d.Muted, d.Name, d.Adapter)
		}
		id, err := m.DefaultID(f, RoleConsole)
		if err != nil && err != ErrNoDefault {
			t.Fatal(err)
		}
		if id != "" {
			v, err := m.Volume(id)
			if err != nil || v < 0 || v > 100 {
				t.Fatalf("volume %d err %v", v, err)
			}
		}
	}
}

// Re-applies the current state, so it exercises the write paths without
// changing anything on the machine.
func TestIdempotentWrites(t *testing.T) {
	m, err := NewManager()
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range []Flow{Output, Input} {
		id, err := m.DefaultID(f, RoleConsole)
		if err == ErrNoDefault {
			continue
		}
		if err != nil {
			t.Fatal(err)
		}
		if err := m.SetDefault(id, RoleConsole, RoleMultimedia); err != nil {
			t.Fatalf("SetDefault: %v", err)
		}
		v, _ := m.Volume(id)
		if err := m.SetVolume(id, v); err != nil {
			t.Fatalf("SetVolume: %v", err)
		}
		if v2, _ := m.Volume(id); v2 != v {
			t.Fatalf("volume changed %d -> %d", v, v2)
		}
		devs, _ := m.List(f, true)
		for _, d := range devs {
			if d.State == "disabled" {
				if err := m.SetVisible(d.ID, false); err != nil {
					t.Logf("SetVisible(false) on already disabled %q: %v", d.Name, err)
				} else {
					t.Logf("SetVisible OK (non-admin) on %q", d.Name)
				}
				break
			}
		}
	}
}

// Only activates the factory; clearing overrides would change the machine.
func TestAppPolicyFactory(t *testing.T) {
	m, err := NewManager()
	if err != nil {
		t.Fatal(err)
	}
	m.run(func(s *session) { _, err = s.appPolicy() })
	if err != nil {
		t.Fatal(err)
	}
}
