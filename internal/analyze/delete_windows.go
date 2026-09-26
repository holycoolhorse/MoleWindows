//go:build windows

package analyze

import (
	"errors"
	"fmt"
	"os"
	"os/user"
	"path/filepath"
	"runtime"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
)

// Shell file-operation constants from shellapi.h.
const (
	foDelete            = 0x0003
	fofSilent           = 0x0004
	fofNoConfirmation   = 0x0010
	fofAllowUndo        = 0x0040
	fofNoConfirmMkdir   = 0x0200
	fofNoErrorUI        = 0x0400
	fofWantNukeWarning  = 0x4000
	recycleBinOperation = fofAllowUndo | fofNoConfirmation | fofSilent | fofNoErrorUI | fofNoConfirmMkdir | fofWantNukeWarning
)

// shFileOpStruct mirrors SHFILEOPSTRUCTW for 64-bit Windows, where the header
// uses natural alignment (the pshpack1 packing applies to 32-bit builds only).
type shFileOpStruct struct {
	hwnd                  uintptr
	wFunc                 uint32
	pFrom                 *uint16
	pTo                   *uint16
	fFlags                uint16
	fAnyOperationsAborted int32
	hNameMappings         uintptr
	lpszProgressTitle     *uint16
}

// The layout above is only valid on 64-bit Windows. This fails to compile
// anywhere SHFILEOPSTRUCTW is not the 56-byte natural-alignment form, such as
// GOARCH=386 where shellapi.h packs it to 1 byte.
var _ = [1]struct{}{}[unsafe.Sizeof(shFileOpStruct{})-56]

var (
	modShell32           = windows.NewLazySystemDLL("shell32.dll")
	procSHFileOperationW = modShell32.NewProc("SHFileOperationW")
	modKernel32          = windows.NewLazySystemDLL("kernel32.dll")
	procGetConsoleWindow = modKernel32.NewProc("GetConsoleWindow")
)

// recycleBinMover is the final sink. Tests replace it to observe exactly which
// paths reach the shell without touching the real Recycle Bin.
var recycleBinMover = moveToRecycleBin

// driveTypeForPath is replaceable in tests.
var driveTypeForPath = recycleBinDriveType

// GetFinalPathNameByHandleW flags (fileapi.h).
const (
	fileNameNormalized = 0x0
	volumeNameDOS      = 0x0
	volumeNameGUID     = 0x1
)

// recycleBinDriveType reports the drive type of the volume that will hold the
// item in the Recycle Bin, which is the volume of its parent directory: the
// shell recycles a symlink or junction itself, never its target. The drive
// letter alone is not enough, because the parent can be a volume mount folder
// on C:, or sit behind a junction or symlink that leads to another volume or a
// share. The parent's lexical and final (all reparse points followed) paths
// must both be on a fixed volume; anything that cannot be resolved reports
// DRIVE_UNKNOWN.
func recycleBinDriveType(absPath string) uint32 {
	if isVolumeMountFolder(absPath) {
		// Deleting a mount folder would act on a whole volume.
		return windows.DRIVE_UNKNOWN
	}
	parent := filepath.Dir(filepath.Clean(absPath))
	final, err := finalPathName(parent)
	if err != nil {
		return windows.DRIVE_UNKNOWN
	}
	for _, p := range []string{parent, final} {
		if t := classifyVolumePath(p); t != windows.DRIVE_FIXED {
			return t
		}
	}
	return windows.DRIVE_FIXED
}

