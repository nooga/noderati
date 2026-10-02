//go:build darwin

package host

import (
	"encoding/binary"
	"os/exec"
	"os/user"
	"strings"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

// macOS readers for node:os, matching libuv's src/unix/darwin.c.

func sysTotalMem() uint64 {
	v, _ := unix.SysctlUint64("hw.memsize")
	return v
}

// libuv: host_statistics(HOST_VM_INFO).free_count * page size.
func sysFreeMem() uint64 {
	free, err := unix.SysctlUint32("vm.page_free_count")
	if err != nil {
		return 0
	}
	return uint64(free) * uint64(unix.Getpagesize())
}

// libuv: whole seconds since kern.boottime.
func sysUptime() float64 {
	tv, err := unix.SysctlTimeval("kern.boottime")
	if err != nil {
		return 0
	}
	return float64(time.Now().Unix() - tv.Sec)
}

// vm.loadavg is struct loadavg { fixpt_t ldavg[3]; long fscale; }.
func sysLoadAvg() [3]float64 {
	raw, err := unix.SysctlRaw("vm.loadavg")
	if err != nil || len(raw) < 24 {
		return [3]float64{}
	}
	scale := float64(binary.LittleEndian.Uint64(raw[16:24]))
	var out [3]float64
	for i := range out {
		out[i] = float64(binary.LittleEndian.Uint32(raw[i*4:])) / scale
	}
	return out
}

func sysUname() unix.Utsname {
	var u unix.Utsname
	_ = unix.Uname(&u)
	return u
}

func sysKernelVersion() string { u := sysUname(); return unix.ByteSliceToString(u.Version[:]) }
func sysMachine() string       { u := sysUname(); return unix.ByteSliceToString(u.Machine[:]) }

func sysGetPriority(pid int) (int, error) {
	return unix.Getpriority(unix.PRIO_PROCESS, pid)
}

func sysSetPriority(pid, prio int) error {
	return unix.Setpriority(unix.PRIO_PROCESS, pid, prio)
}

// The login shell lives in Directory Services, not /etc/passwd, and
// os/user doesn't expose it; dscl reads the same record getpwuid does.
func sysLoginShell(u *user.User) string {
	out, err := exec.Command("/usr/bin/dscl", ".", "-read", "/Users/"+u.Username, "UserShell").Output()
	if err == nil {
		if _, shell, ok := strings.Cut(strings.TrimSpace(string(out)), "UserShell:"); ok {
			return strings.TrimSpace(shell)
		}
	}
	return passwdShell("/etc/passwd", u.Uid)
}

var sysDlopenConstants = []struct {
	name  string
	value int
}{{"RTLD_LAZY", 1}, {"RTLD_NOW", 2}, {"RTLD_GLOBAL", 8}, {"RTLD_LOCAL", 4}}

var sysSignalNames = []string{"SIGHUP", "SIGINT", "SIGQUIT", "SIGILL", "SIGTRAP", "SIGABRT", "SIGIOT", "SIGBUS",
	"SIGFPE", "SIGKILL", "SIGUSR1", "SIGSEGV", "SIGUSR2", "SIGPIPE", "SIGALRM", "SIGTERM", "SIGCHLD", "SIGCONT",
	"SIGSTOP", "SIGTSTP", "SIGTTIN", "SIGTTOU", "SIGURG", "SIGXCPU", "SIGXFSZ", "SIGVTALRM", "SIGPROF", "SIGWINCH",
	"SIGIO", "SIGINFO", "SIGSYS"}

func sysSignalNumber(name string) (syscall.Signal, bool) {
	if name == "SIGIOT" {
		return unix.SIGABRT, true
	}
	s := unix.SignalNum(name)
	return s, s != 0
}

var sysErrno = map[string]syscall.Errno{
	"E2BIG": unix.E2BIG, "EACCES": unix.EACCES, "EADDRINUSE": unix.EADDRINUSE, "EADDRNOTAVAIL": unix.EADDRNOTAVAIL,
	"EAFNOSUPPORT": unix.EAFNOSUPPORT, "EAGAIN": unix.EAGAIN, "EALREADY": unix.EALREADY, "EBADF": unix.EBADF,
	"EBADMSG": unix.EBADMSG, "EBUSY": unix.EBUSY, "ECANCELED": unix.ECANCELED, "ECHILD": unix.ECHILD,
	"ECONNABORTED": unix.ECONNABORTED, "ECONNREFUSED": unix.ECONNREFUSED, "ECONNRESET": unix.ECONNRESET,
	"EDEADLK": unix.EDEADLK, "EDESTADDRREQ": unix.EDESTADDRREQ, "EDOM": unix.EDOM, "EDQUOT": unix.EDQUOT,
	"EEXIST": unix.EEXIST, "EFAULT": unix.EFAULT, "EFBIG": unix.EFBIG, "EHOSTUNREACH": unix.EHOSTUNREACH,
	"EIDRM": unix.EIDRM, "EILSEQ": unix.EILSEQ, "EINPROGRESS": unix.EINPROGRESS, "EINTR": unix.EINTR,
	"EINVAL": unix.EINVAL, "EIO": unix.EIO, "EISCONN": unix.EISCONN, "EISDIR": unix.EISDIR, "ELOOP": unix.ELOOP,
	"EMFILE": unix.EMFILE, "EMLINK": unix.EMLINK, "EMSGSIZE": unix.EMSGSIZE, "EMULTIHOP": unix.EMULTIHOP,
	"ENAMETOOLONG": unix.ENAMETOOLONG, "ENETDOWN": unix.ENETDOWN, "ENETRESET": unix.ENETRESET,
	"ENETUNREACH": unix.ENETUNREACH, "ENFILE": unix.ENFILE, "ENOBUFS": unix.ENOBUFS, "ENODATA": unix.ENODATA,
	"ENODEV": unix.ENODEV, "ENOENT": unix.ENOENT, "ENOEXEC": unix.ENOEXEC, "ENOLCK": unix.ENOLCK,
	"ENOLINK": unix.ENOLINK, "ENOMEM": unix.ENOMEM, "ENOMSG": unix.ENOMSG, "ENOPROTOOPT": unix.ENOPROTOOPT,
	"ENOSPC": unix.ENOSPC, "ENOSR": unix.ENOSR, "ENOSTR": unix.ENOSTR, "ENOSYS": unix.ENOSYS,
	"ENOTCONN": unix.ENOTCONN, "ENOTDIR": unix.ENOTDIR, "ENOTEMPTY": unix.ENOTEMPTY, "ENOTSOCK": unix.ENOTSOCK,
	"ENOTSUP": unix.ENOTSUP, "ENOTTY": unix.ENOTTY, "ENXIO": unix.ENXIO, "EOPNOTSUPP": unix.EOPNOTSUPP,
	"EOVERFLOW": unix.EOVERFLOW, "EPERM": unix.EPERM, "EPIPE": unix.EPIPE, "EPROTO": unix.EPROTO,
	"EPROTONOSUPPORT": unix.EPROTONOSUPPORT, "EPROTOTYPE": unix.EPROTOTYPE, "ERANGE": unix.ERANGE,
	"EROFS": unix.EROFS, "ESPIPE": unix.ESPIPE, "ESRCH": unix.ESRCH, "ESTALE": unix.ESTALE, "ETIME": unix.ETIME,
	"ETIMEDOUT": unix.ETIMEDOUT, "ETXTBSY": unix.ETXTBSY, "EWOULDBLOCK": unix.EWOULDBLOCK, "EXDEV": unix.EXDEV,
}
