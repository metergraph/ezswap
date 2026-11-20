#!/usr/bin/env node
// Launcher: runs the native binary from the platform package npm installed
// alongside this one (an optional dependency picked by os and cpu). No
// install script, no download: npm only ever fetches packages it can verify.
"use strict";

const { spawnSync } = require("node:child_process");
const path = require("node:path");

const PLATFORMS = {
  "darwin arm64": "ezswap-darwin-arm64",
  "darwin x64": "ezswap-darwin-x64",
  "linux arm64": "ezswap-linux-arm64",
  "linux x64": "ezswap-linux-x64",
  "win32 arm64": "ezswap-win32-arm64",
  "win32 x64": "ezswap-win32-x64",
};

const key = `${process.platform} ${process.arch}`;
const pkg = PLATFORMS[key];
if (!pkg) {
  console.error(`ezswap: no build for ${key}. Install with Go instead: go install github.com/metergraph/ezswap/cmd/ezswap@latest`);
  process.exit(1);
}

let binary;
try {
  const exe = process.platform === "win32" ? "ezswap.exe" : "ezswap";
  binary = path.join(path.dirname(require.resolve(`${pkg}/package.json`)), "bin", exe);
} catch {
  console.error(`ezswap: the ${pkg} package is missing. Reinstall without --no-optional (or --omit=optional).`);
  process.exit(1);
}

const result = spawnSync(binary, process.argv.slice(2), { stdio: "inherit" });
if (result.error) {
  console.error(`ezswap: could not start ${binary}: ${result.error.message}`);
  process.exit(1);
}
if (result.signal) process.kill(process.pid, result.signal);
process.exit(result.status ?? 1);
