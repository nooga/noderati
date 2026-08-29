package host

const undiciShim = `let globalDispatcher = null;

class EnvHttpProxyAgent {
  constructor(opts) {
    this.opts = opts;
  }
}

function setGlobalDispatcher(dispatcher) {
  globalDispatcher = dispatcher;
}

function install() {
  const current = globalThis.fetch;
  if (typeof current === "function") {
    globalThis.fetch = current;
  }
}

export { setGlobalDispatcher, EnvHttpProxyAgent, install };
export default { setGlobalDispatcher, EnvHttpProxyAgent, install };
`

func declareUndici() {
	registerJSShim("undici", undiciShim)
}
