package host

import (
	"math"
	"math/big"
	"os"
	"sync"
	"time"

	"github.com/nooga/paserati/pkg/vm"
)

// fs_classes.go holds the Go-built fs classes whose instances native code
// creates directly: Stats (and the unexported BigIntStats), Dirent, and
// StatFs. Each gets one shared prototype per VM with a real `constructor`,
// so `instanceof fs.Stats` / `instanceof fs.Dirent` hold as in Node.

type fsClassSet struct {
	statsProto, bigStatsProto, direntProto, statfsProto *vm.PlainObject
	statsCtor, direntCtor                               vm.Value
	direntType                                          vm.Value // the Symbol(type) key
}

var fsClassSets sync.Map // *vm.VM -> *fsClassSet

func fsClasses(vmInst *vm.VM) *fsClassSet {
	if c, ok := fsClassSets.Load(vmInst); ok {
		return c.(*fsClassSet)
	}
	c := &fsClassSet{}
	base := buildStatsBaseProto(vmInst)
	c.statsProto = buildStatsProto(vmInst, base)
	c.bigStatsProto = buildStatsProto(vmInst, base)
	c.statsCtor = buildCtor(c.statsProto, "Stats", 18, func(args []vm.Value) (vm.Value, error) {
		// Node 26's (deprecated) signature: the ten numeric fields, then
		// each timestamp as a (seconds, nanoseconds) pair.
		obj := vm.NewObject(vm.NewValueFromPlainObject(c.statsProto)).AsPlainObject()
		for i, name := range statsNumberFields {
			obj.SetOwn(name, argAt(args, i))
		}
		for i, name := range []string{"atimeMs", "mtimeMs", "ctimeMs", "birthtimeMs"} {
			sec, nsec := argAt(args, 10+2*i).ToFloat(), argAt(args, 11+2*i).ToFloat()
			obj.SetOwn(name, vm.NumberValue(sec*1e3+nsec/1e6))
		}
		return vm.NewValueFromPlainObject(obj), nil
	})
	buildCtor(c.bigStatsProto, "BigIntStats", 0, func([]vm.Value) (vm.Value, error) {
		return vm.NewValueFromPlainObject(vm.NewObject(vm.NewValueFromPlainObject(c.bigStatsProto)).AsPlainObject()), nil
	})
	c.direntType = vm.NewSymbol("type")
	c.direntProto = buildDirentProto(vmInst, c.direntType)
	c.direntCtor = buildCtor(c.direntProto, "Dirent", 3, func(args []vm.Value) (vm.Value, error) {
		return newDirentTyped(vmInst, argAt(args, 0), argAt(args, 2), argAt(args, 1)), nil
	})
	c.statfsProto = vm.NewObject(vmInst.ObjectPrototype).AsPlainObject()
	buildCtor(c.statfsProto, "StatFs", 0, func([]vm.Value) (vm.Value, error) {
		return vm.NewValueFromPlainObject(vm.NewObject(vm.NewValueFromPlainObject(c.statfsProto)).AsPlainObject()), nil
	})
	actual, _ := fsClassSets.LoadOrStore(vmInst, c)
	return actual.(*fsClassSet)
}

func buildCtor(proto *vm.PlainObject, name string, arity int, fn func(args []vm.Value) (vm.Value, error)) vm.Value {
	ctor := vm.NewConstructorWithProps(arity, true, name, fn)
	if props := ctor.AsNativeFunctionWithProps(); props != nil && props.Properties != nil {
		props.Properties.DefineFixedProperty("prototype", vm.NewValueFromPlainObject(proto))
	}
	proto.SetOwnNonEnumerable("constructor", ctor)
	return ctor
}

// statsPrototype is the shared prototype every (number) Stats object gets.
func statsPrototype(vmInst *vm.VM) *vm.PlainObject { return fsClasses(vmInst).statsProto }

var statsNumberFields = []string{"dev", "mode", "nlink", "uid", "gid", "rdev", "blksize", "ino", "size", "blocks"}

func valueUint(v vm.Value) uint64 {
	if v.IsBigInt() {
		return v.AsBigInt().Uint64()
	}
	return uint64(v.ToFloat())
}

