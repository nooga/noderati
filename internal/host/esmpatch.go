package host

import (
	"path/filepath"
	"strings"
)

// patchModuleSource applies small transforms for known third-party ESM quirks.
func patchModuleSource(source, filename string) string {
	source = patchESMKeybindingsAlias(source, filename)
	source = patchESMSyntaxHighlightStub(source, filename)
	source = patchESMThemeTypeboxImport(source, filename)
	source = patchESMSdkReexports(source, filename)
	source = patchESMExtensionLoaderStub(source, filename)
	source = patchESMPiAgentCoreReexports(source, filename)
	source = patchESMPiAiIndexReexports(source, filename)
	source = patchESMPiAiCompatReexports(source, filename)
	source = patchESMPiAiOauthReexports(source, filename)
	source = patchESMPiAiOauthIndexReexports(source, filename)
	source = patchESMPiAiSyntaxCompat(source, filename)
	source = patchESMPiAiAuthContext(source, filename)
	return source
}

// patchESMPiAiIndexReexports keeps only modelsAreEqual from pi-ai index — the
// only symbol pi-coding-agent imports from the main entrypoint.
func patchESMPiAiIndexReexports(source, filename string) string {
	if filepath.Base(filename) != "index.js" {
		return source
	}
	if !strings.Contains(filename, filepath.Join("@earendil-works", "pi-ai", "dist")) {
		return source
	}
	return `export { Type } from "typebox";
export { modelsAreEqual } from "./models.js";
`
}

// patchESMPiAiCompatReexports replaces export * blocks with named re-exports for
// symbols pi-coding-agent imports from @earendil-works/pi-ai/compat.
func patchESMPiAiCompatReexports(source, filename string) string {
	if filepath.Base(filename) != "compat.js" {
		return source
	}
	if !strings.Contains(filename, filepath.Join("@earendil-works", "pi-ai", "dist")) {
		return source
	}
	const exportStar = `export * from `
	for {
		idx := strings.Index(source, exportStar)
		if idx < 0 {
			break
		}
		end := strings.Index(source[idx:], "\n")
		if end < 0 {
			source = source[:idx]
			break
		}
		source = source[:idx] + source[idx+end+1:]
	}
	named := `export { setBedrockProviderModule } from "./api/bedrock-converse-stream.lazy.js";
export { findEnvKeys, getEnvApiKey } from "./env-api-keys.js";
export { modelsAreEqual, clampThinkingLevel, getSupportedThinkingLevels } from "./models.js";
export { isContextOverflow } from "./utils/overflow.js";
export { cleanupSessionResources } from "./session-resources.js";
export { EventStream, AssistantMessageEventStream, createAssistantMessageEventStream } from "./utils/event-stream.js";
export { validateToolArguments } from "./utils/validation.js";
export { getBuiltinModel as getModel, getBuiltinModels as getModels, getBuiltinProviders as getProviders } from "./providers/all.js";
`
	if imp := strings.Index(source, "import {"); imp >= 0 {
		source = source[:imp] + named + source[imp:]
	} else {
		source = named + source
	}
	// Paserati static imports only bind symbols from early export declarations;
	// re-export stream helpers defined later in compat.js.
	if !strings.Contains(source, "export { streamSimple") {
		source += `
export { streamSimple, stream, complete, completeSimple, registerApiProvider, resetApiProviders, registerFauxProvider, registerBuiltInApiProviders };
`
	}
	return source
}

// patchESMPiAiOauthReexports replaces oauth.js — provider modules use syntax
// and Node APIs Paserati does not support yet; print mode only needs registry stubs.
func patchESMPiAiOauthReexports(source, filename string) string {
	if filepath.Base(filename) != "oauth.js" {
		return source
	}
	if !strings.Contains(filename, filepath.Join("@earendil-works", "pi-ai", "dist")) {
		return source
	}
	return `const BUILT_IN_OAUTH_PROVIDERS = [
  { id: "anthropic", name: "Anthropic (Claude Pro/Max)", usesCallbackServer: true },
  { id: "github-copilot", name: "GitHub Copilot", usesCallbackServer: false },
  { id: "openai-codex", name: "OpenAI Codex (ChatGPT)", usesCallbackServer: false },
];
const oauthProviderRegistry = new Map(BUILT_IN_OAUTH_PROVIDERS.map((provider) => [provider.id, provider]));
export function registerOAuthProvider(provider) { oauthProviderRegistry.set(provider.id, provider); }
export function resetOAuthProviders() {
  oauthProviderRegistry.clear();
  for (const provider of BUILT_IN_OAUTH_PROVIDERS) {
    oauthProviderRegistry.set(provider.id, provider);
  }
}
export function getOAuthApiKey() { return undefined; }
export function getOAuthProvider(id) { return oauthProviderRegistry.get(id); }
export function getOAuthProviders() { return Array.from(oauthProviderRegistry.values()); }
`
}

