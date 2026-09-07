package host

import "strings"

// httpModuleShimTemplate is shared by both node:http and node:https - the
// two differ only in which scheme request()/get() bind to (real Node's
// http module always speaks plain HTTP, https always TLS, regardless of
// what a caller's options happen to say), so scheme is baked in per module
// as a literal at shim-build time rather than read from options.
const httpModuleShimTemplate = `const Agent = globalThis.__noderatiHTTPAgentCtor;

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

export { request, get, Agent };
export default { request, get, Agent };
`

func renderHTTPShim(scheme string) string {
	return strings.ReplaceAll(httpModuleShimTemplate, "__NODERATI_SCHEME__", scheme)
}

var httpShim = renderHTTPShim("http")
var httpsShim = renderHTTPShim("https")