// buildStatsBaseProto builds StatsBase.prototype, shared by Stats and
// BigIntStats: the is*() predicates read this.mode. Real Node's are
// enumerable (they show up in for-in), so these are too.
func buildStatsBaseProto(vmInst *vm.VM) *vm.PlainObject {
	proto := vm.NewObject(vmInst.ObjectPrototype).AsPlainObject()
	for _, e := range []struct {
		name string
		bits uint64
	}{
		{"isDirectory", sIFDIR}, {"isFile", sIFREG}, {"isBlockDevice", sIFBLK}, {"isCharacterDevice", sIFCHR},
		{"isSymbolicLink", sIFLNK}, {"isFIFO", sIFIFO}, {"isSocket", sIFSOCK},
	} {
		want := e.bits
		proto.SetOwn(e.name, vm.NewNativeFunction(0, false, e.name, func(_ []vm.Value) (vm.Value, error) {
			obj := vmInst.GetThis().AsPlainObject()
			if obj == nil {
				return vm.False, nil
			}
			modeVal, _ := obj.Get("mode")
			return vm.BooleanValue(valueUint(modeVal)&sIFMT == want), nil
		}))
	}
	return proto
}

// buildStatsProto builds Stats.prototype / BigIntStats.prototype: lazy
// Date and Temporal.Instant accessors over the timestamp fields.
func buildStatsProto(vmInst *vm.VM, base *vm.PlainObject) *vm.PlainObject {
	proto := vm.NewObject(vm.NewValueFromPlainObject(base)).AsPlainObject()
	// Real Node (22+) exposes the Date fields as lazy prototype accessors
	// over the *Ms numbers (so they're absent from Object.keys until used);
	// the first read, or any write, replaces the accessor with a plain own
	// enumerable data property.
	yes := true
	for _, name := range []string{"atime", "mtime", "ctime", "birthtime"} {
		field := name
		getter := vm.NewNativeFunction(0, false, "get "+field, func(_ []vm.Value) (vm.Value, error) {
			obj := vmInst.GetThis().AsPlainObject()
			if obj == nil {
				return vm.Undefined, nil
			}
			ms, _ := obj.Get(field + "Ms")
			date := newJSDate(vmInst, math.Trunc(ms.ToFloat()))
			obj.DefineOwnProperty(field, date, &yes, &yes, &yes)
			return date, nil
		})
		setter := vm.NewNativeFunction(1, false, "set "+field, func(args []vm.Value) (vm.Value, error) {
			if obj := vmInst.GetThis().AsPlainObject(); obj != nil {
				obj.DefineOwnProperty(field, argAt(args, 0), &yes, &yes, &yes)
			}
			return vm.Undefined, nil
		})
		proto.DefineAccessorProperty(field, getter, true, setter, true, &yes, &yes)
	}
	// atimeInstant & co: Temporal.Instant views of the same timestamps,
	// at full nanosecond precision for BigIntStats.
	for _, name := range []string{"atime", "mtime", "ctime", "birthtime"} {
		field := name
		getter := vm.NewNativeFunction(0, false, "get "+field+"Instant", func(_ []vm.Value) (vm.Value, error) {
			obj := vmInst.GetThis().AsPlainObject()
			if obj == nil {
				return vm.Undefined, nil
			}
			var ns *big.Int
			if v, ok := obj.Get(field + "Ns"); ok && v.IsBigInt() {
				ns = new(big.Int).Set(v.AsBigInt())
			} else {
				ms, _ := obj.Get(field + "Ms")
				ns = big.NewInt(int64(math.Round(ms.ToFloat() * 1e6)))
			}
			return temporalInstant(vmInst, ns)
		})
		proto.DefineAccessorProperty(field+"Instant", getter, true, vm.Undefined, false, &yes, &yes)
	}
	proto.SetOwn("_checkModeProperty", vm.NewNativeFunction(1, false, "_checkModeProperty", func(args []vm.Value) (vm.Value, error) {
		obj := vmInst.GetThis().AsPlainObject()
		if obj == nil {
			return vm.False, nil
		}
		modeVal, _ := obj.Get("mode")
		return vm.BooleanValue(valueUint(modeVal)&sIFMT == valueUint(argAt(args, 0))), nil
	}))
	return proto
}

