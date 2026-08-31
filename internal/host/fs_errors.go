package host

import (
	"errors"
	"fmt"
	"syscall"

	"github.com/nooga/paserati/pkg/vm"
)

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
	if errors.As(err, &errnoVal) {
		errno = -int(errnoVal)
		detail = errnoVal.Error()
		if c, ok := errnoToCode[errnoVal]; ok {
			code = c
		}
	}

	message := err.Error()
	if code != "" {
		message = fmt.Sprintf("%s: %s, %s '%s'", code, detail, syscallName, path)
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
		obj.SetOwn("path", vm.NewString(path))
	}

	return &fsSystemError{exception: exception, message: message}
}
