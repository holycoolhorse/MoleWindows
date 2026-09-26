//go:build windows

package uninstall

import (
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

const uninstallSubkey = `Software\Microsoft\Windows\CurrentVersion\Uninstall`

// readInstalledApps reads every Uninstall entry for this machine (64- and
// 32-bit views) and the current user.
func readInstalledApps() []App {
	var apps []App
	for _, src := range []struct {
		root  registry.Key
		label string
		path  string
		scope string
	}{
		{registry.LOCAL_MACHINE, "HKLM", uninstallSubkey, "machine"},
		{registry.LOCAL_MACHINE, "HKLM", `Software\WOW6432Node\Microsoft\Windows\CurrentVersion\Uninstall`, "machine"},
		{registry.CURRENT_USER, "HKCU", uninstallSubkey, "user"},
	} {
		parent, err := registry.OpenKey(src.root, src.path, registry.ENUMERATE_SUB_KEYS)
		if err != nil {
			continue
		}
		names, _ := parent.ReadSubKeyNames(-1)
		_ = parent.Close()
		for _, name := range names {
			k, err := registry.OpenKey(src.root, src.path+`\`+name, registry.QUERY_VALUE)
			if err != nil {
				continue
			}
			str := func(v string) string { s, _, _ := k.GetStringValue(v); return strings.TrimSpace(s) }
			num := func(v string) uint64 { n, _, _ := k.GetIntegerValue(v); return n }
			apps = append(apps, App{
				Key:             src.label + `\` + src.path + `\` + name,
				Name:            str("DisplayName"),
				Version:         str("DisplayVersion"),
				Publisher:       str("Publisher"),
				InstallLocation: str("InstallLocation"),
				UninstallString: str("UninstallString"),
				SizeKB:          num("EstimatedSize"),
				Scope:           src.scope,
				systemComponent: num("SystemComponent") == 1,
				parentKey:       str("ParentKeyName"),
				releaseType:     str("ReleaseType"),
			})
			_ = k.Close()
		}
	}
	return apps
}

// runUninstaller starts the app's own uninstaller with its exact command
// line (no shell) and waits for it. Uninstallers that demand elevation are
// relaunched through ShellExecuteExW "runas", which shows the UAC prompt.
func runUninstaller(program, cmdline string) error {
	cmd := exec.Command(program)
	cmd.SysProcAttr = &syscall.SysProcAttr{CmdLine: cmdline}
	err := cmd.Run()
	if err == nil {
		return nil
	}
	if !errors.Is(err, windows.ERROR_ELEVATION_REQUIRED) {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return exitCodeError(uint32(exitErr.ExitCode()))
		}
		return err
	}
	params := strings.TrimSpace(strings.TrimPrefix(cmdline, `"`+program+`"`))
	return runElevated(program, params)
}

// shellExecuteInfo mirrors SHELLEXECUTEINFOW on 64-bit Windows.
type shellExecuteInfo struct {
	cbSize       uint32
	fMask        uint32
	hwnd         uintptr
	lpVerb       *uint16
	lpFile       *uint16
	lpParameters *uint16
	lpDirectory  *uint16
	nShow        int32
	hInstApp     uintptr
	lpIDList     uintptr
	lpClass      *uint16
	hkeyClass    uintptr
	dwHotKey     uint32
	hIcon        uintptr
	hProcess     windows.Handle
}

// Fails to compile unless the struct has the 112-byte 64-bit layout.
var _ = [1]struct{}{}[unsafe.Sizeof(shellExecuteInfo{})-112]

var procShellExecuteExW = windows.NewLazySystemDLL("shell32.dll").NewProc("ShellExecuteExW")

func runElevated(program, params string) error {
	const (
		seeMaskNoCloseProcess = 0x00000040
		seeMaskNoAsync        = 0x00000100
		swShowNormal          = 1
	)
	verb, _ := windows.UTF16PtrFromString("runas")
	file, err := windows.UTF16PtrFromString(program)
	if err != nil {
		return err
	}
	args, err := windows.UTF16PtrFromString(params)
	if err != nil {
		return err
	}
	info := shellExecuteInfo{
		fMask:        seeMaskNoCloseProcess | seeMaskNoAsync,
		lpVerb:       verb,
		lpFile:       file,
		lpParameters: args,
		nShow:        swShowNormal,
	}
	info.cbSize = uint32(unsafe.Sizeof(info))
	if ret, _, callErr := procShellExecuteExW.Call(uintptr(unsafe.Pointer(&info))); ret == 0 {
		if errors.Is(callErr, windows.ERROR_CANCELLED) {
			return errors.New("administrator approval was declined")
		}
		return fmt.Errorf("could not start the uninstaller as administrator: %v", callErr)
	}
	if info.hProcess == 0 {
		return nil
	}
	defer windows.CloseHandle(info.hProcess) //nolint:errcheck // process handle
	if _, err := windows.WaitForSingleObject(info.hProcess, windows.INFINITE); err != nil {
		return err
	}
	var code uint32
	if err := windows.GetExitCodeProcess(info.hProcess, &code); err == nil {
		return exitCodeError(code)
	}
	return nil
}

// exitCodeError treats MSI's "success, restart required" codes as success.
func exitCodeError(code uint32) error {
	switch code {
	case 0, 3010, 1641: // ERROR_SUCCESS_REBOOT_REQUIRED, ERROR_SUCCESS_REBOOT_INITIATED
		return nil
	}
	return fmt.Errorf("uninstaller exited with code %d", code)
}
