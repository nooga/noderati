import semver from "semver";

const v = semver.parse("1.2.3");
if (!v || v.major !== 1 || v.minor !== 2 || v.patch !== 3) {
  throw new Error("semver.parse failed");
}
if (!semver.gt("2.0.0", "1.9.9")) {
  throw new Error("semver.gt failed");
}
console.log(`${v.major}.${v.minor}.${v.patch}`);
console.log("ok semver");
