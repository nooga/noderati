
// ---- Web Crypto (placeholder) --------------------------------------------------

class CryptoKey {}

function webcryptoImportKeyObject() {
  throw makeError(Error, "ERR_NODERATI_NOT_IMPLEMENTED", "KeyObject.toCryptoKey is not implemented in noderati");
}

const webcrypto = { getRandomValues, randomUUID };
const subtle = undefined;