// patchESMPiAiOauthIndexReexports removes export * from utils/oauth/index.js.
func patchESMPiAiOauthIndexReexports(source, filename string) string {
	if filepath.Base(filename) != "index.js" {
		return source
	}
	if !strings.Contains(filename, filepath.Join("@earendil-works", "pi-ai", "dist", "utils", "oauth")) {
		return source
	}
	source = strings.Replace(source, `export * from "./device-code.js";
`, `export { pollOAuthDeviceCodeFlow } from "./device-code.js";
`, 1)
	source = strings.Replace(source, `export * from "./types.js";
`, "", 1)
	return source
}

// patchESMPiAiSyntaxCompat fixes pi-ai dist syntax Paserati does not parse yet.
func patchESMPiAiSyntaxCompat(source, filename string) string {
	if !strings.Contains(filename, filepath.Join("@earendil-works", "pi-ai", "dist")) {
		return source
	}
	return strings.ReplaceAll(source, "catch {", "catch (_e) {")
}

// patchESMPiAiAuthContext replaces auth/context.js — Paserati misparses
// `await importNodeModule` (import keyword) and optional catch binding.
func patchESMPiAiAuthContext(source, filename string) string {
	if filepath.Base(filename) != "context.js" {
		return source
	}
	if !strings.Contains(filename, filepath.Join("@earendil-works", "pi-ai", "dist", "auth")) {
		return source
	}
	return `import { access } from "node:fs/promises";
import { homedir } from "node:os";

function getProcessEnv() {
  const proc = globalThis.process;
  return proc ? proc.env : undefined;
}

export function defaultProviderAuthContext() {
  return {
    async env(name) {
      const envObj = getProcessEnv();
      const value = envObj ? envObj[name] : undefined;
      return typeof value === "string" && value.trim().length > 0 ? value : undefined;
    },
    async fileExists(path) {
      try {
        let resolved = path;
        if (resolved.startsWith("~")) {
          resolved = homedir() + resolved.slice(1);
        }
        await access(resolved);
        return true;
      } catch (_e) {
        return false;
      }
    },
  };
}
`
}

// patchESMPiAgentCoreReexports replaces pi-agent-core index.js with named
// re-exports — Paserati skip-typecheck cannot harvest class names from export *.
func patchESMPiAgentCoreReexports(source, filename string) string {
	if filepath.Base(filename) != "index.js" {
		return source
	}
	if !strings.Contains(filename, filepath.Join("@earendil-works", "pi-agent-core", "dist")) {
		return source
	}
	return `export { uuidv7 } from "./harness/session/uuid.js";
export { Agent } from "./agent.js";
`
}

// patchESMSdkReexports removes re-export blocks that Paserati cannot compile.
func patchESMSdkReexports(source, filename string) string {
	if filepath.Base(filename) != "sdk.js" {
		return source
	}
	// Drop duplicate tool re-exports; imports at top already bind for local use.
	source = strings.Replace(source, `export { withFileMutationQueue, 
// Tool factories (for custom cwd)
createCodingTools, createReadOnlyTools, createReadTool, createBashTool, createEditTool, createWriteTool, createGrepTool, createFindTool, createLsTool, };`, "", 1)
	return source
}

// patchESMKeybindingsAlias rewrites pi-coding-agent keybindings.js to import
// TuiKeybindingsManager directly — Paserati does not bind import aliases for
// class extends.
func patchESMKeybindingsAlias(source, filename string) string {
	if filepath.Base(filename) != "keybindings.js" {
		return source
	}
	return strings.Replace(source,
		"KeybindingsManager as TuiKeybindingsManager",
		"TuiKeybindingsManager",
		1)
}

const syntaxHighlightStub = `export function highlight(code, _lang) { return code; }
export function supportsLanguage(_lang) { return false; }
`

