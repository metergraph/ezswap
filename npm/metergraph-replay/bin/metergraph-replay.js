#!/usr/bin/env node
// Launcher: runs the native binary from the platform package npm installed
// alongside this one (an optional dependency picked by os and cpu). No
// install script, no download: npm only ever fetches packages it can verify.
"use strict";

const { spawnSync } = require("node:child_process");
const path = require("node:path");

const PLATFORMS = {
  "darwin arm64": "metergraph-replay-darwin-arm64",
  "darwin x64": "metergraph-replay-darwin-x64",
  "linux arm64": "metergraph-replay-linux-arm64",
  "linux x64": "metergraph-replay-linux-x64",
  "win32 arm64": "metergraph-replay-win32-arm64",
  "win32 x64": "metergraph-replay-win32-x64",
};

const key = `${process.platform} ${process.arch}`;
const pkg = PLATFORMS[key];
if (!pkg) {
  console.error(`metergraph-replay: no build for ${key}. Install with Go instead: go install github.com/metergraph/replay/cmd/metergraph-replay@latest`);
  process.exit(1);
}

let binary;
try {
  const exe = process.platform === "win32" ? "metergraph-replay.exe" : "metergraph-replay";
  binary = path.join(path.dirname(require.resolve(`${pkg}/package.json`)), "bin", exe);
} catch {
  console.error(`metergraph-replay: the ${pkg} package is missing. Reinstall without --no-optional (or --omit=optional).`);
  process.exit(1);
}

const result = spawnSync(binary, process.argv.slice(2), { stdio: "inherit" });
if (result.error) {
  console.error(`metergraph-replay: could not start ${binary}: ${result.error.message}`);
  process.exit(1);
}
if (result.signal) process.kill(process.pid, result.signal);
process.exit(result.status ?? 1);
