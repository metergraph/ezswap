// The command line: demo, estimate, run and report.
//
// Exit codes are stable (see EXIT and docs/cli.md). With --json, stdout is
// one JSON object and nothing else.

import { readFileSync } from "node:fs";
import { copyFile, mkdir, readFile } from "node:fs/promises";
import { isAbsolute, join, relative, resolve } from "node:path";
import { createInterface } from "node:readline/promises";
import { fileURLToPath, pathToFileURL } from "node:url";
import { parseArgs } from "node:util";
import { parseJsonPath } from "../core/paths.ts";
import { loadPricing, type CatalogPricing } from "../pricing/catalog.ts";
import {
  ProviderConfigError,
  createProvider,
  type FetchLike,
  type OpenAICompatibleProvider,
} from "../providers/index.ts";
import { type ExplorerResult, hostedLink, renderReport, runExplorer, type ReportOptions } from "../report/index.ts";
import type { ExplorerInput } from "../report/explore/run.ts";
import {
  DEFAULT_BUDGET_USD,
  DEFAULT_ENDPOINT,
  DEFAULT_RUN_CONCURRENCY,
  DEFAULT_SAMPLE,
  MAX_SAMPLE,
  MAX_TRACKED_FIELDS,
  PlanError,
  SpecError,
  executeRun,
  parseEffort,
  parseEndpoint,
  parseMaxTokens,
  parseModels,
  parseTemperature,
  planRun,
  writeRun,
  type EndpointSpec,
  type RunPlan,
} from "../run/index.ts";
import type { RunFile, TreatmentSettings } from "../types.ts";
import { explainEstimate, explainOutcome, usd } from "./explain.ts";

export const EXIT = {
  ok: 0,
  error: 1,
  usage: 2,
  noReplayableCases: 3,
  refused: 4,
  providerConfig: 5,
  stoppedEarly: 6,
} as const;

export interface CliIo {
  stdout: { write(text: string): unknown; isTTY?: boolean; columns?: number };
  stderr: { write(text: string): unknown; isTTY?: boolean };
  stdinIsTTY: boolean;
  /** Keys for the explorer; default process.stdin. */
  stdin?: ExplorerInput;
  /** Opens the interactive explorer; default runExplorer on the real terminal. Tests replace it. */
  explore?: (run: RunFile, options: ExploreOptions) => Promise<ExplorerResult["reason"]>;
  env: Record<string, string | undefined>;
  fetch?: FetchLike;
  /** Ask a yes or no question; default reads a line from stdin. */
  confirm?: (question: string) => Promise<boolean>;
  /** Stops new calls; default: the first Ctrl-C. */
  signal?: AbortSignal;
  /** Cancels calls in flight; default: a second Ctrl-C. */
  hardSignal?: AbortSignal;
  now?: () => Date;
}

export interface ExploreOptions extends ReportOptions {
  /** The run's path as shown in the status bar. */
  name: string;
  color: boolean;
  ascii: boolean;
  /** The tab to open on. */
  tab?: number;
}

class UsageError extends Error {
  constructor(message: string) {
    super(message);
    this.name = "UsageError";
  }
}

class CliError extends Error {
  readonly exitCode: number;
  readonly code: string;
  constructor(exitCode: number, code: string, message: string) {
    super(message);
    this.name = "CliError";
    this.exitCode = exitCode;
    this.code = code;
  }
}

function packageVersion(): string {
  try {
    const pkg = JSON.parse(readFileSync(new URL("../../package.json", import.meta.url), "utf8")) as { version?: string };
    return pkg.version ?? "0.0.0";
  } catch {
    return "0.0.0";
  }
}

const DEMO_DIR = new URL("../../demo/", import.meta.url);

