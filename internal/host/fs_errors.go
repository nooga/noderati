package host

import (
	"errors"
	"fmt"
	"syscall"

	"github.com/nooga/paserati/pkg/vm"
)

// uvErrMessages is libuv's own uv_strerror text, which real Node puts in
// SystemError messages ("EEXIST: file already exists, ..."); Go's
// strerror wording differs for many codes ("file exists").
var uvErrMessages = map[string]string{
	"E2BIG": "argument list too long", "EACCES": "permission denied", "EADDRINUSE": "address already in use",
	"EADDRNOTAVAIL": "address not available", "EAFNOSUPPORT": "address family not supported",
	"EAGAIN": "resource temporarily unavailable", "EALREADY": "connection already in progress",
	"EBADF": "bad file descriptor", "EBUSY": "resource busy or locked", "ECANCELED": "operation canceled",
	"ECONNABORTED": "software caused connection abort", "ECONNREFUSED": "connection refused",
	"ECONNRESET": "connection reset by peer", "EDESTADDRREQ": "destination address required",
	"EEXIST": "file already exists", "EFAULT": "bad address in system call argument", "EFBIG": "file too large",
	"EHOSTUNREACH": "host is unreachable", "EILSEQ": "illegal byte sequence", "EINTR": "interrupted system call",
	"EINVAL": "invalid argument", "EIO": "i/o error", "EISCONN": "socket is already connected",
	"EISDIR": "illegal operation on a directory", "ELOOP": "too many symbolic links encountered",
	"EMFILE": "too many open files", "EMLINK": "too many links", "EMSGSIZE": "message too long",
	"ENAMETOOLONG": "name too long", "ENETDOWN": "network is down", "ENETUNREACH": "network is unreachable",
	"ENFILE": "file table overflow", "ENOBUFS": "no buffer space available", "ENODATA": "no data available",
	"ENODEV": "no such device", "ENOENT": "no such file or directory", "ENOEXEC": "exec format error",
	"ENOMEM": "not enough memory", "ENOPROTOOPT": "protocol not available", "ENOSPC": "no space left on device",
	"ENOSYS": "function not implemented", "ENOTCONN": "socket is not connected", "ENOTDIR": "not a directory",
	"ENOTEMPTY": "directory not empty", "ENOTSOCK": "socket operation on non-socket",
	"ENOTSUP": "operation not supported on socket", "ENOTTY": "inappropriate ioctl for device",
	"ENXIO": "no such device or address", "EOVERFLOW": "value too large for defined data type",
	"EPERM": "operation not permitted", "EPIPE": "broken pipe", "EPROTO": "protocol error",
	"EPROTONOSUPPORT": "protocol not supported", "EPROTOTYPE": "protocol wrong type for socket",
	"ERANGE": "result too large", "EROFS": "read-only file system", "ESPIPE": "invalid seek",
	"ESRCH": "no such process", "ETIMEDOUT": "connection timed out", "ETXTBSY": "text file is busy",
	"EXDEV": "cross-device link not permitted",
}

// fsDestError carries the second path of a two-path operation (rename,
// link, symlink, copyfile), which real Node reports as `dest` and in the
// message as "'src' -> 'dest'".
type fsDestError struct {
	err  error
	dest string
}

func (e *fsDestError) Error() string { return e.err.Error() }
func (e *fsDestError) Unwrap() error { return e.err }

func withDest(err error, dest string) error {
	if err == nil {
		return nil
	}
	return &fsDestError{err: err, dest: dest}
}