// classifyVolumePath maps a DOS, \\?\ or \\?\Volume{GUID} path to its drive
// type. UNC forms are network paths; everything else is resolved by the
// volume manager.
func classifyVolumePath(p string) uint32 {
	upper := strings.ToUpper(p)
	if strings.HasPrefix(upper, `\\?\UNC\`) {
		return windows.DRIVE_REMOTE
	}
	if (strings.HasPrefix(p, `\\`) || strings.HasPrefix(p, "//")) &&
		!strings.HasPrefix(p, `\\?\`) && !strings.HasPrefix(p, `\\.\`) {
		return windows.DRIVE_REMOTE
	}
	return volumeDriveType(p)
}

// isVolumeMountFolder reports whether path is itself a folder where a volume
// is mounted: GetVolumePathNameW returns the path as its own mount point.
// Drive roots are excluded; they are protected separately.
func isVolumeMountFolder(path string) bool {
	clean := filepath.Clean(path)
	if isFilesystemRoot(clean) {
		return false
	}
	p, err := windows.UTF16PtrFromString(clean)
	if err != nil {
		return true
	}
	buf := make([]uint16, windows.MAX_LONG_PATH)
	if err := windows.GetVolumePathName(p, &buf[0], uint32(len(buf))); err != nil {
		return false
	}
	return strings.EqualFold(filepath.Clean(windows.UTF16ToString(buf)), clean)
}

// volumeDriveType asks for the mount point that actually holds path
// (GetVolumePathNameW follows volume mount folders) and returns its type.
func volumeDriveType(path string) uint32 {
	p, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return windows.DRIVE_UNKNOWN
	}
	buf := make([]uint16, windows.MAX_LONG_PATH)
	if err := windows.GetVolumePathName(p, &buf[0], uint32(len(buf))); err != nil {
		return windows.DRIVE_UNKNOWN
	}
	return windows.GetDriveType(&buf[0])
}

// finalPathName follows every symlink, junction, and mount point in dir.
// A directory on a volume without a drive letter has no DOS name, so the GUID
// form is the fallback.
func finalPathName(dir string) (string, error) {
	p, err := windows.UTF16PtrFromString(dir)
	if err != nil {
		return "", err
	}
	h, err := windows.CreateFile(p, 0,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
		nil, windows.OPEN_EXISTING, windows.FILE_FLAG_BACKUP_SEMANTICS, 0)
	if err != nil {
		return "", err
	}
	defer windows.CloseHandle(h) //nolint:errcheck // read-only query handle

	buf := make([]uint16, windows.MAX_LONG_PATH)
	for _, flags := range []uint32{fileNameNormalized | volumeNameDOS, fileNameNormalized | volumeNameGUID} {
		n, err := windows.GetFinalPathNameByHandle(h, &buf[0], uint32(len(buf)), flags)
		if err == nil && n > 0 && int(n) < len(buf) {
			return windows.UTF16ToString(buf[:n]), nil
		}
	}
	return "", errors.New("cannot resolve final path")
}

var errRecycleBinUnavailable = errors.New("recycle bin is not available on this drive; delete it manually if intended")

// moveToTrash moves a file or directory to the Recycle Bin. Mole never
// deletes permanently on its own: drives without a Recycle Bin are refused, and
// an item the bin cannot hold is left to the shell's own confirmation prompt.
func moveToTrash(path string) error {
	// Validate raw input before Abs resolves ".." components away.
	if err := validateTrashTarget(path); err != nil {
		return err
	}

	absPath, err := filepath.Abs(path)
	if err != nil {
		return fmt.Errorf("failed to resolve path: %w", err)
	}

	// Validate resolved path as well (defense-in-depth).
	if err := validateTrashTarget(absPath); err != nil {
		return err
	}

	// Only local fixed drives have a Recycle Bin. On removable, network, or
	// RAM drives the shell deletes permanently, which analyze never does.
	if driveTypeForPath(absPath) != windows.DRIVE_FIXED {
		return errRecycleBinUnavailable
	}

	if err := recycleBinMover(absPath); err != nil {
		return err
	}
	if _, statErr := os.Lstat(absPath); statErr == nil {
		return fmt.Errorf("failed to move to Recycle Bin: %s is still present", absPath)
	}
	return nil
}

// moveToRecycleBin calls SHFileOperationW with FOF_ALLOWUNDO. The call runs on
// a locked, COM-initialized thread because the shell implements it on top of
// IFileOperation. FOF_WANTNUKEWARNING makes the shell ask before destroying
// anything it cannot recycle (for example an item larger than the bin); the
// console window owns that dialog so it opens in front of the terminal, and
// answering No leaves the item in place.
func moveToRecycleBin(absPath string) error {
	from, err := windows.UTF16FromString(absPath)
	if err != nil {
		return fmt.Errorf("invalid path: %w", err)
	}
	// pFrom is a double-null-terminated list.
	from = append(from, 0)

	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	if coErr := windows.CoInitializeEx(0, windows.COINIT_APARTMENTTHREADED|windows.COINIT_DISABLE_OLE1DDE); coErr == nil {
		defer windows.CoUninitialize()
	}

	consoleWindow, _, _ := procGetConsoleWindow.Call()
	op := shFileOpStruct{
		hwnd:   consoleWindow,
		wFunc:  foDelete,
		pFrom:  &from[0],
		fFlags: recycleBinOperation,
	}
	ret, _, _ := procSHFileOperationW.Call(uintptr(unsafe.Pointer(&op)))
	runtime.KeepAlive(from)
	if ret != 0 {
		return fmt.Errorf("failed to move to Recycle Bin: shell error 0x%x", ret)
	}
	if op.fAnyOperationsAborted != 0 {
		return fmt.Errorf("move to Recycle Bin was cancelled")
	}
	return nil
}

// validateWindowsPathShape rejects path forms that bypass drive-letter checks:
// UNC and device paths (\\server\share, \\?\C:\), and NTFS alternate data
// streams (C:\file:stream).
func validateWindowsPathShape(path string) error {
	if strings.HasPrefix(path, `\\`) || strings.HasPrefix(path, `//`) {
		return fmt.Errorf("network and device paths are not supported: %s", path)
	}
	volume := filepath.VolumeName(path)
	if len(volume) != 2 || volume[1] != ':' {
		return fmt.Errorf("path must start with a drive letter: %s", path)
	}
	if strings.Contains(path[len(volume):], ":") {
		return fmt.Errorf("alternate data streams are not supported: %s", path)
	}
	return nil
}

