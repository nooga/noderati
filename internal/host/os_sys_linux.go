//go:build linux

package host

import (
	"os"
	"os/user"
	"strconv"
	"strings"
	"syscall"

	"golang.org/x/sys/unix"
)

// Linux readers for node:os, matching libuv's src/unix/linux.c.

func meminfoKB(field string) uint64 {
	data, err := os.ReadFile("/proc/meminfo")
	if err != nil {
		return 0
	}
	for _, line := range strings.Split(string(data), "\n") {
		if rest, ok := strings.CutPrefix(line, field+":"); ok {
			n, _ := strconv.ParseUint(strings.TrimSuffix(strings.TrimSpace(rest), " kB"), 10, 64)
			return n
		}
	}
	return 0
}

func sysTotalMem() uint64 {
	if kb := meminfoKB("MemTotal"); kb > 0 {
		return kb * 1024
	}
	var si unix.Sysinfo_t
	if unix.Sysinfo(&si) == nil {
		return uint64(si.Totalram) * uint64(si.Unit)
	}
	return 0
}

// libuv reports MemAvailable, falling back to sysinfo's freeram.
func sysFreeMem() uint64 {
	if kb := meminfoKB("MemAvailable"); kb > 0 {
		return kb * 1024
	}
	var si unix.Sysinfo_t
	if unix.Sysinfo(&si) == nil {
		return uint64(si.Freeram) * uint64(si.Unit)
	}
	return 0
}

func sysUptime() float64 {
	if data, err := os.ReadFile("/proc/uptime"); err == nil {
		if f := strings.Fields(string(data)); len(f) > 0 {
			if v, err := strconv.ParseFloat(f[0], 64); err == nil {
				return v
			}
		}
	}
	var si unix.Sysinfo_t
	if unix.Sysinfo(&si) == nil {
		return float64(si.Uptime)
	}
	return 0
}

func sysLoadAvg() [3]float64 {
	var out [3]float64
	if data, err := os.ReadFile("/proc/loadavg"); err == nil {
		f := strings.Fields(string(data))
		for i := 0; i < 3 && i < len(f); i++ {
			out[i], _ = strconv.ParseFloat(f[i], 64)
		}
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

// The raw getpriority syscall returns 20 - nice; libc (and libuv)
// report the nice value itself.
func sysGetPriority(pid int) (int, error) {
	r, err := unix.Getpriority(unix.PRIO_PROCESS, pid)
	if err != nil {
		return 0, err
	}
	return 20 - r, nil
}

func sysSetPriority(pid, prio int) error {
	return unix.Setpriority(unix.PRIO_PROCESS, pid, prio)
}

func sysLoginShell(u *user.User) string { return passwdShell("/etc/passwd", u.Uid) }

var sysDlopenConstants = []struct {
	name  string
	value int
}{{"RTLD_LAZY", 1}, {"RTLD_NOW", 2}, {"RTLD_GLOBAL", 256}, {"RTLD_LOCAL", 0}, {"RTLD_DEEPBIND", 8}}

var sysSignalNames = []string{"SIGHUP", "SIGINT", "SIGQUIT", "SIGILL", "SIGTRAP", "SIGABRT", "SIGIOT", "SIGBUS",
	"SIGFPE", "SIGKILL", "SIGUSR1", "SIGSEGV", "SIGUSR2", "SIGPIPE", "SIGALRM", "SIGTERM", "SIGCHLD", "SIGSTKFLT",
	"SIGCONT", "SIGSTOP", "SIGTSTP", "SIGTTIN", "SIGTTOU", "SIGURG", "SIGXCPU", "SIGXFSZ", "SIGVTALRM", "SIGPROF",
	"SIGWINCH", "SIGIO", "SIGPOLL", "SIGPWR", "SIGSYS"}

func sysSignalNumber(name string) (syscall.Signal, bool) {
	switch name {
	case "SIGIOT":
		return unix.SIGABRT, true
	case "SIGPOLL":
		return unix.SIGIO, true
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
