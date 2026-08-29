package host

const stringDecoderShim = `export function StringDecoder() {}
StringDecoder.prototype.write = function(c) { return String(c); };
StringDecoder.prototype.end = function() { return ""; };
export default { StringDecoder };
`

func declareStringDecoder() {
	registerJSShim("string_decoder", stringDecoderShim)
}