export const HELP = `metergraph-replay: replay your production LLM calls on other models, and read
what changes against a noise floor (production's model replayed twice).

Usage
  metergraph-replay demo [--out DIR] [--explore]
  metergraph-replay estimate <traces> --models LIST [options]
  metergraph-replay run <traces> --models LIST [options]
  metergraph-replay report <run.json> [--no-interactive]

Commands
  demo       Print the report of bundled synthetic traces. No keys, no network.
  estimate   Count replayable cases and price the run. Makes no provider call.
  run        Estimate, confirm, replay, write run.json and print the report.
  report     Explore a run.json in the terminal: tabs for fields, the noise
             floor and every call. Prints the report when not interactive.

Models and endpoints
  --models LIST            Comma-separated models, as the catalog names them
                           (creator/model). Prefix an endpoint preset to send
                           one elsewhere: openai:gpt-5-mini. Per-model settings
                           follow an @: model@temperature=0.2,max_tokens=500
  --endpoint NAME|URL      Endpoint for models without a prefix (default
                           ${DEFAULT_ENDPOINT}). Presets: openai, openrouter, vercel,
                           litellm, ollama; or any OpenAI-compatible base URL.
  --floor-endpoint NAME|URL  Endpoint for the noise floor (default: --endpoint).
  --api-key-env NAME       Variable holding the key for a base URL endpoint.
  --temperature N          Temperature for every model (default: production's).
  --max-tokens N           Max tokens for every model (default: production's).
  --reasoning-effort E     none, minimal, low, medium, high or xhigh.

Keys are read from the environment: OPENAI_API_KEY, OPENROUTER_API_KEY,
AI_GATEWAY_API_KEY, LITELLM_API_KEY (with LITELLM_BASE_URL). Ollama needs none.

Sampling and statistics
  --sample N               Latest N replayable cases, one per trace (default
                           ${DEFAULT_SAMPLE}, max ${MAX_SAMPLE}).
  --track PATH             A JSON field to compare, such as $.category.
                           Repeat for more (max ${MAX_TRACKED_FIELDS}).
  --no-noise-floor         Skip the two replays of production's model.
  --labels                 Show within-noise labels in the report.
  --allow-no-search        Replay calls that searched the web, without search.
  --adapter NAME           Trace format: replay-jsonl, raw, langfuse, otel
                           (default: detected).

Spend
  --budget USD             Most the run may spend (default ${DEFAULT_BUDGET_USD.toFixed(2)}). Each call
                           reserves its maximum before it is sent; the run stops
                           when the next case does not fit.
  --monthly-calls N        Calls a month the workload makes, for the monthly cost
                           projection (default: estimated from the file's time span).
  --allow-unpriced         Run models the price catalog does not know.
  --yes                    Skip the spend confirmation (needed without a terminal).
  --concurrency N          Calls in flight at once (default ${DEFAULT_RUN_CONCURRENCY}).

Output
  --out DIR                Where run.json goes (default ./replay-out).
  --explore                demo: open the explorer instead of printing the report.
  --no-interactive         report: print the report instead of opening the explorer.
  --ascii                  Plain ASCII, no box drawing (default when stdout is not
                           a terminal or TERM is dumb).
  --color, --no-color      Colour on or off (default: on in a terminal, unless
                           NO_COLOR is set).
  --json                   Print one JSON object instead of text.
  --help, --version

Exit codes
  0  done
  1  any other error
  2  usage: a bad option, value or file
  3  no replayable cases in the sample
  4  refused: over the budget, unpriced models, or no confirmation
  5  endpoint configuration, such as a missing key
  6  interrupted, or stopped early (budget or a call above its reservation)

Nothing leaves this machine except the provider calls a run makes.
`;

const OPTIONS = {
  models: { type: "string" },
  endpoint: { type: "string" },
  "floor-endpoint": { type: "string" },
  "api-key-env": { type: "string" },
  sample: { type: "string" },
  track: { type: "string", multiple: true },
  temperature: { type: "string" },
  "max-tokens": { type: "string" },
  "reasoning-effort": { type: "string" },
  budget: { type: "string" },
  "monthly-calls": { type: "string" },
  "no-noise-floor": { type: "boolean" },
  labels: { type: "boolean" },
  "allow-no-search": { type: "boolean" },
  "allow-unpriced": { type: "boolean" },
  adapter: { type: "string" },
  out: { type: "string" },
  json: { type: "boolean" },
  explore: { type: "boolean" },
  "no-interactive": { type: "boolean" },
  ascii: { type: "boolean" },
  color: { type: "boolean" },
  "no-color": { type: "boolean" },
  yes: { type: "boolean", short: "y" },
  concurrency: { type: "string" },
  help: { type: "boolean", short: "h" },
  version: { type: "boolean", short: "v" },
} as const;

