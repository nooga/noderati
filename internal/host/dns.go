package host

import (
	"context"
	"net"

	"github.com/nooga/paserati/pkg/driver"
	"github.com/nooga/paserati/pkg/vm"
)

// dns.go implements the one real, reachable piece of node:dns this
// dependency tree needs: dns.lookup, real, actual DNS resolution via
// Go's own net.DefaultResolver - not a synthetic answer. Real undici's
// lib/interceptor/dns.js is required unconditionally at index.js's own
// top level (`interceptors: { dns: require('./lib/interceptor/dns') }`),
// so `require('node:dns')` has to succeed just to load undici at all -
// but that interceptor's own DNS-caching feature is opt-in (nothing in
// pi's own real usage of undici enables it), so #defaultLookup (the
// only place lookup() is actually called) may never run in practice.
// Built for real anyway, not a stub, since Go's stdlib makes a genuine
// implementation just as cheap as a fake one - confirmed the exact real
// call shape directly from the source before writing this:
// `lookup(hostname, { all: true, family, order: 'ipv4first' }, (err,
// addresses) => {...})`, `addresses` being `[{address, family}]` when
// `all` is true.
func declareDNS() {
	registerJSShim("dns", dnsShim)
}

func installDNSNatives(p *driver.Paserati) {
	vmInst := p.GetVM()
	if vmInst == nil {
		return
	}
	gt, ok := vmInst.GetGlobal("globalThis")
	if !ok {
		return
	}
	obj := gt.AsPlainObject()
	if obj == nil {
		return
	}
	rt := vmInst.GetAsyncRuntime()

	obj.SetOwn("__noderatiDNSLookup", vm.NewNativeFunction(3, false, "__noderatiDNSLookup", func(args []vm.Value) (vm.Value, error) {
		if len(args) < 2 {
			return vm.Undefined, nil
		}
		hostname := args[0].ToString()
		opts := args[1].AsPlainObject()
		all := getBoolOpt(opts, "all", false)
		wantFamily := getIntOpt(opts, "family", 0)
		var cb vm.Value = vm.Undefined
		if len(args) > 2 {
			cb = args[2]
		}
		if !cb.IsCallable() {
			return vm.Undefined, nil
		}

		rt.BeginExternalOp()
		go func() {
			defer rt.EndExternalOp()
			addrs, err := net.DefaultResolver.LookupIPAddr(context.Background(), hostname)
			rt.ScheduleNextTick(func() {
				if err != nil {
					_, _ = vmInst.Call(cb, vm.Undefined, []vm.Value{errorValueFromGo(vmInst, err)})
					return
				}
				type resolved struct {
					addr   string
					family int
				}
				results := make([]resolved, 0, len(addrs))
				for _, a := range addrs {
					family := 6
					if a.IP.To4() != nil {
						family = 4
					}
					if wantFamily != 0 && wantFamily != family {
						continue
					}
					results = append(results, resolved{addr: a.IP.String(), family: family})
				}
				if all {
					arr := vm.NewArray()
					arrObj := arr.AsArray()
					for _, r := range results {
						o := vm.NewObject(vmInst.ObjectPrototype).AsPlainObject()
						o.SetOwn("address", vm.NewString(r.addr))
						o.SetOwn("family", vm.NumberValue(float64(r.family)))
						arrObj.Append(vm.NewValueFromPlainObject(o))
					}
					_, _ = vmInst.Call(cb, vm.Undefined, []vm.Value{vm.Undefined, arr})
					return
				}
				if len(results) == 0 {
					_, _ = vmInst.Call(cb, vm.Undefined, []vm.Value{errorValueFromGo(vmInst, &net.DNSError{Err: "no addresses found", Name: hostname, IsNotFound: true})})
					return
				}
				_, _ = vmInst.Call(cb, vm.Undefined, []vm.Value{vm.Undefined, vm.NewString(results[0].addr), vm.NumberValue(float64(results[0].family))})
			})
		}()
		return vm.Undefined, nil
	}))
}

const dnsShim = `const __lookup = globalThis.__noderatiDNSLookup;

function lookup(hostname, optionsOrCallback, maybeCallback) {
  let options = {};
  let callback = optionsOrCallback;
  if (typeof optionsOrCallback === "number") {
    options = { family: optionsOrCallback };
    callback = maybeCallback;
  } else if (typeof optionsOrCallback === "object" && optionsOrCallback !== null) {
    options = optionsOrCallback;
    callback = maybeCallback;
  }
  return __lookup(hostname, options, callback);
}

export { lookup };
export default { lookup };
`