func temporalInstant(vmInst *vm.VM, ns *big.Int) (vm.Value, error) {
	temporal, ok := vmInst.GetGlobal("Temporal")
	if !ok || temporal.IsUndefined() {
		return vm.Undefined, newJSErrorErr(vmInst, "Temporal is not supported in this environment")
	}
	instant, err := vmInst.GetProperty(temporal, "Instant")
	if err != nil {
		return vm.Undefined, err
	}
	from, err := vmInst.GetProperty(instant, "fromEpochNanoseconds")
	if err != nil {
		return vm.Undefined, err
	}
	return vmInst.Call(from, instant, []vm.Value{vm.NewBigInt(ns)})
}

func newJSErrorErr(vmInst *vm.VM, message string) error {
	return &fsSystemError{exception: newJSError(vmInst, message), message: message}
}

func timeNs(t time.Time) *big.Int {
	return new(big.Int).Add(new(big.Int).Mul(big.NewInt(t.Unix()), big.NewInt(1e9)), big.NewInt(int64(t.Nanosecond())))
}

// fsStatsValue builds a real Node-shaped fs.Stats object (every numeric
// field plus the atime/mtime/ctime/birthtime Dates) from a Go FileInfo.
// Real packages read far more than size/mtime: tar writes mode/uid/gid/
// atime/ctime into every header and keys hardlink detection on dev:ino
// when nlink > 1.
func fsStatsValue(vmInst *vm.VM, info os.FileInfo) vm.Value {
	return fsStatsValueOpt(vmInst, info, false)
}

// fsStatsValueOpt builds a Stats, or with bigint a BigIntStats (every
// field a BigInt, plus the *Ns nanosecond fields).
func fsStatsValueOpt(vmInst *vm.VM, info os.FileInfo, bigint bool) vm.Value {
	sys, ok := statSysFields(info)
	if !ok {
		mt := info.ModTime()
		sys = statSys{mode: modeFromFileInfo(info), nlink: 1, blksize: 4096, atime: mt, ctime: mt, birthtime: mt}
		sys.blocks = (info.Size() + 511) / 512
	}
	classes := fsClasses(vmInst)
	proto := classes.statsProto
	if bigint {
		proto = classes.bigStatsProto
	}
	obj := vm.NewObject(vm.NewValueFromPlainObject(proto)).AsPlainObject()
	vals := []uint64{sys.dev, uint64(sys.mode), sys.nlink, uint64(sys.uid), uint64(sys.gid), sys.rdev,
		uint64(sys.blksize), sys.ino, uint64(info.Size()), uint64(sys.blocks)}
	for i, name := range statsNumberFields {
		if bigint {
			obj.SetOwn(name, vm.NewBigInt(new(big.Int).SetUint64(vals[i])))
		} else {
			obj.SetOwn(name, vm.NumberValue(float64(vals[i])))
		}
	}
	times := []struct {
		name string
		at   time.Time
	}{{"atime", sys.atime}, {"mtime", info.ModTime()}, {"ctime", sys.ctime}, {"birthtime", sys.birthtime}}
	for _, t := range times {
		if bigint {
			obj.SetOwn(t.name+"Ms", vm.NewBigInt(new(big.Int).Quo(timeNs(t.at), big.NewInt(1e6))))
		} else {
			obj.SetOwn(t.name+"Ms", vm.NumberValue(timeToMs(t.at)))
		}
	}
	if bigint {
		for _, t := range times {
			obj.SetOwn(t.name+"Ns", vm.NewBigInt(timeNs(t.at)))
		}
	}
	return vm.NewValueFromPlainObject(obj)
}

// bigintRequested reports whether a stat-family options argument asked
// for { bigint: true }.
func bigintRequested(opts []vm.Value) bool {
	for _, v := range opts {
		if b, ok := objOption(v, "bigint"); ok && b.IsTruthy() {
			return true
		}
	}
	return false
}

