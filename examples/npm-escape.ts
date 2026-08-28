import escapeStringRegexp from "escape-string-regexp";

const raw = "How much $ for a (file).js?";
const escaped = escapeStringRegexp(raw);
console.log("raw:", raw);
console.log("escaped:", escaped);
console.log("safe in regexp:", "How much $ for a (file).js?".replace(new RegExp(escaped), "MATCHED"));
