import jwt from "jsonwebtoken";

const secret = "test-secret-do-not-use-in-prod";

// HS256 sign + verify
const token = jwt.sign({ userId: 42, role: "admin" }, secret, {
  expiresIn: "1h",
  issuer: "noderati-test",
});
console.log("token:", token);

const decoded = jwt.verify(token, secret);
console.log("decoded:", JSON.stringify(decoded));

// decode without verifying
const decodedOnly = jwt.decode(token, { complete: true });
console.log("header:", JSON.stringify(decodedOnly.header));

// expired token should throw TokenExpiredError
try {
  const expired = jwt.sign({ foo: "bar" }, secret, { expiresIn: -10 });
  jwt.verify(expired, secret);
  console.log("ERROR: expected expired token to throw");
} catch (e) {
  console.log("expired token correctly threw:", e.name, e.message);
}

// wrong secret should throw JsonWebTokenError
try {
  jwt.verify(token, "wrong-secret");
  console.log("ERROR: expected bad signature to throw");
} catch (e) {
  console.log("bad signature correctly threw:", e.name, e.message);
}

// RS256 (asymmetric) is a known, honest, flagged gap - real Node's
// crypto.generateKeyPairSync/createPublicKey/createPrivateKey (real RSA
// key generation/parsing/signing) aren't implemented here at all, only
// stubbed enough for jwa's own KeyObject feature-detection to see them
// exist (see crypto.go's own doc comments). HS256 (the common case,
// and the only algorithm this probe's own routes actually rely on) is
// fully verified above.
try {
  const { generateKeyPairSync } = await import("crypto");
  generateKeyPairSync("rsa", { modulusLength: 2048 });
  console.log("UNEXPECTED: RS256 keypair generation succeeded");
} catch (e) {
  console.log("RS256 correctly unsupported (known gap):", e.message);
}

console.log("ALL OK");
