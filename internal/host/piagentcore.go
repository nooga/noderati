package host

const piAgentCoreShim = `import { streamSimple } from "@earendil-works/pi-ai/compat";

var _uuidSeq = 0;
export function uuidv7() {
  _uuidSeq += 1;
  var n = Date.now().toString(16) + _uuidSeq.toString(16);
  while (n.length < 32) n += "0";
  return n.slice(0, 8) + "-" + n.slice(8, 12) + "-7" + n.slice(13, 16) + "-8" + n.slice(17, 20) + "-" + n.slice(20, 32);
}

export function Agent(options) {
  options = options || {};
  var initial = options.initialState || {};
  this.state = {
    systemPrompt: initial.systemPrompt || "",
    model: initial.model,
    thinkingLevel: initial.thinkingLevel || "off",
    tools: initial.tools ? initial.tools.slice() : [],
    messages: initial.messages ? initial.messages.slice() : [],
    isStreaming: false,
    streamingMessage: undefined,
    pendingToolCalls: new Set(),
    errorMessage: undefined,
  };
  this.listeners = new Set();
  this.convertToLlm = options.convertToLlm;
  this.transformContext = options.transformContext;
  this.streamFn = options.streamFn || streamSimple;
  this.getApiKey = options.getApiKey;
  this.onPayload = options.onPayload;
  this.onResponse = options.onResponse;
  this.beforeToolCall = options.beforeToolCall;
  this.afterToolCall = options.afterToolCall;
  this.prepareNextTurn = options.prepareNextTurn;
  this.sessionId = options.sessionId;
  this.thinkingBudgets = options.thinkingBudgets;
  this.transport = options.transport || "auto";
  this.maxRetryDelayMs = options.maxRetryDelayMs;
  this.toolExecution = options.toolExecution || "parallel";
  this.steeringMode = options.steeringMode || "one-at-a-time";
  this.followUpMode = options.followUpMode || "one-at-a-time";
}

Agent.prototype.subscribe = function (listener) {
  var listeners = this.listeners;
  listeners.add(listener);
  return function () { listeners.delete(listener); };
};
Agent.prototype.steer = function () {};
Agent.prototype.followUp = function () {};
Agent.prototype.clearSteeringQueue = function () {};
Agent.prototype.clearFollowUpQueue = function () {};
Agent.prototype.clearAllQueues = function () {};
Agent.prototype.hasQueuedMessages = function () { return false; };
Agent.prototype.abort = function () {};
Agent.prototype.waitForIdle = function () { return Promise.resolve(); };
Agent.prototype.reset = function () {
  this.state.messages = [];
  this.state.errorMessage = undefined;
};
Agent.prototype.continue = function () { return Promise.resolve(); };

Agent.prototype.prompt = function (input, images) {
  var self = this;
  var userMessages;
  if (Array.isArray(input)) userMessages = input;
  else if (typeof input !== "string") userMessages = [input];
  else {
    var content = [{ type: "text", text: input }];
    if (images && images.length) for (var i = 0; i < images.length; i++) content.push(images[i]);
    userMessages = [{ role: "user", content: content, timestamp: Date.now() }];
  }
  var context = {
    systemPrompt: self.state.systemPrompt,
    messages: self.state.messages.concat(userMessages),
    tools: self.state.tools,
  };
  var fn = self.streamFn || streamSimple;
  return Promise.resolve(fn(self.state.model, context, {})).then(function (stream) {
    if (!stream || typeof stream.result !== "function") {
      return stream;
    }
    return stream.result();
  }).then(function (msg) {
    self.state.messages = self.state.messages.concat(userMessages);
    if (msg) {
      self.state.messages.push(msg);
      if (msg.errorMessage) self.state.errorMessage = msg.errorMessage;
    }
    return msg;
  });
};

export default { uuidv7: uuidv7, Agent: Agent };
`

func declarePiAgentCore() {
	registerJSShim("@earendil-works/pi-agent-core", piAgentCoreShim)
}
