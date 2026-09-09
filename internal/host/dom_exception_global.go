package host

import (
	"github.com/nooga/paserati/pkg/driver"
	"github.com/nooga/paserati/pkg/vm"
)

// dom_exception_global.go implements the WHATWG `DOMException` global -
// found missing while probing real undici (round 74, docs/real-node-plan.md):
// real undici's lib/web/websocket/util.js, eventsource.js, and
// websocket.js all do `throw new DOMException(message, name)` directly,
// and lib/web/websocket/stream/websocketerror.js does
// `class Test extends DOMException { get reason() {...} }` at its own
// module top level (a real Node bug workaround check - nodejs/node#59677)
// before ever constructing a real WebSocketError. A missing DOMException
// throws "DOMException is not defined" immediately, before any of that
// runs.
//
// Built with the real legacy numeric `.code` table the WHATWG spec still
// defines (an instance whose `name` matches one of the 25 legacy names
// gets the matching code, everything else gets 0) - a real, if small,
// static lookup, not a stand-in. Per spec, DOMException does NOT extend
// Error (checked directly against the spec, not assumed) - it's its own
// interface with just message/name/code - so this doesn't pretend it's
// an Error subclass either. Native Go constructor with a real
// `.prototype`, not JS run through EvalCode/RunCode, for the same reason
// file_global.go is (see its own doc comment - paserati#298).
var domExceptionLegacyCodes = map[string]int{
	"IndexSizeError":              1,
	"DOMStringSizeError":          2,
	"HierarchyRequestError":       3,
	"WrongDocumentError":          4,
	"InvalidCharacterError":       5,
	"NoDataAllowedError":          6,
	"NoModificationAllowedError":  7,
	"NotFoundError":               8,
	"NotSupportedError":           9,
	"InUseAttributeError":         10,
	"InvalidStateError":           11,
	"SyntaxError":                 12,
	"InvalidModificationError":    13,
	"NamespaceError":              14,
	"InvalidAccessError":          15,
	"ValidationError":             16,
	"TypeMismatchError":           17,
	"SecurityError":               18,
	"NetworkError":                19,
	"AbortError":                  20,
	"URLMismatchError":            21,
	"QuotaExceededError":          22,
	"TimeoutError":                23,
	"InvalidNodeTypeError":        24,
	"DataCloneError":              25,
	"EncodingError":               0,
	"NotReadableError":            0,
	"UnknownError":                0,
	"ConstraintError":             0,
	"DataError":                   0,
	"TransactionInactiveError":    0,
	"ReadOnlyError":               0,
	"VersionError":                0,
	"OperationError":              0,
	"NotAllowedError":             0,
}

func installDOMExceptionGlobal(p *driver.Paserati) {
	vmInst := p.GetVM()
	if vmInst == nil {
		return
	}
	gt, ok := vmInst.GetGlobal("globalThis")
	if !ok {
		return
	}
	gobj := gt.AsPlainObject()
	if gobj == nil {
		return
	}
	if _, exists := gobj.GetOwn("DOMException"); exists {
		return
	}

	proto := vm.NewObject(vmInst.ObjectPrototype).AsPlainObject()

	ctor := vm.NewConstructorWithProps(2, false, "DOMException", func(args []vm.Value) (vm.Value, error) {
		obj := vm.NewObject(vm.NewValueFromPlainObject(proto)).AsPlainObject()
		self := vm.NewValueFromPlainObject(obj)

		message := ""
		if len(args) > 0 && !args[0].IsUndefined() {
			message = args[0].ToString()
		}
		name := "Error"
		if len(args) > 1 && !args[1].IsUndefined() {
			name = args[1].ToString()
		}
		code := domExceptionLegacyCodes[name]

		obj.SetOwn("message", vm.NewString(message))
		obj.SetOwn("name", vm.NewString(name))
		obj.SetOwn("code", vm.NumberValue(float64(code)))

		return self, nil
	})
	if props := ctor.AsNativeFunctionWithProps(); props != nil && props.Properties != nil {
		props.Properties.DefineFixedProperty("prototype", vm.NewValueFromPlainObject(proto))
		// Legacy static/instance code constants (DOMException.SYNTAX_ERR
		// === 12, etc.) - real, spec-defined names, not made up.
		legacyConstantNames := map[string]int{
			"INDEX_SIZE_ERR": 1, "DOMSTRING_SIZE_ERR": 2, "HIERARCHY_REQUEST_ERR": 3,
			"WRONG_DOCUMENT_ERR": 4, "INVALID_CHARACTER_ERR": 5, "NO_DATA_ALLOWED_ERR": 6,
			"NO_MODIFICATION_ALLOWED_ERR": 7, "NOT_FOUND_ERR": 8, "NOT_SUPPORTED_ERR": 9,
			"INUSE_ATTRIBUTE_ERR": 10, "INVALID_STATE_ERR": 11, "SYNTAX_ERR": 12,
			"INVALID_MODIFICATION_ERR": 13, "NAMESPACE_ERR": 14, "INVALID_ACCESS_ERR": 15,
			"VALIDATION_ERR": 16, "TYPE_MISMATCH_ERR": 17, "SECURITY_ERR": 18,
			"NETWORK_ERR": 19, "ABORT_ERR": 20, "URL_MISMATCH_ERR": 21,
			"QUOTA_EXCEEDED_ERR": 22, "TIMEOUT_ERR": 23, "INVALID_NODE_TYPE_ERR": 24,
			"DATA_CLONE_ERR": 25,
		}
		for constName, code := range legacyConstantNames {
			props.Properties.DefineFixedProperty(constName, vm.NumberValue(float64(code)))
			proto.DefineFixedProperty(constName, vm.NumberValue(float64(code)))
		}
	}
	proto.SetOwnNonEnumerable("constructor", ctor)

	gobj.SetOwn("DOMException", ctor)
}
