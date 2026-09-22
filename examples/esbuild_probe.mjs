// Real, unmodified esbuild@0.28.2: build() spawns esbuild's real native
// binary via child_process and talks to it over a length-prefixed
// stdin/stdout protocol - a genuinely different code path than pure-JS
// bundlers like webpack (already passing), stressing child_process
// stdio plumbing rather than the JS module-resolution/compile pipeline.
import * as esbuild from "esbuild";

const result = await esbuild.build({
  entryPoints: ["esbuild-fixture.js"],
  bundle: true,
  write: false,
  format: "esm",
  minify: false,
});

for (const file of result.outputFiles) {
  console.log("=== " + file.path + " ===");
  console.log(file.text);
}

const result2 = await esbuild.transform("const x   =    1 + 2;\nconsole.log(x)", {
  minify: true,
});
console.log("=== transform (minified) ===");
console.log(result2.code);

process.exit(0);