// MoveToRecycleBin moves path to the Recycle Bin with every analyze safety
// check (protected paths, drive type, path shape). Other Windows commands use
// it so all recoverable deletes share one sink.
func MoveToRecycleBin(path string) error {
	return moveToTrash(path)
}

// IsProtectedPath exposes the Windows protected-path policy to the other
// Windows commands (clean), so every delete surface refuses the same paths.
func IsProtectedPath(path string) bool {
	return isProtectedAnalyzeDeletePath(path)
}

func isProtectedAnalyzeDeletePath(path string) bool {
	if path == "" {
		return false
	}
	if validateWindowsPathShape(path) != nil {
		return true
	}

	cleanPath := filepath.Clean(path)

	// Volume roots (C:\, D:\) are never an Analyze delete target.
	if isFilesystemRoot(cleanPath) {
		return true
	}

	for _, root := range windowsCriticalExactPaths() {
		if pathEqualFold(cleanPath, root) || isSameExistingPath(cleanPath, root) {
			return true
		}
	}

	for _, root := range windowsProtectedTrees() {
		if pathWithinFold(cleanPath, root) || isPathWithinExistingRoot(cleanPath, root) {
			return true
		}
	}

	// Entries directly under a volume root that belong to the OS.
	volumeRoot := filepath.VolumeName(cleanPath) + `\`
	for name := range windowsVolumeSystemEntries {
		if pathWithinFold(cleanPath, filepath.Join(volumeRoot, name)) {
			return true
		}
	}

	// The Users directory and each account profile root directly under it.
	for _, usersRoot := range windowsUsersRoots() {
		if pathEqualFold(cleanPath, usersRoot) || isSameExistingPath(cleanPath, usersRoot) ||
			pathEqualFold(filepath.Dir(cleanPath), usersRoot) || isDirectChildOfExistingRoot(cleanPath, usersRoot) {
			return true
		}
	}

	return false
}

// windowsVolumeSystemEntries are OS-owned names at any volume root. The whole
// tree under each is protected.
var windowsVolumeSystemEntries = map[string]bool{
	"$Recycle.Bin":              true,
	"System Volume Information": true,
	"$WinREAgent":               true,
	"$SysReset":                 true,
	"$Windows.~BT":              true,
	"$Windows.~WS":              true,
	"Recovery":                  true,
	"Boot":                      true,
	"EFI":                       true,
	"Config.Msi":                true,
	"Documents and Settings":    true,
	"pagefile.sys":              true,
	"hiberfil.sys":              true,
	"swapfile.sys":              true,
	"DumpStack.log.tmp":         true,
	"bootmgr":                   true,
	"BOOTNXT":                   true,
}

// windowsCriticalExactPaths are directories that may hold deletable children
// but must never be deleted as a whole.
func windowsCriticalExactPaths() []string {
	var paths []string
	for _, env := range []string{
		"ProgramData", "PUBLIC", "USERPROFILE", "APPDATA", "LOCALAPPDATA", "TEMP", "TMP",
		"OneDrive", "OneDriveConsumer", "OneDriveCommercial",
	} {
		if v := os.Getenv(env); v != "" {
			paths = append(paths, filepath.Clean(v))
		}
	}
	for _, home := range protectedAnalyzeHomeRoots() {
		paths = append(paths,
			home,
			filepath.Join(home, "AppData"),
			filepath.Join(home, "AppData", "Local"),
			filepath.Join(home, "AppData", "LocalLow"),
			filepath.Join(home, "AppData", "Roaming"),
			filepath.Join(home, "AppData", "Local", "Temp"),
			filepath.Join(home, "OneDrive"),
		)
	}
	return paths
}

// windowsProtectedTrees are trees whose every descendant is off limits:
// the OS, installed programs (removed through their uninstallers, not the
// Recycle Bin), MSI repair sources, and security or container state.
func windowsProtectedTrees() []string {
	var trees []string
	add := func(p string) {
		if p != "" {
			trees = append(trees, filepath.Clean(p))
		}
	}
	systemRoot := os.Getenv("SystemRoot")
	if systemRoot == "" {
		systemRoot = os.Getenv("windir")
	}
	add(systemRoot)
	add(os.Getenv("ProgramFiles"))
	add(os.Getenv("ProgramFiles(x86)"))
	add(os.Getenv("ProgramW6432"))
	if programData := os.Getenv("ProgramData"); programData != "" {
		for _, sub := range []string{
			"Microsoft", "Package Cache", "Packages", "regid.1991-06.com.microsoft",
			"CrowdStrike", "SentinelOne", "ESET", "Palo Alto Networks", "Cisco", "Sophos",
			"Docker", "DockerDesktop",
		} {
			add(filepath.Join(programData, sub))
		}
	}
	for _, home := range protectedAnalyzeHomeRoots() {
		local := filepath.Join(home, "AppData", "Local")
		add(filepath.Join(local, "Packages"))
		add(filepath.Join(local, "Docker"))
		add(filepath.Join(local, "Microsoft", "Credentials"))
		add(filepath.Join(local, "Microsoft", "Windows", "UsrClass.dat"))
		add(filepath.Join(home, "AppData", "Roaming", "Microsoft", "Credentials"))
		add(filepath.Join(home, "AppData", "Roaming", "Microsoft", "Protect"))
		add(filepath.Join(home, "AppData", "Roaming", "Microsoft", "SystemCertificates"))
		add(filepath.Join(home, "NTUSER.DAT"))
	}
	return trees
}

// windowsUsersRoots returns the directory that holds account profiles.
func windowsUsersRoots() []string {
	var roots []string
	if drive := os.Getenv("SystemDrive"); drive != "" {
		roots = append(roots, filepath.Join(drive+`\`, "Users"))
	}
	for _, home := range protectedAnalyzeHomeRoots() {
		roots = append(roots, filepath.Dir(home))
	}
	return roots
}

func protectedAnalyzeHomeRoots() []string {
	var homeRoots []string
	seen := make(map[string]bool)
	add := func(home string) {
		if home == "" {
			return
		}
		clean := filepath.Clean(home)
		if key := strings.ToLower(clean); !seen[key] {
			seen[key] = true
			homeRoots = append(homeRoots, clean)
		}
	}
	add(os.Getenv("USERPROFILE"))
	if currentUser, err := user.Current(); err == nil {
		add(currentUser.HomeDir)
	}
	return homeRoots
}

// pathEqualFold compares two cleaned paths the way NTFS does by default.
func pathEqualFold(a, b string) bool {
	return strings.EqualFold(filepath.Clean(a), filepath.Clean(b))
}

// pathWithinFold reports whether path is root or a descendant of root,
// case-insensitively.
func pathWithinFold(path, root string) bool {
	path = filepath.Clean(path)
	root = filepath.Clean(root)
	if strings.EqualFold(path, root) {
		return true
	}
	prefix := root
	if !strings.HasSuffix(prefix, `\`) {
		prefix += `\`
	}
	return len(path) > len(prefix) && strings.EqualFold(path[:len(prefix)], prefix)
}
