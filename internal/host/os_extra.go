package host

import (
	"fmt"
	"net"
	"os"
	"os/user"
	"runtime"
	"strconv"
	"strings"
	"sync"

	"github.com/nooga/paserati/pkg/driver"
	"github.com/nooga/paserati/pkg/vm"
)

// os_extra.go: the rest of node:os - memory, uptime, load, user info,
// network interfaces, priorities and os.constants - read the way libuv
// reads them (os_sys_*.go holds the per-OS parts). Pure Go throughout.

func installOSExtras(m *driver.ModuleBuilder, vmInst *vm.VM) {
	m.Function("totalmem", func() float64 { return float64(sysTotalMem()) })
	m.Function("freemem", func() float64 { return float64(sysFreeMem()) })
	m.Function("uptime", sysUptime)
	m.Function("loadavg", func() []float64 {
		la := sysLoadAvg()
		return la[:]
	})
	m.Function("availableParallelism", func() int { return max(runtime.NumCPU(), 1) })
	m.Function("version", sysKernelVersion)
	m.Function("machine", sysMachine)
	m.Const("devNull", os.DevNull)
	m.Function("userInfo", func() (vm.Value, error) { return osUserInfo(vmInst) })
	m.Function("networkInterfaces", func() (vm.Value, error) { return osNetworkInterfaces(vmInst) })
	m.Function("getPriority", func(args ...vm.Value) (float64, error) {
		pid := 0
		if len(args) > 0 && !args[0].IsUndefined() {
			pid = int(args[0].ToFloat())
		}
		prio, err := sysGetPriority(pid)
		if err != nil {
			return 0, systemError(vmInst, "uv_os_getpriority", err)
		}
		return float64(prio), nil
	})
	m.Function("setPriority", func(args ...vm.Value) (vm.Value, error) {
		pid, prio := 0, argAt(args, 0)
		if len(args) > 1 {
			pid, prio = int(args[0].ToFloat()), args[1]
		}
		if err := sysSetPriority(pid, int(prio.ToFloat())); err != nil {
			return vm.Undefined, systemError(vmInst, "uv_os_setpriority", err)
		}
		return vm.Undefined, nil
	})
}

// installOSConstants runs after preload: os.constants is an object tree,
// which ModuleBuilder.Const can't carry.
func installOSConstants(p *driver.Paserati) {
	setNativeExport(p, "os", "constants", osConstants(p.GetVM()))
}

// systemError is Node's ERR_SYSTEM_ERROR shape for a failed uv_* call.
func systemError(vmInst *vm.VM, syscallName string, err error) error {
	code, errno := spawnErrCode(err)
	msg := fmt.Sprintf("A system error occurred: %s returned %s (%s)", syscallName, code, strings.ToLower(err.Error()))
	exception := newJSError(vmInst, msg)
	if obj := exception.AsPlainObject(); obj != nil {
		obj.SetOwn("name", vm.NewString("SystemError"))
		obj.SetOwn("code", vm.NewString("ERR_SYSTEM_ERROR"))
		obj.SetOwn("errno", vm.NumberValue(float64(errno)))
		obj.SetOwn("syscall", vm.NewString(syscallName))
	}
	return &fsSystemError{exception: exception, message: msg}
}

var (
	loginShellOnce sync.Once
	loginShell     string
)

func osUserInfo(vmInst *vm.VM) (vm.Value, error) {
	u, err := user.Current()
	if err != nil {
		return vm.Undefined, systemError(vmInst, "uv_os_get_passwd", err)
	}
	loginShellOnce.Do(func() { loginShell = sysLoginShell(u) })
	obj := vm.NewObject(vmInst.ObjectPrototype).AsPlainObject()
	uid, _ := strconv.Atoi(u.Uid)
	gid, _ := strconv.Atoi(u.Gid)
	obj.SetOwn("uid", vm.NumberValue(float64(uid)))
	obj.SetOwn("gid", vm.NumberValue(float64(gid)))
	obj.SetOwn("username", vm.NewString(u.Username))
	obj.SetOwn("homedir", vm.NewString(u.HomeDir))
	if loginShell == "" {
		obj.SetOwn("shell", vm.Null)
	} else {
		obj.SetOwn("shell", vm.NewString(loginShell))
	}
	return vm.NewValueFromPlainObject(obj), nil
}

// passwdShell reads uid's shell field from an /etc/passwd-format file.
func passwdShell(path, uid string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(data), "\n") {
		f := strings.Split(line, ":")
		if len(f) >= 7 && f[2] == uid {
			return f[6]
		}
	}
	return ""
}

