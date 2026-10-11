#!/usr/bin/env node
import { existsSync } from "node:fs";
import { spawn, spawnSync } from "node:child_process";
import { fileURLToPath } from "node:url";
import { join } from "node:path";

const root = fileURLToPath(new URL("../", import.meta.url));
const binary = join(root, "bin", "phantomprompt");
if (existsSync(join(root, "go.mod"))) {
  const build = spawnSync("go", ["build", "-trimpath", "-o", binary, "./cmd/phantomprompt"], { cwd: root, stdio: "inherit" });
  if (build.error || build.status !== 0) {
    process.stderr.write("Build failed. Source installations require Go; packaged macOS releases do not.\n");
    process.exit(1);
  }
}
if (!existsSync(binary)) {
  process.stderr.write("PhantomPrompt binary is missing. Use a complete release or the source repository.\n");
  process.exit(1);
}
const args = process.argv.slice(2);
const child = spawn(binary, args.length ? args : ["web"], { cwd: root, stdio: "inherit" });
for (const signal of ["SIGINT", "SIGTERM"]) process.on(signal, () => child.kill(signal));
child.on("error", () => { process.stderr.write("Could not start PhantomPrompt. Check the binary platform and permissions.\n"); process.exitCode = 1; });
child.on("exit", (code, signal) => { process.exitCode = code ?? (signal === "SIGINT" ? 0 : 1); });
