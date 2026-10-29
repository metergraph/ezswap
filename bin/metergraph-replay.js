#!/usr/bin/env node
// Launcher: the published package runs the compiled CLI from dist/.
import { main } from "../dist/cli/main.js";

process.exitCode = await main(process.argv.slice(2));
