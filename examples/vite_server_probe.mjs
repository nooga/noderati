import { createServer } from "vite";

console.log("creating vite server...");
const server = await createServer({
  server: { port: 5199 },
  logLevel: "info",
});
console.log("server created, listening...");
await server.listen();
console.log("listening!");
const addr = server.httpServer.address();
console.log("address:", addr);
await server.close();
console.log("closed");
