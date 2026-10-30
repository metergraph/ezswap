// Builds the npm packages from GoReleaser's binaries, for the release
// workflow: one package per platform holding its binary, and the launcher
// package that depends on all of them optionally.
//
//   node scripts/npm-packages.mjs <version> <goreleaser-dist> <out-dir>
//
// Publish the platform packages first, then npm/metergraph-replay.
import { chmodSync, copyFileSync, cpSync, mkdirSync, readdirSync, readFileSync, writeFileSync } from "node:fs";
import { join } from "node:path";
import { fileURLToPath } from "node:url";

const [version, dist, out] = process.argv.slice(2);
if (!version || !dist || !out) {
  console.error("usage: node scripts/npm-packages.mjs <version> <goreleaser-dist> <out-dir>");
  process.exit(2);
}
if (!/^\d+\.\d+\.\d+(-[0-9A-Za-z.-]+)?$/.test(version)) {
  console.error(`not a semver version: ${version}`);
  process.exit(2);
}

// npm platform names, and how GoReleaser names the same build directory.
const TARGETS = [
  { os: "darwin", cpu: "arm64", goos: "darwin", goarch: "arm64" },
  { os: "darwin", cpu: "x64", goos: "darwin", goarch: "amd64" },
  { os: "linux", cpu: "arm64", goos: "linux", goarch: "arm64" },
  { os: "linux", cpu: "x64", goos: "linux", goarch: "amd64" },
  { os: "win32", cpu: "arm64", goos: "windows", goarch: "arm64" },
  { os: "win32", cpu: "x64", goos: "windows", goarch: "amd64" },
];

const root = fileURLToPath(new URL("..", import.meta.url));
const launcher = JSON.parse(readFileSync(join(root, "npm/metergraph-replay/package.json"), "utf8"));
const expected = TARGETS.map((t) => `metergraph-replay-${t.os}-${t.cpu}`).sort().join(" ");
if (Object.keys(launcher.optionalDependencies).sort().join(" ") !== expected) {
  throw new Error(`npm/metergraph-replay/package.json optionalDependencies must be exactly: ${expected}`);
}
const dirs = readdirSync(dist, { withFileTypes: true }).filter((d) => d.isDirectory()).map((d) => d.name);

for (const t of TARGETS) {
  const name = `metergraph-replay-${t.os}-${t.cpu}`;
  const build = dirs.find((d) => d.startsWith("metergraph-replay_") && d.includes(`_${t.goos}_${t.goarch}`));
  if (!build) throw new Error(`no GoReleaser build for ${t.goos}/${t.goarch} in ${dist}`);
  const exe = t.os === "win32" ? "metergraph-replay.exe" : "metergraph-replay";
  const dir = join(out, name);
  mkdirSync(join(dir, "bin"), { recursive: true });
  copyFileSync(join(dist, build, exe), join(dir, "bin", exe));
  if (t.os !== "win32") chmodSync(join(dir, "bin", exe), 0o755);
  copyFileSync(join(root, "LICENSE"), join(dir, "LICENSE"));
  writeFileSync(join(dir, "package.json"), JSON.stringify({
    name,
    version,
    description: `The metergraph-replay binary for ${t.os} ${t.cpu}. Install metergraph-replay instead.`,
    os: [t.os],
    cpu: [t.cpu],
    files: ["bin", "LICENSE"],
    license: launcher.license,
    repository: launcher.repository,
  }, null, 2) + "\n");
}

const main = join(out, "metergraph-replay");
cpSync(join(root, "npm/metergraph-replay"), main, { recursive: true });
copyFileSync(join(root, "LICENSE"), join(main, "LICENSE"));
copyFileSync(join(root, "README.md"), join(main, "README.md"));
launcher.version = version;
for (const dep of Object.keys(launcher.optionalDependencies)) launcher.optionalDependencies[dep] = version;
writeFileSync(join(main, "package.json"), JSON.stringify(launcher, null, 2) + "\n");
console.log(`wrote ${TARGETS.length + 1} packages to ${out}`);