type Values = ReturnType<typeof parse>["values"];

function parse(argv: string[]) {
  return parseArgs({ args: argv, options: OPTIONS, allowPositionals: true, strict: true });
}

function defaultIo(): CliIo {
  return {
    stdout: process.stdout,
    stderr: process.stderr,
    stdinIsTTY: Boolean(process.stdin.isTTY),
    env: process.env,
  };
}

async function askOnStdin(question: string): Promise<boolean> {
  const rl = createInterface({ input: process.stdin, output: process.stderr });
  try {
    const answer = await rl.question(question);
    return /^y(es)?$/i.test(answer.trim());
  } finally {
    rl.close();
  }
}

function intOption(value: string | undefined, name: string, min: number, max: number, fallback: number): number {
  if (value === undefined) return fallback;
  if (!/^\d+$/.test(value.trim()) || Number(value) < min || Number(value) > max) {
    throw new UsageError(`--${name} must be a whole number from ${min} to ${max}.`);
  }
  return Number(value);
}

function budgetOption(value: string | undefined): number {
  if (value === undefined) return DEFAULT_BUDGET_USD;
  const text = value.trim().replace(/^\$/, "");
  const n = Number(text);
  if (!/^\d+(\.\d+)?$/.test(text) || !Number.isFinite(n)) {
    throw new UsageError(`--budget must be an amount in dollars, such as 2.00.`);
  }
  return n;
}

function trackedOption(values: string[] | undefined): string[] {
  const tracked = (values ?? []).flatMap((value) => value.split(",")).map((v) => v.trim()).filter(Boolean);
  if (tracked.length > MAX_TRACKED_FIELDS) throw new UsageError(`Track at most ${MAX_TRACKED_FIELDS} fields.`);
  for (const path of tracked) {
    try {
      parseJsonPath(path);
    } catch (error) {
      throw new UsageError(error instanceof Error ? error.message : String(error));
    }
  }
  return [...new Set(tracked)];
}

function reportOptions(values: Values, tracked: string[]): ReportOptions {
  const monthly = monthlyCallsOption(values["monthly-calls"]);
  return {
    ...(tracked.length ? { tracked } : {}),
    ...(values.labels ? { labels: true } : {}),
    ...(monthly !== null ? { monthlyCalls: monthly } : {}),
  };
}

function monthlyCallsOption(value: string | undefined): number | null {
  if (value === undefined) return null;
  const text = value.trim().replace(/[_,]/g, "");
  if (!/^\d+$/.test(text) || Number(text) < 1 || !Number.isSafeInteger(Number(text))) {
    throw new UsageError("--monthly-calls must be a whole number of calls, such as 250000.");
  }
  return Number(text);
}

function pricingHelpUrl(pricing: CatalogPricing): string {
  return `${pricing.source.repository}/blob/main/${pricing.source.path}`;
}

interface Setup {
  pricing: CatalogPricing;
  endpoints: Map<string, EndpointSpec>;
  plan: RunPlan;
  tracked: string[];
}

