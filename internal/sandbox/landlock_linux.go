//go:build linux

package sandbox

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"unsafe"

	"golang.org/x/sys/unix"
)

const (
	landlockCreateRuleset = uintptr(444)
	landlockAddRule       = uintptr(445)
	landlockRestrictSelf  = uintptr(446)

	landlockRulePathBeneath = uintptr(1)

	// A ruleset must be associated with no-new-privileges before it can be
	// installed. The value is the stable Linux prctl operation number.
	prSetNoNewPrivs = uintptr(unix.PR_SET_NO_NEW_PRIVS)
)

type rulesetAttr struct {
	HandledAccessFS  uint64
	HandledAccessNet uint64
}

type pathBeneath struct {
	AllowedAccess uint64
	ParentFD      int32
	_             int32
}

var ErrUnavailable = errors.New("landlock sandbox is unavailable on this kernel")
var ErrNetworkUnavailable = errors.New("landlock network restrictions are unavailable on this kernel")
var ErrResourcesUnavailable = errors.New("bash resource limits are unavailable on this platform")

const (
	resourceCPUSeconds       = uint64(600)
	resourceAddressSpaceByte = uint64(4 << 30)
	resourceProcessCount     = uint64(256)
	resourceOpenFileCount    = uint64(1024)
	resourceFileSizeByte     = uint64(256 << 20)
)

func landlockCall(number uintptr, args ...uintptr) (uintptr, error) {
	var a [6]uintptr
	copy(a[:], args)
	r, _, errno := unix.Syscall6(number, a[0], a[1], a[2], a[3], a[4], a[5])
	if errno != 0 {
		return 0, errno
	}
	return r, nil
}

func kernelABI() (int, error) {
	r, err := landlockCall(
		landlockCreateRuleset,
		0,
		0,
		uintptr(unix.LANDLOCK_CREATE_RULESET_VERSION),
	)
	if err != nil {
		return 0, err
	}
	return int(r), nil
}

// Available reports whether the running kernel supports Landlock rulesets.
// The query is read-only and does not install a restriction.
func Available() bool {
	abi, err := kernelABI()
	return err == nil && abi >= 1
}

// SupportsNetwork reports whether the kernel supports Landlock TCP rules.
func SupportsNetwork() bool {
	abi, err := kernelABI()
	return err == nil && abi >= 4
}

// ResourcesAvailable reports whether this Linux helper can install the
// resource limits used by the opt-in Bash policy.
func ResourcesAvailable() bool { return true }

func handledAccess(abi int) uint64 {
	access := uint64(
		unix.LANDLOCK_ACCESS_FS_EXECUTE |
			unix.LANDLOCK_ACCESS_FS_WRITE_FILE |
			unix.LANDLOCK_ACCESS_FS_READ_FILE |
			unix.LANDLOCK_ACCESS_FS_READ_DIR |
			unix.LANDLOCK_ACCESS_FS_REMOVE_DIR |
			unix.LANDLOCK_ACCESS_FS_REMOVE_FILE |
			unix.LANDLOCK_ACCESS_FS_MAKE_CHAR |
			unix.LANDLOCK_ACCESS_FS_MAKE_DIR |
			unix.LANDLOCK_ACCESS_FS_MAKE_REG |
			unix.LANDLOCK_ACCESS_FS_MAKE_SOCK |
			unix.LANDLOCK_ACCESS_FS_MAKE_FIFO |
			unix.LANDLOCK_ACCESS_FS_MAKE_BLOCK |
			unix.LANDLOCK_ACCESS_FS_MAKE_SYM,
	)
	if abi >= 2 {
		access |= uint64(unix.LANDLOCK_ACCESS_FS_REFER)
	}
	if abi >= 3 {
		access |= uint64(unix.LANDLOCK_ACCESS_FS_TRUNCATE)
	}
	return access
}

func readOnlyAccess() uint64 {
	return uint64(
		unix.LANDLOCK_ACCESS_FS_EXECUTE |
			unix.LANDLOCK_ACCESS_FS_READ_FILE |
			unix.LANDLOCK_ACCESS_FS_READ_DIR,
	)
}

func networkAccess() uint64 {
	return uint64(unix.LANDLOCK_ACCESS_NET_BIND_TCP | unix.LANDLOCK_ACCESS_NET_CONNECT_TCP)
}

func openPath(path string) (int, error) {
	return unix.Open(path, unix.O_PATH|unix.O_CLOEXEC, 0)
}

func addPathRule(rulesetFD int, path string, access uint64, required bool) error {
	fd, err := openPath(path)
	if err != nil {
		if !required && (errors.Is(err, unix.ENOENT) || errors.Is(err, unix.ENOTDIR)) {
			return nil
		}
		return fmt.Errorf("open sandbox path %q: %w", path, err)
	}
	defer unix.Close(fd)

	rule := pathBeneath{AllowedAccess: access, ParentFD: int32(fd)}
	_, err = landlockCall(
		landlockAddRule,
		uintptr(rulesetFD),
		landlockRulePathBeneath,
		uintptr(unsafe.Pointer(&rule)),
		0,
	)
	if err != nil {
		return fmt.Errorf("add sandbox path %q: %w", path, err)
	}
	return nil
}

func setNoNewPrivs() error {
	_, err := landlockCall(unix.SYS_PRCTL, prSetNoNewPrivs, 1)
	if err != nil {
		return fmt.Errorf("set no-new-privileges: %w", err)
	}
	return nil
}

