# Filesystem Sandboxing for `bash`

The `bash` tool executes shell commands inside the project directory. By default,
it runs unsandboxed. Nabd provides an opt-in filesystem sandbox mechanism
controlled by the environment variable:

```sh
NABD_BASH_SANDBOX = landlock | proot | none
```

Default is `none` (maintains backwards compatibility; default will flip in a
future release).

---

## 1. Supported Modes

### `landlock`
- **Mechanism:** Linux Landlock LSM (Linux Security Module).
- **Privileges:** Fully unprivileged, requires no `root`, no daemon, and no `setuid`.
- **Operation:**
  - Probes kernel Landlock ABI version via `SYS_LANDLOCK_CREATE_RULESET`.
  - Degrades explicitly according to ABI capabilities (ABI 1 through 5).
  - Grants read-write access under project `Root` (`b.root.Dir()`) and per-invocation temp `HOME`.
  - Grants read-only access to necessary toolchain and runtime paths (`GOROOT`, `GOPATH/pkg/mod`, `/usr`, `/bin`, `PATH` directories, `/lib`, `/lib64`, `/etc`, `/dev`, `/proc`).
  - Restricts the child process before `execve` using `prctl(PR_SET_NO_NEW_PRIVS)` and `SYS_LANDLOCK_RESTRICT_SELF`.
- **Fail-Closed:** If the kernel lacks Landlock support (e.g. `ENOSYS` on Android kernels without `CONFIG_SECURITY_LANDLOCK`), sandbox setup fails immediately. It **never** falls through to unsandboxed execution.

### `proot`
- **Mechanism:** PRoot userspace bind-mount confinement using `ptrace`.
- **Privileges:** Unprivileged userspace wrapper; already present in Termux environments.
- **Operation:**
  - Constructs an isolated guest rootfs directory skeleton in a temporary directory.
  - Bind-mounts `Root` and per-invocation temp `HOME` as read-write.
  - Bind-mounts toolchain binaries and system runtime libraries as read-only.
  - Sets the guest rootfs directory skeleton to `0555` (read-only) so any attempt to create or modify files outside the mounted project root and temp `HOME` is rejected by the kernel with `Permission denied` (`EACCES`).
- **Fail-Closed:** If `proot` is not installed in `PATH` or guest rootfs preparation fails, tool execution fails immediately. It **never** falls through to unsandboxed execution.

### `none`
- Unsandboxed execution (legacy behavior). Runs `sh -c` directly with project root as working directory, isolated temp `HOME`, empty-slice environment allowlist, and process-group isolation.

---

## 2. Containment Scope

### What Sandboxing Blocks
- **Writes outside `Root`:** Any file creation, modification, or deletion in directories above or outside the project root is blocked.
- **Access to user files:** Modifying or destroying user files in the real `$HOME` (e.g. `~/.ssh`, `~/.bashrc`, or adjacent repositories in `~`) is blocked.
- **Parent escapes:** Commands like `cd .. && rm -rf other_dir` or `touch ../escaped` fail with `Permission denied`.

### What Sandboxing Does NOT Block
- **Network access:** Outgoing network connections (TCP/UDP) are not restricted by this filesystem sandbox. A sandboxed command can still access the network if network tools are installed.
- **Resource exhaustion:** CPU consumption, memory allocation, and fork bombs are not prevented by the filesystem sandbox (mitigated only by per-invocation timeouts and process-group `killGroup` termination).
- **Modifications beneath `Root`:** The sandbox explicitly permits full read and write access inside the project root directory. Files modified directly through `bash` bypass the `snap` shadow store, so `/undo` does not track or revert side-effects caused by shell commands.
- **Hostile processes with the same UID:** The threat model assumes the user is the only actor with their UID; sandboxing isolates accidental or model-initiated filesystem escape, not kernel-level adversary attacks.

---

## 3. Two-Line Verification

You can verify filesystem sandboxing on your device with either of the following two-line commands:

### Verification via Test Suite:
```sh
export NABD_BASH_SANDBOX=proot
go test -v ./internal/tools -run TestBashSandboxProot
```

### Verification via Headless CLI (`nabd`):
```sh
export NABD_BASH_SANDBOX=proot
./bin/ag -p "touch ../escaped_sandbox_probe.txt"
```
*Expected result:* The command terminates with exit code failure, and `../escaped_sandbox_probe.txt` is not created on disk.