// fsStatfsValue builds a StatFs object from fsStatfsRaw's fields.
func fsStatfsValue(vmInst *vm.VM, raw [8]uint64, bigint bool) vm.Value {
	obj := vm.NewObject(vm.NewValueFromPlainObject(fsClasses(vmInst).statfsProto)).AsPlainObject()
	for i, name := range []string{"type", "bsize", "frsize", "blocks", "bfree", "bavail", "files", "ffree"} {
		if bigint {
			obj.SetOwn(name, vm.NewBigInt(new(big.Int).SetUint64(raw[i])))
		} else {
			obj.SetOwn(name, vm.NumberValue(float64(raw[i])))
		}
	}
	return vm.NewValueFromPlainObject(obj)
}

// libuv's UV_DIRENT_* type codes, which real Node's Dirent stores.
const (
	uvDirentUnknown = iota
	uvDirentFile
	uvDirentDir
	uvDirentLink
	uvDirentFifo
	uvDirentSocket
	uvDirentChar
	uvDirentBlock
)

func direntTypeOf(mode os.FileMode) int {
	switch {
	case mode&os.ModeSymlink != 0:
		return uvDirentLink
	case mode.IsDir():
		return uvDirentDir
	case mode&os.ModeNamedPipe != 0:
		return uvDirentFifo
	case mode&os.ModeSocket != 0:
		return uvDirentSocket
	case mode&os.ModeCharDevice != 0:
		return uvDirentChar
	case mode&os.ModeDevice != 0:
		return uvDirentBlock
	case mode.IsRegular():
		return uvDirentFile
	}
	return uvDirentUnknown
}

func buildDirentProto(vmInst *vm.VM, typeSym vm.Value) *vm.PlainObject {
	proto := vm.NewObject(vmInst.ObjectPrototype).AsPlainObject()
	key := vm.NewSymbolKey(typeSym)
	for _, e := range []struct {
		name string
		typ  int
	}{
		{"isDirectory", uvDirentDir}, {"isFile", uvDirentFile}, {"isBlockDevice", uvDirentBlock},
		{"isCharacterDevice", uvDirentChar}, {"isSymbolicLink", uvDirentLink}, {"isFIFO", uvDirentFifo},
		{"isSocket", uvDirentSocket},
	} {
		want := float64(e.typ)
		proto.SetOwnNonEnumerable(e.name, vm.NewNativeFunction(0, false, e.name, func(_ []vm.Value) (vm.Value, error) {
			obj := vmInst.GetThis().AsPlainObject()
			if obj == nil {
				return vm.False, nil
			}
			t, _ := obj.GetOwnByKey(key)
			return vm.BooleanValue(t.ToFloat() == want), nil
		}))
	}
	return proto
}

func newDirentTyped(vmInst *vm.VM, name, parentPath, typ vm.Value) vm.Value {
	c := fsClasses(vmInst)
	obj := vm.NewObject(vm.NewValueFromPlainObject(c.direntProto)).AsPlainObject()
	obj.SetOwn("name", name)
	obj.SetOwn("parentPath", parentPath)
	yes := true
	obj.DefineOwnPropertyByKey(vm.NewSymbolKey(c.direntType), typ, &yes, &yes, &yes)
	return vm.NewValueFromPlainObject(obj)
}

// newDirent builds a real fs.Dirent: .name, .parentPath, and the is*()
// type checks on the shared Dirent prototype.
func newDirent(vmInst *vm.VM, name, parentPath string, mode os.FileMode) vm.Value {
	return newDirentTyped(vmInst, vm.NewString(name), vm.NewString(parentPath), vm.NumberValue(float64(direntTypeOf(mode))))
}

var fsConstantsObjects sync.Map // *vm.VM -> vm.Value

// fsConstantsObject is the one fs.constants object (shared with
// fs/promises.constants), keys in real Node's order.
func fsConstantsObject(vmInst *vm.VM) vm.Value {
	if v, ok := fsConstantsObjects.Load(vmInst); ok {
		return v.(vm.Value)
	}
	obj := vm.NewObject(vmInst.ObjectPrototype).AsPlainObject()
	for _, c := range fsConstantEntries() {
		obj.SetOwn(c.Name, vm.NumberValue(float64(c.Value)))
	}
	v, _ := fsConstantsObjects.LoadOrStore(vmInst, vm.NewValueFromPlainObject(obj))
	return v.(vm.Value)
}
