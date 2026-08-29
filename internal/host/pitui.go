package host

const piTuiShim = `export function TUI() {}
TUI.prototype.start = function() {};

export function KeybindingsManager(defs, user) { this.defs = defs; this.user = user || {}; }
KeybindingsManager.create = function() { return new KeybindingsManager({}, ""); };
KeybindingsManager.prototype.setUserBindings = function() {};
KeybindingsManager.prototype.getResolvedBindings = function() { return {}; };
export const TuiKeybindingsManager = KeybindingsManager;

export function ProcessTerminal() {}

export const TUI_KEYBINDINGS = {};

let _keybindings = KeybindingsManager.create();
export function setKeybindings(k) { _keybindings = k; }
export function getKeybindings() { return _keybindings; }

export function Container() {}
export function Box() {}
export function Text() {}
export function Markdown() {}
export function Image() {}
export function Input() {}
export function Loader() {}
export function Spacer() {}
export function SelectList() {}
export function SettingsList() {}
export function TruncatedText() {}
export function CombinedAutocompleteProvider() {}
export function Editor() {}
export function CancellableLoader() {}
export function Key() {}

export function visibleWidth(s) { return String(s).length; }
export function truncateToWidth(s) { return s; }
export function sliceByColumn(s) { return s; }
export function wrapTextWithAnsi(s) { return s; }
export function fuzzyFilter(items) { return items || []; }
export function fuzzyMatch() { return false; }
export function matchesKey() { return false; }
export function getCapabilities() { return {}; }
export function hyperlink(s) { return s; }
export function imageFallback() { return ""; }
export function getImageDimensions() { return { width: 0, height: 0 }; }
export function isFocusable() { return false; }

export default {
  TUI,
  KeybindingsManager,
  TuiKeybindingsManager,
  ProcessTerminal,
  TUI_KEYBINDINGS,
  setKeybindings,
  getKeybindings,
  Container,
  Box,
  Text,
  Markdown,
  Image,
  Input,
  Loader,
  Spacer,
  SelectList,
  SettingsList,
  TruncatedText,
  CombinedAutocompleteProvider,
  Editor,
  CancellableLoader,
  Key,
  visibleWidth,
  truncateToWidth,
  sliceByColumn,
  wrapTextWithAnsi,
  fuzzyFilter,
  fuzzyMatch,
  matchesKey,
  getCapabilities,
  hyperlink,
  imageFallback,
  getImageDimensions,
  isFocusable,
};
`

func declarePiTui() {
	registerJSShim("@earendil-works/pi-tui", piTuiShim)
}
