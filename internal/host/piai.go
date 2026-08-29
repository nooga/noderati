package host

import (
	"os"
	"path/filepath"
)

// findPiCodingAgentNodeModulesRoots returns install roots whose nested
// node_modules contains @earendil-works/pi-ai (global pi / pi-coding-agent).
func findPiCodingAgentNodeModulesRoots() []string {
	candidates := []string{
		"/opt/homebrew/lib/node_modules/@earendil-works/pi-coding-agent",
		"/usr/local/lib/node_modules/@earendil-works/pi-coding-agent",
	}
	var roots []string
	for _, root := range candidates {
		piAiIndex := filepath.Join(root, "node_modules", "@earendil-works", "pi-ai", "dist", "index.js")
		if _, err := os.Stat(piAiIndex); err == nil {
			roots = append(roots, root)
		}
	}
	return roots
}

const piAiShim = `export function modelsAreEqual(a, b) {
  if (a === b) return true;
  if (!a || !b) return false;
  return a.id === b.id && a.provider === b.provider;
}
export default { modelsAreEqual };
`

const piAiCompatShim = `function envVal(name, env) {
  const src = env || (typeof process !== "undefined" ? process.env : {});
  const v = src && src[name];
  return (typeof v === "string" && v.trim()) ? v.trim() : undefined;
}

const ENV_KEYS = {
  anthropic: ["ANTHROPIC_OAUTH_TOKEN", "ANTHROPIC_API_KEY"],
  openai: ["OPENAI_API_KEY"],
  google: ["GEMINI_API_KEY", "GOOGLE_API_KEY"],
  groq: ["GROQ_API_KEY"],
  openrouter: ["OPENROUTER_API_KEY"],
  deepseek: ["DEEPSEEK_API_KEY"],
};

export function findEnvKeys(provider, env) {
  const names = ENV_KEYS[provider];
  if (!names) return undefined;
  const found = names.filter((n) => envVal(n, env));
  return found.length ? found : undefined;
}

export function getEnvApiKey(provider, env) {
  const keys = findEnvKeys(provider, env);
  return keys ? envVal(keys[0], env) : undefined;
}

function model(id, name, api, provider, baseUrl) {
  return {
    id: id, name: name, api: api, provider: provider, baseUrl: baseUrl,
    reasoning: false, input: ["text"],
    cost: { input: 0, output: 0, cacheRead: 0, cacheWrite: 0 },
    contextWindow: 128000, maxTokens: 8192,
  };
}

const CATALOG = {
  anthropic: [model("claude-sonnet-4-5", "Claude Sonnet 4.5", "anthropic-messages", "anthropic", "https://api.anthropic.com")],
  openai: [model("gpt-4.1-mini", "GPT-4.1 mini", "openai-completions", "openai", "https://api.openai.com/v1")],
  google: [model("gemini-2.0-flash", "Gemini 2.0 Flash", "google-generative-ai", "google", "https://generativelanguage.googleapis.com")],
};

export function getProviders() { return Object.keys(CATALOG); }
export function getModels(provider) {
  if (provider) return CATALOG[provider] ? CATALOG[provider].slice() : [];
  var all = [];
  for (var k of Object.keys(CATALOG)) all = all.concat(CATALOG[k]);
  return all;
}
export function getModel(provider, id) {
  var list = CATALOG[provider] || [];
  for (var i = 0; i < list.length; i++) if (list[i].id === id) return list[i];
}
export function modelsAreEqual(a, b) {
  if (a === b) return true;
  if (!a || !b) return false;
  return a.id === b.id && a.provider === b.provider;
}
export function clampThinkingLevel(level) { return level || "off"; }
export function getSupportedThinkingLevels() { return ["off", "minimal", "low", "medium", "high"]; }
export function isContextOverflow() { return false; }
export function cleanupSessionResources() {}
export function registerApiProvider() {}
export function resetApiProviders() {}
export function setBedrockProviderModule() {}
export function validateToolArguments() { return { valid: true, errors: [] }; }

export function EventStream(isComplete, extractResult) {
  this.queue = [];
  this.waiting = [];
  this.done = false;
  this.isComplete = isComplete;
  this.extractResult = extractResult;
  var self = this;
  this.finalResultPromise = new Promise(function (resolve) { self._resolveFinal = resolve; });
}
EventStream.prototype.push = function (event) {
  if (this.done) return;
  if (this.isComplete && this.isComplete(event)) {
    this.done = true;
    if (this._resolveFinal) this._resolveFinal(this.extractResult ? this.extractResult(event) : event);
  }
  var waiter = this.waiting.shift();
  if (waiter) waiter({ value: event, done: false });
  else this.queue.push(event);
  if (this.done) this._flushEnd();
};
EventStream.prototype.end = function (result) {
  this.done = true;
  if (result !== undefined && this._resolveFinal) this._resolveFinal(result);
  this._flushEnd();
};
EventStream.prototype._flushEnd = function () {
  while (this.waiting.length) {
    var w = this.waiting.shift();
    w({ value: undefined, done: true });
  }
};
EventStream.prototype.result = function () { return this.finalResultPromise; };
EventStream.prototype[Symbol.asyncIterator] = function () {
  var self = this;
  return {
    next: function () {
      if (self.queue.length) return Promise.resolve({ value: self.queue.shift(), done: false });
      if (self.done) return Promise.resolve({ value: undefined, done: true });
      return new Promise(function (resolve) { self.waiting.push(resolve); });
    },
  };
};

export function AssistantMessageEventStream() {
  EventStream.call(this, function (event) {
    return event && (event.type === "done" || event.type === "error");
  }, function (event) {
    if (event.type === "done") return event.message;
    return event.error;
  });
}
AssistantMessageEventStream.prototype = Object.create(EventStream.prototype);
AssistantMessageEventStream.prototype.constructor = AssistantMessageEventStream;
export function createAssistantMessageEventStream() { return new AssistantMessageEventStream(); }

function llmMessages(context) {
  var out = [];
  if (context && context.systemPrompt) out.push({ role: "system", content: context.systemPrompt });
  var msgs = (context && context.messages) || [];
  for (var i = 0; i < msgs.length; i++) {
    var m = msgs[i];
    var role = m.role === "assistant" ? "assistant" : "user";
    var text = "";
    if (typeof m.content === "string") text = m.content;
    else if (Array.isArray(m.content)) {
      for (var j = 0; j < m.content.length; j++) {
        if (m.content[j] && m.content[j].type === "text") text += m.content[j].text;
      }
    }
    out.push({ role: role, content: text });
  }
  return out;
}

function assistantMessage(text, stopReason, errorMessage) {
  return {
    role: "assistant",
    content: [{ type: "text", text: text || "" }],
    api: "unknown",
    provider: "unknown",
    model: "unknown",
    usage: { input: 0, output: 0, cacheRead: 0, cacheWrite: 0, totalTokens: 0, cost: { input: 0, output: 0, cacheRead: 0, cacheWrite: 0, total: 0 } },
    stopReason: stopReason || "stop",
    timestamp: Date.now(),
    errorMessage: errorMessage,
  };
}

function fail(msg) { return Promise.reject(new Error(msg)); }

function completeOnce(model, context, options) {
  var apiKey = (options && options.apiKey) || getEnvApiKey(model && model.provider);
  if (!apiKey) return fail("No API key for " + ((model && model.provider) || "unknown"));
  var msgs = llmMessages(context);
  var api = (model && model.api) || "";
  if (api.indexOf("openai") === 0 || (model && model.provider) === "openai") {
    var base = (model.baseUrl || "https://api.openai.com/v1").replace(/\/$/, "");
    var url = /chat\/completions$/.test(base) ? base
      : /\/v1$/.test(base) ? base + "/chat/completions"
      : base + "/v1/chat/completions";
    return fetch(url, {
      method: "POST",
      headers: { "content-type": "application/json", authorization: "Bearer " + apiKey },
      body: JSON.stringify({ model: model.id, messages: msgs }),
    }).then(function (res) {
      return res.text().then(function (text) {
        var json = {};
        try { json = text ? JSON.parse(text) : {}; } catch (e) { json = { error: { message: text.slice(0, 300) } }; }
        if (!res.ok) throw new Error((json && json.error && (json.error.message || json.error)) || text.slice(0, 300) || ("HTTP " + res.status));
        var choice = json.choices && json.choices[0];
        var content = choice && choice.message && choice.message.content;
        if (typeof content !== "string") throw new Error("unexpected completions response");
        return content;
      });
    });
  }
  if (api.indexOf("anthropic") === 0 || (model && model.provider) === "anthropic") {
    var sys = "";
    var am = [];
    for (var i = 0; i < msgs.length; i++) {
      if (msgs[i].role === "system") sys += msgs[i].content;
      else am.push({ role: msgs[i].role, content: msgs[i].content });
    }
    return fetch(((model.baseUrl || "https://api.anthropic.com").replace(/\/$/, "")) + "/v1/messages", {
      method: "POST",
      headers: {
        "content-type": "application/json",
        "x-api-key": apiKey,
        "anthropic-version": "2023-06-01",
      },
      body: JSON.stringify({ model: model.id, max_tokens: model.maxTokens || 1024, system: sys || undefined, messages: am }),
    }).then(function (ares) {
      return ares.json().then(function (aj) {
        if (!ares.ok) throw new Error((aj && aj.error && aj.error.message) || ("HTTP " + ares.status));
        var text = "";
        var blocks = aj.content || [];
        for (var b = 0; b < blocks.length; b++) if (blocks[b].type === "text") text += blocks[b].text;
        return text;
      });
    });
  }
  if (api.indexOf("google") === 0 || (model && model.provider) === "google") {
    var gurl = ((model.baseUrl || "https://generativelanguage.googleapis.com").replace(/\/$/, "")) +
      "/v1beta/models/" + model.id + ":generateContent?key=" + encodeURIComponent(apiKey);
    var contents = [];
    for (var k = 0; k < msgs.length; k++) {
      if (msgs[k].role === "system") continue;
      contents.push({
        role: msgs[k].role === "assistant" ? "model" : "user",
        parts: [{ text: msgs[k].content }],
      });
    }
    return fetch(gurl, {
      method: "POST",
      headers: { "content-type": "application/json" },
      body: JSON.stringify({ contents: contents }),
    }).then(function (gres) {
      return gres.json().then(function (gj) {
        if (!gres.ok) throw new Error((gj && gj.error && gj.error.message) || ("HTTP " + gres.status));
        var gtext = "";
        var cands = (gj.candidates && gj.candidates[0] && gj.candidates[0].content && gj.candidates[0].content.parts) || [];
        for (var p = 0; p < cands.length; p++) if (cands[p].text) gtext += cands[p].text;
        return gtext;
      });
    });
  }
  return fail("Unsupported api " + api);
}

export function streamSimple(model, context, options) {
  var stream = new AssistantMessageEventStream();
  completeOnce(model, context, options).then(function (text) {
    var msg = assistantMessage(text, "stop");
    msg.api = model.api;
    msg.provider = model.provider;
    msg.model = model.id;
    stream.push({ type: "start", partial: msg });
    stream.push({ type: "text_delta", delta: text, partial: msg });
    stream.push({ type: "done", message: msg });
  }, function (e) {
    var err = assistantMessage("", "error", e && e.message ? e.message : String(e));
    stream.push({ type: "error", error: err });
  });
  return stream;
}

export function completeSimple(model, context, options) {
  return streamSimple(model, context, options).result();
}

export default {
  getProviders, getModels, getModel, getEnvApiKey, findEnvKeys, streamSimple, completeSimple,
  modelsAreEqual, clampThinkingLevel, EventStream, AssistantMessageEventStream,
};
`

const piAiOauthShim = `export function registerOAuthProvider() {}
export function resetOAuthProviders() {}
export function getOAuthApiKey() { return undefined; }
export function getOAuthProvider() { return undefined; }
export function getOAuthProviders() { return []; }
export default {
  registerOAuthProvider,
  resetOAuthProviders,
  getOAuthApiKey,
  getOAuthProvider,
  getOAuthProviders,
};
`

func declarePiAi() {
	registerJSShim("@earendil-works/pi-ai", piAiShim)
	registerJSShim("@earendil-works/pi-ai/compat", piAiCompatShim)
	registerJSShim("@earendil-works/pi-ai/oauth", piAiOauthShim)
}