async function setup(values: Values, file: string, io: CliIo): Promise<Setup> {
  if (!values.models) throw new UsageError("Name the models to replay on with --models.");
  const tracked = trackedOption(values.track);
  const global: TreatmentSettings = {
    temperature: values.temperature !== undefined ? parseTemperature(values.temperature) : null,
    maxTokens: values["max-tokens"] !== undefined ? parseMaxTokens(values["max-tokens"]) : null,
    reasoningEffort: values["reasoning-effort"] !== undefined ? parseEffort(values["reasoning-effort"]) : null,
  };
  const defaultEndpoint = parseEndpoint(values.endpoint ?? DEFAULT_ENDPOINT);
  const floorEndpoint = values["floor-endpoint"] ? parseEndpoint(values["floor-endpoint"]) : defaultEndpoint;
  const models = parseModels(values.models, defaultEndpoint);
  const endpoints = new Map<string, EndpointSpec>();
  const noiseFloor = !values["no-noise-floor"];
  for (const spec of [...models.map((m) => m.endpoint), ...(noiseFloor ? [floorEndpoint] : [])]) {
    const known = endpoints.get(spec.key);
    if (known && known.baseUrl !== spec.baseUrl) {
      throw new UsageError(`Two endpoints share the host ${spec.key}; use one base URL per host.`);
    }
    endpoints.set(spec.key, spec);
  }
  const pricing = loadPricing(io.now ? { now: io.now } : {});
  const sample = intOption(values.sample, "sample", 1, MAX_SAMPLE, DEFAULT_SAMPLE);
  const plan = await planRun({
    file,
    ...(values.adapter ? { adapter: values.adapter } : {}),
    sample,
    treatments: models.map((m) => ({
      model: m.model,
      endpoint: m.endpoint.key,
      settings: {
        temperature: m.settings.temperature ?? global.temperature,
        maxTokens: m.settings.maxTokens ?? global.maxTokens,
        reasoningEffort: m.settings.reasoningEffort ?? global.reasoningEffort,
      },
    })),
    noiseFloor,
    floorEndpoint: floorEndpoint.key,
    trackedFields: tracked,
    budgetUsd: budgetOption(values.budget),
    allowNoSearch: Boolean(values["allow-no-search"]),
    pricing,
    isFreeEndpoint: (key) => endpoints.get(key)?.free ?? false,
    ...(io.now ? { at: io.now().toISOString() } : {}),
  });
  return { pricing, endpoints, plan, tracked };
}

function createProviders(
  endpoints: Map<string, EndpointSpec>,
  values: Values,
  io: CliIo,
): Record<string, OpenAICompatibleProvider> {
  const concurrency = intOption(values.concurrency, "concurrency", 1, 64, DEFAULT_RUN_CONCURRENCY);
  const providers: Record<string, OpenAICompatibleProvider> = {};
  for (const [key, spec] of endpoints) {
    providers[key] = createProvider({
      ...(spec.preset ? { preset: spec.preset } : {}),
      ...(spec.baseUrl ? { baseUrl: spec.baseUrl, apiKeyEnv: values["api-key-env"] ?? null } : {}),
      ...(io.fetch ? { fetch: io.fetch } : {}),
      env: io.env,
      free: spec.free,
      concurrency,
    });
  }
  return providers;
}

function configError(error: ProviderConfigError): CliError {
  const hint =
    error.code === "credential_missing"
      ? " Export it in your shell, or choose another endpoint with --endpoint."
      : error.code === "base_url_missing"
        ? " Export it, or pass a base URL with --endpoint."
        : "";
  return new CliError(EXIT.providerConfig, error.code, `${error.message}.${hint}`);
}

function missingKeys(endpoints: Map<string, EndpointSpec>, values: Values, io: CliIo): string[] {
  const notes: string[] = [];
  // Creating a provider only reads its configuration; it sends nothing.
  for (const [key, spec] of endpoints) {
    try {
      createProviders(new Map([[key, spec]]), values, io);
    } catch (error) {
      if (error instanceof ProviderConfigError) notes.push(`${error.message}; run will need it.`);
      else throw error;
    }
  }
  return notes;
}

// ---------------------------------------------------------------------------
// Commands

interface Outcome {
  exitCode: number;
  json: Record<string, unknown>;
}

async function estimateCommand(values: Values, file: string, io: CliIo, say: (text: string) => void): Promise<Outcome> {
  const { pricing, plan, endpoints } = await setup(values, file, io);
  say(explainEstimate(plan, { pricingHelpUrl: pricingHelpUrl(pricing), allowUnpriced: Boolean(values["allow-unpriced"]) }));
  const notes = missingKeys(endpoints, values, io);
  for (const note of notes) say(`Note: ${note}`);
  const exitCode = plan.estimate.replayable === 0 ? EXIT.noReplayableCases : EXIT.ok;
  if (exitCode === EXIT.noReplayableCases) say("No case in the sample can be replayed, so there is nothing to run.");
  return {
    exitCode,
    json: {
      command: "estimate",
      input: plan.input,
      file: plan.file,
      sampled: plan.cases.length,
      treatments: plan.treatments,
      noiseFloor: plan.settings.noiseFloor,
      budgetUsd: plan.settings.budgetUsd,
      estimate: plan.estimate,
      overBudget: plan.estimate.expectedUsd > plan.settings.budgetUsd,
      notes,
    },
  };
}