func applyResourceLimits() error {
	limits := []struct {
		name     string
		resource int
		value    uint64
	}{
		{"cpu", unix.RLIMIT_CPU, resourceCPUSeconds},
		{"address space", unix.RLIMIT_AS, resourceAddressSpaceByte},
		{"processes", unix.RLIMIT_NPROC, resourceProcessCount},
		{"open files", unix.RLIMIT_NOFILE, resourceOpenFileCount},
		{"file size", unix.RLIMIT_FSIZE, resourceFileSizeByte},
	}
	for _, limit := range limits {
		rlim := unix.Rlimit{Cur: limit.value, Max: limit.value}
		if err := unix.Setrlimit(limit.resource, &rlim); err != nil {
			return fmt.Errorf("set %s limit: %w", limit.name, err)
		}
	}
	return nil
}

// Apply installs a Landlock boundary in the current process. It must be
// called in the child immediately before exec. Filesystem access is always
// restricted; TCP bind/connect are restricted only when DenyNetwork is true.
func Apply(cfg Config) error {
	if !Available() {
		return ErrUnavailable
	}
	root, err := filepath.Abs(cfg.Root)
	if err != nil {
		return fmt.Errorf("resolve sandbox root: %w", err)
	}
	if st, err := os.Stat(root); err != nil || !st.IsDir() {
		if err == nil {
			err = syscall.ENOTDIR
		}
		return fmt.Errorf("sandbox root %q: %w", root, err)
	}

	abi, err := kernelABI()
	if err != nil {
		return ErrUnavailable
	}
	if cfg.DenyNetwork && abi < 4 {
		return ErrNetworkUnavailable
	}
	handled := handledAccess(abi)
	attr := rulesetAttr{HandledAccessFS: handled}
	attrSize := unsafe.Sizeof(attr.HandledAccessFS)
	if cfg.DenyNetwork {
		attr.HandledAccessNet = networkAccess()
		attrSize = unsafe.Sizeof(attr)
	}
	rulesetFD, err := landlockCall(
		landlockCreateRuleset,
		uintptr(unsafe.Pointer(&attr)),
		attrSize,
		0,
	)
	if err != nil {
		return fmt.Errorf("create Landlock ruleset: %w", err)
	}
	defer unix.Close(int(rulesetFD))

	if err := addPathRule(int(rulesetFD), root, handled, true); err != nil {
		return err
	}
	seenWritable := map[string]bool{root: true}
	for _, path := range cfg.Writable {
		abs, err := filepath.Abs(path)
		if err != nil {
			return fmt.Errorf("resolve writable sandbox path %q: %w", path, err)
		}
		if seenWritable[abs] {
			continue
		}
		if err := addPathRule(int(rulesetFD), abs, handled, true); err != nil {
			return err
		}
		seenWritable[abs] = true
	}
	seenReadOnly := map[string]bool{}
	for _, path := range cfg.ReadOnly {
		abs, err := filepath.Abs(path)
		if err != nil {
			return fmt.Errorf("resolve read-only sandbox path %q: %w", path, err)
		}
		if seenWritable[abs] || seenReadOnly[abs] {
			continue
		}
		if err := addPathRule(int(rulesetFD), abs, readOnlyAccess(), false); err != nil {
			return err
		}
		seenReadOnly[abs] = true
	}

	if err := setNoNewPrivs(); err != nil {
		return err
	}
	if _, err := landlockCall(landlockRestrictSelf, rulesetFD, 0); err != nil {
		return fmt.Errorf("restrict process with Landlock: %w", err)
	}
	if cfg.LimitResources {
		if err := applyResourceLimits(); err != nil {
			return err
		}
	}
	return nil
}

// Run is the internal helper entry point used by cmd/ag. args are:
// root, home, temp, network mode, resource mode, executable, and arguments.
func Run(args []string) int {
	if len(args) < 6 {
		fmt.Fprintln(os.Stderr, "nabd: invalid sandbox helper arguments")
		return 2
	}
	root, home, temp, networkMode, resourcesMode, executable := args[0], args[1], args[2], args[3], args[4], args[5]
	if networkMode != "allow" && networkMode != "deny" {
		fmt.Fprintln(os.Stderr, "nabd: invalid sandbox network mode")
		return 2
	}
	if resourcesMode != "allow" && resourcesMode != "limit" {
		fmt.Fprintln(os.Stderr, "nabd: invalid sandbox resource mode")
		return 2
	}
	if err := os.Chdir(root); err != nil {
		fmt.Fprintf(os.Stderr, "nabd: sandbox chdir: %v\n", err)
		return 126
	}

	writable := []string{root}
	if home != "" {
		writable = append(writable, home)
	}
	if temp != "" {
		writable = append(writable, temp)
	}
	readOnly := []string{
		"/bin", "/usr/bin", "/usr/local/bin",
		"/sbin", "/usr/sbin",
		"/lib", "/lib64", "/usr/lib", "/usr/lib64",
		"/etc",
		"/dev/null", "/dev/urandom", "/dev/random",
	}
	if err := Apply(Config{
		Root:           root,
		Writable:       writable,
		ReadOnly:       readOnly,
		DenyNetwork:    networkMode == "deny",
		LimitResources: resourcesMode == "limit",
	}); err != nil {
		fmt.Fprintf(os.Stderr, "nabd: Bash sandbox unavailable: %v\n", err)
		return 126
	}
	if err := syscall.Exec(executable, args[5:], os.Environ()); err != nil {
		fmt.Fprintf(os.Stderr, "nabd: sandbox exec: %v\n", err)
		return 126
	}
	return 126
}
