package host

import "strings"

// httpModuleShimTemplate is shared by both node:http and node:https - the
// two differ only in which scheme request()/get() bind to (real Node's
// http module always speaks plain HTTP, https always TLS, regardless of
// what a caller's options happen to say), so scheme is baked in per module
// as a literal at shim-build time rather than read from options.
const httpModuleShimTemplate = `const Agent = globalThis.__noderatiHTTPAgentCtor;
__NODERATI_MAX_HEADER_SIZE__

function normalizeOptions(urlOrOptions, maybeOptions) {
  if (typeof urlOrOptions === "string") {
    const u = new URL(urlOrOptions);
    return {
      hostname: u.hostname,
      port: u.port || undefined,
      path: u.pathname + u.search,
      ...maybeOptions,
    };
  }
  if (urlOrOptions instanceof URL) {
    return {
      hostname: urlOrOptions.hostname,
      port: urlOrOptions.port || undefined,
      path: urlOrOptions.pathname + urlOrOptions.search,
      ...maybeOptions,
    };
  }
  return { ...urlOrOptions, ...maybeOptions };
}

function request(urlOrOptions, maybeOptionsOrCb, maybeCb) {
  let options;
  let callback = maybeOptionsOrCb;
  if (typeof maybeOptionsOrCb === "object" && maybeOptionsOrCb !== null) {
    options = normalizeOptions(urlOrOptions, maybeOptionsOrCb);
    callback = maybeCb;
  } else {
    options = normalizeOptions(urlOrOptions, undefined);
  }
  const req = globalThis.__noderatiHTTPRequest("__NODERATI_SCHEME__", options);
  if (typeof callback === "function") {
    req.on("response", callback);
  }
  return req;
}

function get(urlOrOptions, maybeOptionsOrCb, maybeCb) {
  const req = request(urlOrOptions, maybeOptionsOrCb, maybeCb);
  req.end();
  return req;
}

__NODERATI_EXPORTS__
`

// maxHeaderSize: real Node's own default (16384 bytes, since v13.13.0;
// overridable at process startup via --max-http-header-size, which this
// project has no equivalent CLI flag for, so it's a fixed constant here).
// Only http.maxHeaderSize is real - https has no such export in real
// Node - but every real consumer reads it off require("node:http")
// regardless of which scheme it's about to use (real undici's own
// lib/dispatcher/client.js does exactly this unconditionally), so this
// only needs to exist on the http shim. Found missing while re-probing
// real undici's fetch() (round 75, docs/real-node-plan.md): Client's
// module-load-time getDefaultNodeMaxHeaderSize check
// (`http && http.maxHeaderSize && Number.isInteger(...) && ... > 0`)
// throws InvalidArgumentError the instant any Client/Pool is
// constructed without it - a real, unconditional call path, not an
// edge case.
const httpMaxHeaderSizeDecl = "const maxHeaderSize = 16384;"

func renderHTTPShim(scheme string, withMaxHeaderSize bool) string {
	s := strings.ReplaceAll(httpModuleShimTemplate, "__NODERATI_SCHEME__", scheme)
	exportsList := "request, get, Agent"
	defaultExports := "{ request, get, Agent }"
	maxHeaderSizeDecl := ""
	if withMaxHeaderSize {
		maxHeaderSizeDecl = httpMaxHeaderSizeDecl
		exportsList = "request, get, Agent, maxHeaderSize"
		defaultExports = "{ request, get, Agent, maxHeaderSize }"
	}
	s = strings.ReplaceAll(s, "__NODERATI_MAX_HEADER_SIZE__", maxHeaderSizeDecl)
	s = strings.ReplaceAll(s, "__NODERATI_EXPORTS__", "export { "+exportsList+" };\nexport default "+defaultExports+";")
	return s
}

var httpShim = renderHTTPShim("http", true)
var httpsShim = renderHTTPShim("https", false)