function installInterrupt(io: CliIo, say: (text: string) => void): { signal: AbortSignal; hard: AbortSignal; dispose: () => void } {
  if (io.signal) {
    return { signal: io.signal, hard: io.hardSignal ?? new AbortController().signal, dispose: () => {} };
  }
  const soft = new AbortController();
  const hard = new AbortController();
  const onSigint = () => {
    if (!soft.signal.aborted) {
      soft.abort();
      say("\nStopping: finishing the calls in flight, then writing what exists. Press Ctrl-C again to quit now.");
    } else {
      hard.abort();
    }
  };
  process.on("SIGINT", onSigint);
  return { signal: soft.signal, hard: hard.signal, dispose: () => process.off("SIGINT", onSigint) };
}

async function runCommand(values: Values, file: string, io: CliIo, say: (text: string) => void): Promise<Outcome> {
  const { pricing, plan, endpoints, tracked } = await setup(values, file, io);
  const help = pricingHelpUrl(pricing);
  say(explainEstimate(plan, { pricingHelpUrl: help, allowUnpriced: Boolean(values["allow-unpriced"]) }));
  const base = { command: "run", input: plan.input, estimate: plan.estimate };

  if (plan.estimate.replayable === 0) {
    throw new CliError(EXIT.noReplayableCases, "no_replayable_cases", "No case in the sample can be replayed.");
  }
  if (plan.estimate.unpriced.length > 0 && !values["allow-unpriced"]) {
    throw new CliError(
      EXIT.refused,
      "unpriced_models",
      `The catalog has no price for ${plan.estimate.unpriced.join(", ")}, so the run cannot be budgeted. ` +
        `Pass --allow-unpriced to run anyway, or add the price: ${help}`,
    );
  }
  if (plan.estimate.expectedUsd > plan.settings.budgetUsd) {
    throw new CliError(
      EXIT.refused,
      "over_budget",
      `The expected spend (${usd(plan.estimate.expectedUsd)}) is over the budget (${usd(plan.settings.budgetUsd)}). ` +
        `Raise --budget or lower --sample.`,
    );
  }

  let providers: Record<string, OpenAICompatibleProvider>;
  try {
    providers = createProviders(endpoints, values, io);
  } catch (error) {
    if (error instanceof ProviderConfigError) throw configError(error);
    throw error;
  }

  const ceiling = Math.min(plan.estimate.upperBoundUsd, plan.settings.budgetUsd);
  if (!values.yes) {
    if (!io.stdinIsTTY) {
      throw new CliError(
        EXIT.refused,
        "confirmation_required",
        "Refusing to spend without a confirmation. Pass --yes to run without a terminal.",
      );
    }
    const confirm = io.confirm ?? askOnStdin;
    const yes = await confirm(`Spend up to ${usd(ceiling)} (about ${usd(plan.estimate.expectedUsd)} expected)? [y/N] `);
    if (!yes) throw new CliError(EXIT.refused, "not_confirmed", "Nothing was sent.");
  }

  const interrupt = installInterrupt(io, say);
  const showProgress = Boolean(io.stderr.isTTY) && !values.json;
  let run: RunFile;
  try {
    run = await executeRun(plan, {
      providers,
      pricing,
      signal: interrupt.signal,
      hardSignal: interrupt.hard,
      concurrency: intOption(values.concurrency, "concurrency", 1, 64, DEFAULT_RUN_CONCURRENCY),
      toolVersion: packageVersion(),
      ...(io.now ? { now: io.now } : {}),
      ...(showProgress
        ? {
            onProgress: (p) => {
              io.stderr.write(`\r  ${p.done}/${p.total} calls, spent ${usd(p.spentUsd)}   `);
            },
          }
        : {}),
    });
  } finally {
    interrupt.dispose();
    if (showProgress) io.stderr.write("\n");
  }

  const monthly = monthlyCallsOption(values["monthly-calls"]);
  if (monthly !== null) run.settings.monthlyCalls = monthly;
  const out = values.out ?? "replay-out";
  const written = await writeRun(out, run);
  const link = hostedLink(run, "cli");
  say("");
  say(explainOutcome(run));
  say(`Wrote ${written.runPath}`);
  say("");
  say(renderReport(run, { ...termOptions(values, io), ...reportOptions(values, tracked), explore: shownPath(written.runPath) }).trimEnd());

  const counts = { ok: 0, failed: 0, skipped: 0 };
  for (const replay of run.replays) counts[replay.status]++;
  let exitCode: number = run.stopped ? EXIT.stoppedEarly : EXIT.ok;
  if (!run.stopped && counts.ok === 0 && counts.failed > 0) exitCode = EXIT.error;
  return {
    exitCode,
    json: {
      ...base,
      runFile: written.runPath,
      stopped: run.stopped,
      spentUsd: run.spentUsd,
      budgetUsd: run.settings.budgetUsd,
      replays: counts,
      hostedLink: link,
    },
  };
}

