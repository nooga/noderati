
// ---- exports -------------------------------------------------------------------------

const pseudoRandomBytes = randomBytes;
const prng = randomBytes;
const rng = randomBytes;

const cryptoModule = {
  // hashes
  createHash, createHmac, hash, getHashes, Hash, Hmac,
  // random
  randomBytes, randomFill, randomFillSync, randomInt, randomUUID, randomUUIDv7,
  getRandomValues, pseudoRandomBytes, prng, rng,
  // KDFs
  pbkdf2, pbkdf2Sync, scrypt, scryptSync, hkdf, hkdfSync, argon2, argon2Sync,
  // keys
  KeyObject, createSecretKey, createPublicKey, createPrivateKey,
  // misc
  timingSafeEqual, constants, getFips, setFips, setEngine, secureHeapUsed,
  webcrypto, subtle,
};

Object.defineProperty(cryptoModule, "fips", {
  get: getFips,
  set: setFips,
  enumerable: false,
  configurable: false,
});

export default cryptoModule;
export {
  createHash, createHmac, hash, getHashes, Hash, Hmac,
  randomBytes, randomFill, randomFillSync, randomInt, randomUUID, randomUUIDv7,
  getRandomValues, pseudoRandomBytes, prng, rng,
  pbkdf2, pbkdf2Sync, scrypt, scryptSync, hkdf, hkdfSync, argon2, argon2Sync,
  KeyObject, createSecretKey, createPublicKey, createPrivateKey,
  timingSafeEqual, constants, getFips, setFips, setEngine, secureHeapUsed,
  webcrypto, subtle,
};