// errnoToCode maps a Go syscall.Errno to the Node-style error code real
// packages branch on (err.code === 'ENOENT', etc.). syscall.E* constants
// are correctly platform-dispatched by Go itself (same pattern as
// constants.go's O_* flags), so this table is portable as written. Covers
// the codes real packages actually seem to check in practice, not the
// full errno space.
var errnoToCode = map[syscall.Errno]string{
	syscall.ENOENT:       "ENOENT",
	syscall.EACCES:       "EACCES",
	syscall.EEXIST:       "EEXIST",
	syscall.ENOTDIR:      "ENOTDIR",
	syscall.EISDIR:       "EISDIR",
	syscall.EPERM:        "EPERM",
	syscall.ENOTEMPTY:    "ENOTEMPTY",
	syscall.EMFILE:       "EMFILE",
	syscall.ENFILE:       "ENFILE",
	syscall.ELOOP:        "ELOOP",
	syscall.ENAMETOOLONG: "ENAMETOOLONG",
	syscall.EINVAL:       "EINVAL",
	syscall.EBUSY:        "EBUSY",
	syscall.EXDEV:        "EXDEV",
	syscall.ENOSPC:       "ENOSPC",
	syscall.EROFS:        "EROFS",
	syscall.EBADF:        "EBADF",
}

// fsSystemError is a Go error implementing vm.ExceptionError, wrapping a
// real JS Error object shaped like Node's SystemError: .code, .errno,
// .syscall, .path set alongside the usual .message/.name/.stack. Real
// Node code overwhelmingly branches on err.code (`if (e.code ===
// 'ENOENT')`), not err.message — an fs error without .code is invisible
// to that extremely common idiom (proper-lockfile's own checkSync is what
// surfaced this: it couldn't tell "not locked" from a real failure).
type fsSystemError struct {
	exception vm.Value
	message   string
}

func (e *fsSystemError) Error() string               { return e.message }
func (e *fsSystemError) GetExceptionValue() vm.Value { return e.exception }

// wrapFsErr classifies a Go stdlib fs error and returns a Go error that
// throws as a real, Node-shaped SystemError when returned from a native
// function. Returns nil unchanged. syscallName is the low-level operation
// Node itself would report (e.g. "open" for a readFileSync failure,
// "scandir" for readdirSync) — see call sites for the mapping used here.
func wrapFsErr(vmInst *vm.VM, syscallName, path string, err error) error {
	if err == nil {
		return nil
	}

	var errnoVal syscall.Errno
	code := ""
	errno := 0
	detail := err.Error()
	if fe, ok := err.(*fsSystemError); ok {
		return fe
	}
	dest := ""
	var de *fsDestError
	if errors.As(err, &de) {
		dest = de.dest
	}
	if errors.As(err, &errnoVal) {
		errno = -int(errnoVal)
		detail = errnoVal.Error()
		if c, ok := errnoToCode[errnoVal]; ok {
			code = c
		} else if c := errnoName(errnoVal); c != "" {
			code = c
		}
		if m, ok := uvErrMessages[code]; ok {
			detail = m
		}
	}

	message := err.Error()
	if code != "" {
		switch {
		case path == "":
			message = fmt.Sprintf("%s: %s, %s", code, detail, syscallName)
		case dest != "":
			message = fmt.Sprintf("%s: %s, %s '%s' -> '%s'", code, detail, syscallName, path, dest)
		default:
			message = fmt.Sprintf("%s: %s, %s '%s'", code, detail, syscallName, path)
		}
	}

	exception, built := vm.Undefined, false
	if errCtor, ok := vmInst.GetGlobal("Error"); ok {
		if v, cerr := vmInst.Construct(errCtor, []vm.Value{vm.NewString(message)}); cerr == nil {
			exception, built = v, true
		}
	}
	if !built {
		// Fall back to a plain object shaped enough like an Error for
		// property access to still work, if Error somehow isn't global.
		obj := vm.NewObject(vmInst.ObjectPrototype).AsPlainObject()
		obj.SetOwn("name", vm.NewString("Error"))
		obj.SetOwn("message", vm.NewString(message))
		exception = vm.NewValueFromPlainObject(obj)
	}
	if obj := exception.AsPlainObject(); obj != nil {
		if code != "" {
			obj.SetOwn("code", vm.NewString(code))
			obj.SetOwn("errno", vm.NumberValue(float64(errno)))
		}
		obj.SetOwn("syscall", vm.NewString(syscallName))
		if path != "" {
			obj.SetOwn("path", vm.NewString(path))
		}
		if dest != "" {
			obj.SetOwn("dest", vm.NewString(dest))
		}
	}

	return &fsSystemError{exception: exception, message: message}
}