/** Width, colour and character set for the terminal report. */
function termOptions(values: Values, io: CliIo): { width: number; color: boolean; ascii: boolean } {
  const tty = Boolean(io.stdout.isTTY);
  const dumb = io.env["TERM"] === "dumb";
  const color = values["no-color"] ? false : values.color ? true : tty && !dumb && !io.env["NO_COLOR"];
  const ascii = Boolean(values.ascii) || !tty || dumb;
  const columns = io.stdout.columns || Number(io.env["COLUMNS"]) || 100;
  return { width: columns, color, ascii };
}

/** A path as the user would type it: relative to here when it is below here. */
function shownPath(path: string): string {
  const rel = relative(process.cwd(), path);
  return rel && !rel.startsWith("..") && !isAbsolute(rel) ? rel : path;
}

async function defaultExplore(run: RunFile, options: ExploreOptions, io: CliIo): Promise<ExplorerResult["reason"]> {
  const stdin = io.stdin ?? (process.stdin as unknown as ExplorerInput);
  const result = await runExplorer(
    run,
    { stdin, stdout: io.stdout as never, color: options.color, ascii: options.ascii },
    { ...options, name: options.name },
  );
  return result.reason;
}

/** Open the explorer; the exit code says how it ended. */
async function explore(run: RunFile, options: ExploreOptions, io: CliIo): Promise<number> {
  const reason = io.explore ? await io.explore(run, options) : await defaultExplore(run, options, io);
  return reason === "SIGINT" ? 130 : reason === "SIGTERM" ? 143 : EXIT.ok;
}

function interactive(io: CliIo): boolean {
  return io.stdinIsTTY && Boolean(io.stdout.isTTY) && io.env["TERM"] !== "dumb";
}

async function readRunFile(path: string): Promise<RunFile> {
  let text: string;
  try {
    text = await readFile(path, "utf8");
  } catch {
    throw new UsageError(`Could not read ${path}.`);
  }
  let run: RunFile;
  try {
    run = JSON.parse(text) as RunFile;
  } catch {
    throw new UsageError(`${path} is not JSON.`);
  }
  if (run?.schema !== "metergraph.replay-run/v1") {
    throw new UsageError(`${path} is not a metergraph-replay run file.`);
  }
  return run;
}

async function reportCommand(values: Values, file: string, io: CliIo, say: (text: string) => void, asJson: boolean): Promise<Outcome> {
  if (values.out !== undefined) throw new UsageError("report draws in the terminal and writes no file, so it takes no --out.");
  const run = await readRunFile(file);
  const tracked = trackedOption(values.track);
  const term = termOptions(values, io);
  const json = { command: "report", runFile: resolve(file) };
  if (!asJson && !values["no-interactive"] && interactive(io)) {
    const exitCode = await explore(run, { ...reportOptions(values, tracked), name: shownPath(resolve(file)), color: term.color, ascii: term.ascii }, io);
    return { exitCode, json };
  }
  say(renderReport(run, { ...term, ...reportOptions(values, tracked), explore: shownPath(resolve(file)) }).trimEnd());
  return { exitCode: EXIT.ok, json };
}

