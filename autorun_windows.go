package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strings"
	"syscall"
	"unicode/utf16"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

// The app requires elevation, and Windows silently skips elevated programs
// listed under the Run registry key. A logon task with "highest privileges"
// is the supported way to autostart an admin app without a UAC prompt.
const taskName = "AudioManagerPro"

var procGetOEMCP = windows.NewLazySystemDLL("kernel32.dll").NewProc("GetOEMCP")

const runKeyPath = `Software\Microsoft\Windows\CurrentVersion\Run`

func isElevated() bool {
	return windows.GetCurrentProcessToken().IsElevated()
}

func hiddenCmd(name string, args ...string) *exec.Cmd {
	cmd := exec.Command(name, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: 0x08000000} // CREATE_NO_WINDOW
	return cmd
}

func autorunEnabled() bool {
	return hiddenCmd("schtasks", "/Query", "/TN", taskName).Run() == nil
}

func setAutorun(enable bool) error {
	// Clean up a plain Run entry in case an older build or the user added one.
	if k, err := registry.OpenKey(registry.CURRENT_USER, runKeyPath, registry.SET_VALUE); err == nil {
		_ = k.DeleteValue(taskName)
		k.Close()
	}
	if !enable {
		if !autorunEnabled() {
			return nil
		}
		if out, err := hiddenCmd("schtasks", "/Delete", "/TN", taskName, "/F").CombinedOutput(); err != nil {
			return fmt.Errorf("schtasks: %s", strings.TrimSpace(decodeOEM(out)))
		}
		return nil
	}
	if !isElevated() {
		return errors.New("для автозапуска с правами администратора запустите приложение от имени администратора")
	}
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	u, err := user.Current()
	if err != nil {
		return err
	}
	xmlPath := filepath.Join(os.TempDir(), "AudioManagerPro-task.xml")
	if err := os.WriteFile(xmlPath, utf16LE(taskXML(exe, u.Username)), 0o644); err != nil {
		return err
	}
	defer os.Remove(xmlPath)
	if out, err := hiddenCmd("schtasks", "/Create", "/TN", taskName, "/XML", xmlPath, "/F").CombinedOutput(); err != nil {
		return fmt.Errorf("schtasks: %s", strings.TrimSpace(decodeOEM(out)))
	}
	return nil
}

func taskXML(exe, userID string) string {
	esc := func(s string) string {
		r := strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;")
		return r.Replace(s)
	}
	return `<?xml version="1.0" encoding="UTF-16"?>
<Task version="1.2" xmlns="http://schemas.microsoft.com/windows/2004/02/mit/task">
  <RegistrationInfo><Description>AudioManagerPro autostart</Description></RegistrationInfo>
  <Triggers>
    <LogonTrigger><Enabled>true</Enabled><UserId>` + esc(userID) + `</UserId><Delay>PT5S</Delay></LogonTrigger>
  </Triggers>
  <Principals>
    <Principal id="Author">
      <UserId>` + esc(userID) + `</UserId>
      <LogonType>InteractiveToken</LogonType>
      <RunLevel>HighestAvailable</RunLevel>
    </Principal>
  </Principals>
  <Settings>
    <MultipleInstancesPolicy>IgnoreNew</MultipleInstancesPolicy>
    <DisallowStartIfOnBatteries>false</DisallowStartIfOnBatteries>
    <StopIfGoingOnBatteries>false</StopIfGoingOnBatteries>
    <AllowHardTerminate>true</AllowHardTerminate>
    <StartWhenAvailable>true</StartWhenAvailable>
    <RunOnlyIfNetworkAvailable>false</RunOnlyIfNetworkAvailable>
    <IdleSettings><StopOnIdleEnd>false</StopOnIdleEnd><RestartOnIdle>false</RestartOnIdle></IdleSettings>
    <AllowStartOnDemand>true</AllowStartOnDemand>
    <Enabled>true</Enabled>
    <Hidden>false</Hidden>
    <RunOnlyIfIdle>false</RunOnlyIfIdle>
    <ExecutionTimeLimit>PT0S</ExecutionTimeLimit>
    <Priority>7</Priority>
  </Settings>
  <Actions Context="Author">
    <Exec>
      <Command>"` + esc(exe) + `"</Command>
      <Arguments>--autostart</Arguments>
      <WorkingDirectory>` + esc(filepath.Dir(exe)) + `</WorkingDirectory>
    </Exec>
  </Actions>
</Task>
`
}

func utf16LE(s string) []byte {
	u := utf16.Encode([]rune(s))
	b := make([]byte, 2, 2+len(u)*2)
	b[0], b[1] = 0xFF, 0xFE
	for _, c := range u {
		b = append(b, byte(c), byte(c>>8))
	}
	return b
}

// decodeOEM converts console output (OEM code page, e.g. CP866) to UTF-8.
func decodeOEM(b []byte) string {
	if len(b) == 0 {
		return ""
	}
	r, _, _ := procGetOEMCP.Call()
	cp := uint32(r)
	n, err := windows.MultiByteToWideChar(cp, 0, &b[0], int32(len(b)), nil, 0)
	if err != nil || n == 0 {
		return string(b)
	}
	w := make([]uint16, n)
	windows.MultiByteToWideChar(cp, 0, &b[0], int32(len(b)), &w[0], n)
	return string(utf16.Decode(w))
}