// patchESMSyntaxHighlightStub replaces highlight.js-backed syntax highlighting
// with a no-op stub — the real module exceeds Paserati compiler limits.
func patchESMSyntaxHighlightStub(source, filename string) string {
	if filepath.Base(filename) != "syntax-highlight.js" {
		return source
	}
	return syntaxHighlightStub
}

// patchESMThemeTypeboxImport replaces pi theme.js with a stub — the real module
// depends on typebox re-exports that Paserati does not bind reliably.
func patchESMThemeTypeboxImport(source, filename string) string {
	if !strings.Contains(filename, "modes/interactive/theme/theme.js") {
		return source
	}
	return themeStub
}

const themeStub = `function noopStr(s) { return String(s); }

export class Theme {
  constructor() { this.name = "dark"; }
  fg(_k, s) { return String(s); }
  bg(_k, s) { return String(s); }
}

const themeObj = {
  fg: (_k, s) => String(s),
  bg: (_k, s) => String(s),
  bold: noopStr,
  italic: noopStr,
  underline: noopStr,
  inverse: noopStr,
  strikethrough: noopStr,
  getBashModeBorderColor() { return noopStr; },
  getThinkingBorderColor() { return noopStr; },
};

export const theme = themeObj;

export function initTheme() {}
export function stopThemeWatcher() {}
export function setRegisteredThemes() {}
export function setTheme() {}
export function setThemeInstance(t) { if (t) Object.assign(themeObj, t); }
export function onThemeChange() {}
export function detectTerminalBackgroundFromEnv() { return { theme: "dark" }; }
export async function detectTerminalBackgroundTheme() { return { theme: "dark" }; }
export async function detectTerminalThemeForAuto() { return "dark"; }
export function getDefaultTheme() { return "dark"; }
export function parseAutoThemeSetting(s) { return s; }
export function resolveThemeSetting(s) { return s; }
export function loadThemeFromPath() { return new Theme(); }
export function getAvailableThemes() { return ["dark", "light"]; }
export function getAvailableThemesWithPaths() { return []; }
export function getThemeByName() { return new Theme(); }
export function getThemeForRgbColor() { return "dark"; }
export function getResolvedThemeColors() { return {}; }
export function isLightTheme() { return false; }
export function getThemeExportColors() { return {}; }
export function highlightCode(code) { return code; }
export function getLanguageFromPath() { return undefined; }
export function getMarkdownTheme() { return {}; }
export function getSelectListTheme() { return {}; }
export function getEditorTheme() { return {}; }
export function getSettingsListTheme() { return {}; }
`

const extensionLoaderStub = `function notInitialized() {
  throw new Error("Extension runtime not initialized.");
}
export function createExtensionRuntime() {
  const runtime = {
    sendMessage: notInitialized,
    sendUserMessage: notInitialized,
    appendEntry: notInitialized,
    setSessionName: notInitialized,
    getSessionName: notInitialized,
    setLabel: notInitialized,
    getActiveTools: notInitialized,
    getAllTools: notInitialized,
    setActiveTools: notInitialized,
    refreshTools: function() {},
    getCommands: notInitialized,
    setModel: function() { return Promise.reject(new Error("Extension runtime not initialized")); },
    getThinkingLevel: notInitialized,
    setThinkingLevel: notInitialized,
    flagValues: new Map(),
    pendingProviderRegistrations: [],
    assertActive: function() {},
    invalidate: function() {},
    registerProvider: function(name, config, extensionPath) {
      runtime.pendingProviderRegistrations.push({ name: name, config: config, extensionPath: extensionPath || "<unknown>" });
    },
    unregisterProvider: function(name) {
      runtime.pendingProviderRegistrations = runtime.pendingProviderRegistrations.filter(function(r) { return r.name !== name; });
    },
  };
  return runtime;
}
function emptyExtensions(runtime) {
  return { extensions: [], errors: [], runtime: runtime || createExtensionRuntime() };
}
export function clearExtensionCache() {}
export async function loadExtensionFromFactory() { return emptyExtensions(); }
export async function loadExtensions() { return emptyExtensions(); }
export async function loadExtensionsCached() { return emptyExtensions(); }
export async function discoverAndLoadExtensions() { return emptyExtensions(); }
`

// patchESMExtensionLoaderStub replaces jiti-backed extension loader with no-op stubs.
func patchESMExtensionLoaderStub(source, filename string) string {
	if !strings.Contains(filename, "core/extensions/loader.js") {
		return source
	}
	return extensionLoaderStub
}
