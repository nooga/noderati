import { parse, pathToFileURL, fileURLToPath, resolve } from "url";

const href = "https://example.com:8080/a/b?x=1#h";
const u = parse(href);
console.log("protocol:", u.protocol);
console.log("hostname:", u.hostname);
console.log("pathname:", u.pathname);
console.log("search:", u.search);
console.log("hash:", u.hash);
console.log("port:", u.port);
console.log("resolve:", resolve("https://example.com/a/", "b"));

const fileURL = pathToFileURL("/tmp/foo");
console.log("pathToFileURL:", fileURL);
console.log("fileURLToPath:", fileURLToPath(fileURL));
