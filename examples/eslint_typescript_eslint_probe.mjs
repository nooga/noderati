// Real, unmodified eslint@9.36.0 + typescript-eslint@8 + @eslint/js@9,
// linting a real .ts file through typescript-eslint's recommended config.
//
// Known blocker (2026-09-21): tseslint.configs.recommended's getter calls
// typescript-eslint's own getTSConfigRootDirFromStack(), which sets
// Error.prepareStackTrace and expects Error.captureStackTrace to hand back
// structured CallSite objects (the V8 "Stack Trace API" -
// https://v8.dev/docs/stack-trace-api). paserati doesn't implement
// Error.prepareStackTrace / Error.stackTraceLimit / CallSite at all, so
// this currently throws "TypeError: undefined is not a function" at
// getTSConfigRootDirFromStack:31. Filed as
// https://github.com/nooga/paserati/issues/492.
import { ESLint } from "eslint";
import tseslint from "typescript-eslint";

const eslint = new ESLint({
  cwd: process.cwd(),
  overrideConfigFile: true,
  overrideConfig: tseslint.config(
    ...tseslint.configs.recommended,
    {
      rules: {
        "no-unused-vars": "off",
        "@typescript-eslint/no-unused-vars": "warn",
      },
    },
  ),
});

const results = await eslint.lintFiles(["eslint-ts-sample.ts"]);
for (const r of results) {
  console.log("file:", r.filePath);
  for (const m of r.messages) {
    console.log(`  ${m.line}:${m.column} ${m.severity === 2 ? "error" : "warn"} ${m.message} (${m.ruleId})`);
  }
}
console.log("errorCount:", results.reduce((a, r) => a + r.errorCount, 0));
console.log("warningCount:", results.reduce((a, r) => a + r.warningCount, 0));