async function demoCommand(values: Values, io: CliIo, say: (text: string) => void, asJson: boolean): Promise<Outcome> {
  const source = fileURLToPath(new URL("run.json", DEMO_DIR));
  const run = await readRunFile(source);
  const tracked = trackedOption(values.track);
  // Check every option before anything is written.
  monthlyCallsOption(values["monthly-calls"]);
  const out = resolve(values.out ?? "replay-out");
  await mkdir(out, { recursive: true });
  const runPath = join(out, "run.json");
  await copyFile(source, runPath);
  const json = { command: "demo", runFile: runPath, hostedLink: hostedLink(run, "cli") };
  const term = termOptions(values, io);
  if (values.explore && !asJson && interactive(io)) {
    const exitCode = await explore(run, { ...reportOptions(values, tracked), name: shownPath(runPath), color: term.color, ascii: term.ascii }, io);
    return { exitCode, json };
  }
  say("A replay of synthetic support-ticket traces, recorded so it needs no keys and no network.");
  say("Try it on your own traces:");
  say("  metergraph-replay estimate traces.jsonl --models openai/gpt-5-mini,anthropic/claude-haiku-4.5");
  say("");
  say(renderReport(run, { ...term, ...reportOptions(values, tracked), explore: shownPath(runPath) }).trimEnd());
  return { exitCode: EXIT.ok, json };
}

// ---------------------------------------------------------------------------

export async function main(argv: string[], ioOverrides: Partial<CliIo> = {}): Promise<number> {
  const io: CliIo = { ...defaultIo(), ...ioOverrides };
  let values: Values | null = null;
  let command: string | undefined;
  const asJson = argv.includes("--json");
  const say = (text: string) => {
    if (!asJson) io.stdout.write(`${text}\n`);
  };
  const emit = (exitCode: number, json: Record<string, unknown>) => {
    if (asJson) io.stdout.write(`${JSON.stringify({ ok: exitCode === EXIT.ok, exitCode, ...json })}\n`);
    return exitCode;
  };

  try {
    let positionals: string[];
    try {
      ({ values, positionals } = parse(argv));
    } catch (error) {
      throw new UsageError(error instanceof Error ? error.message : String(error));
    }
    if (values.version) {
      const version = packageVersion();
      say(version);
      return emit(EXIT.ok, { command: "version", version });
    }
    [command] = positionals;
    if (values.help || command === undefined || command === "help") {
      if (!asJson) io.stdout.write(HELP);
      return emit(command === undefined && !values.help ? EXIT.usage : EXIT.ok, { command: "help" });
    }
    const args = positionals.slice(1);
    const needFile = (what: string) => {
      if (args.length !== 1) throw new UsageError(`${command} takes one ${what}.`);
      return args[0]!;
    };
    let outcome: Outcome;
    switch (command) {
      case "demo":
        if (args.length) throw new UsageError("demo takes no file.");
        outcome = await demoCommand(values, io, say, asJson);
        break;
      case "estimate":
        outcome = await estimateCommand(values, needFile("trace file"), io, say);
        break;
      case "run":
        outcome = await runCommand(values, needFile("trace file"), io, say);
        break;
      case "report":
        outcome = await reportCommand(values, needFile("run.json"), io, say, asJson);
        break;
      default:
        throw new UsageError(`Unknown command "${command}". Run metergraph-replay --help.`);
    }
    return emit(outcome.exitCode, outcome.json);
  } catch (error) {
    let exitCode: number = EXIT.error;
    let code = "error";
    let message = "Something went wrong.";
    if (error instanceof CliError) {
      ({ exitCode, code, message } = error);
    } else if (error instanceof UsageError || error instanceof SpecError || error instanceof PlanError) {
      exitCode = EXIT.usage;
      code = "usage";
      message = error.message;
    } else if (error instanceof ProviderConfigError) {
      ({ exitCode, code, message } = configError(error));
    } else if ((error as { code?: unknown })?.code === "ENOENT") {
      exitCode = EXIT.usage;
      code = "file_not_found";
      message = `No such file: ${(error as { path?: string }).path ?? "the input"}.`;
    } else if (error instanceof Error) {
      message = error.message;
    }
    if (!asJson) io.stderr.write(`metergraph-replay: ${message}\n${exitCode === EXIT.usage ? "Run metergraph-replay --help for usage.\n" : ""}`);
    return emit(exitCode, { command: command ?? null, error: { code, message } });
  }
}

// Run directly in development: node --experimental-strip-types src/cli/main.ts
if (process.argv[1] && import.meta.url === pathToFileURL(resolve(process.argv[1])).href) {
  process.exitCode = await main(process.argv.slice(2));
}
