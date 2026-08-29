import yaml from "yaml";

const doc = yaml.parse("name: noderati\nversion: 1\n");
if (doc.name !== "noderati") {
  throw new Error(`expected name noderati, got ${doc.name}`);
}
console.log(doc.name);
console.log("ok yaml");