// osNetworkInterfaces mirrors uv_interface_addresses: interfaces that are
// up and running, each address with its netmask, family, MAC, internal
// (loopback) flag, CIDR, and for IPv6 the scope id.
func osNetworkInterfaces(vmInst *vm.VM) (vm.Value, error) {
	ifaces, err := net.Interfaces()
	if err != nil {
		return vm.Undefined, systemError(vmInst, "uv_interface_addresses", err)
	}
	result := vm.NewObject(vmInst.ObjectPrototype).AsPlainObject()
	for _, iface := range ifaces {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagRunning == 0 {
			continue
		}
		addrs, err := iface.Addrs()
		if err != nil || len(addrs) == 0 {
			continue
		}
		mac := "00:00:00:00:00:00"
		if len(iface.HardwareAddr) == 6 {
			mac = iface.HardwareAddr.String()
		}
		list := vm.NewArray()
		for _, a := range addrs {
			ipnet, ok := a.(*net.IPNet)
			if !ok {
				continue
			}
			entry := vm.NewObject(vmInst.ObjectPrototype).AsPlainObject()
			ones, _ := ipnet.Mask.Size()
			if ip4 := ipnet.IP.To4(); ip4 != nil {
				entry.SetOwn("address", vm.NewString(ip4.String()))
				entry.SetOwn("netmask", vm.NewString(net.IP(ipnet.Mask).To4().String()))
				entry.SetOwn("family", vm.NewString("IPv4"))
			} else {
				entry.SetOwn("address", vm.NewString(ipnet.IP.String()))
				entry.SetOwn("netmask", vm.NewString(net.IP(ipnet.Mask).String()))
				entry.SetOwn("family", vm.NewString("IPv6"))
			}
			entry.SetOwn("mac", vm.NewString(mac))
			entry.SetOwn("internal", vm.BooleanValue(iface.Flags&net.FlagLoopback != 0))
			addr, _ := entry.GetOwn("address")
			entry.SetOwn("cidr", vm.NewString(addr.ToString()+"/"+strconv.Itoa(ones)))
			if ipnet.IP.To4() == nil {
				scope := 0
				if ipnet.IP.IsLinkLocalUnicast() || ipnet.IP.IsLinkLocalMulticast() {
					scope = iface.Index
				}
				entry.SetOwn("scopeid", vm.NumberValue(float64(scope)))
			}
			list.AsArray().Append(vm.NewValueFromPlainObject(entry))
		}
		if list.AsArray().Length() > 0 {
			result.SetOwn(iface.Name, list)
		}
	}
	return vm.NewValueFromPlainObject(result), nil
}

// Node's os.constants.errno names, in Node's order; values come from the
// platform (os_sys_*.go's sysErrno).
var nodeErrnoNames = []string{"E2BIG", "EACCES", "EADDRINUSE", "EADDRNOTAVAIL", "EAFNOSUPPORT", "EAGAIN", "EALREADY",
	"EBADF", "EBADMSG", "EBUSY", "ECANCELED", "ECHILD", "ECONNABORTED", "ECONNREFUSED", "ECONNRESET", "EDEADLK",
	"EDESTADDRREQ", "EDOM", "EDQUOT", "EEXIST", "EFAULT", "EFBIG", "EHOSTUNREACH", "EIDRM", "EILSEQ", "EINPROGRESS",
	"EINTR", "EINVAL", "EIO", "EISCONN", "EISDIR", "ELOOP", "EMFILE", "EMLINK", "EMSGSIZE", "EMULTIHOP",
	"ENAMETOOLONG", "ENETDOWN", "ENETRESET", "ENETUNREACH", "ENFILE", "ENOBUFS", "ENODATA", "ENODEV", "ENOENT",
	"ENOEXEC", "ENOLCK", "ENOLINK", "ENOMEM", "ENOMSG", "ENOPROTOOPT", "ENOSPC", "ENOSR", "ENOSTR", "ENOSYS",
	"ENOTCONN", "ENOTDIR", "ENOTEMPTY", "ENOTSOCK", "ENOTSUP", "ENOTTY", "ENXIO", "EOPNOTSUPP", "EOVERFLOW", "EPERM",
	"EPIPE", "EPROTO", "EPROTONOSUPPORT", "EPROTOTYPE", "ERANGE", "EROFS", "ESPIPE", "ESRCH", "ESTALE", "ETIME",
	"ETIMEDOUT", "ETXTBSY", "EWOULDBLOCK", "EXDEV"}

func osConstants(vmInst *vm.VM) vm.Value {
	newObj := func() *vm.PlainObject { return vm.NewObject(vmInst.ObjectPrototype).AsPlainObject() }
	c := newObj()
	c.SetOwn("UV_UDP_REUSEADDR", vm.NumberValue(4))
	dl := newObj()
	for _, kv := range sysDlopenConstants {
		dl.SetOwn(kv.name, vm.NumberValue(float64(kv.value)))
	}
	c.SetOwn("dlopen", vm.NewValueFromPlainObject(dl))
	errno := newObj()
	for _, name := range nodeErrnoNames {
		if v, ok := sysErrno[name]; ok {
			errno.SetOwn(name, vm.NumberValue(float64(v)))
		}
	}
	c.SetOwn("errno", vm.NewValueFromPlainObject(errno))
	sig := newObj()
	for _, name := range sysSignalNames {
		if s, ok := sysSignalNumber(name); ok {
			sig.SetOwn(name, vm.NumberValue(float64(s)))
		}
	}
	c.SetOwn("signals", vm.NewValueFromPlainObject(sig))
	prio := newObj()
	for _, kv := range []struct {
		name  string
		value int
	}{{"PRIORITY_LOW", 19}, {"PRIORITY_BELOW_NORMAL", 10}, {"PRIORITY_NORMAL", 0},
		{"PRIORITY_ABOVE_NORMAL", -7}, {"PRIORITY_HIGH", -14}, {"PRIORITY_HIGHEST", -20}} {
		prio.SetOwn(kv.name, vm.NumberValue(float64(kv.value)))
	}
	c.SetOwn("priority", vm.NewValueFromPlainObject(prio))
	return vm.NewValueFromPlainObject(c)
}
