import { startVitest } from "vitest/node";

try {
  console.log("calling startVitest...");
  const vitest = await startVitest("test", ["vitest-sample.test.js"], {
    watch: false,
    run: true,
  });
  console.log("startVitest returned:", !!vitest);
  await vitest?.close();
  console.log("done");
} catch (e) {
  console.log("CAUGHT ERROR:", e && e.stack || e);
}
