// Build the bundled demo: synthetic support-ticket traces (demo/traces.jsonl)
// and a recorded run over them (demo/run.json), made with a deterministic
// fake provider so `metergraph-replay demo` needs no keys and no network.
//
//   npm run build:demo
//
// Every ticket, name and address is invented; addresses use example.com.
// The fake models differ the way real ones tend to: the cheaper model
// relabels a few tickets, the reasoning model writes more tokens and cuts one
// reply off at the token limit, and production's model replayed twice
// disagrees with itself on a couple of tickets (the noise floor).

import { writeFile } from "node:fs/promises";
import { fileURLToPath } from "node:url";
import { loadPricing } from "../src/pricing/catalog.ts";
import { executeRun, planRun } from "../src/run/index.ts";
import type { Provider, ReplayCall, ReplyOutcome } from "../src/types.ts";

const DEMO = new URL("../demo/", import.meta.url);
const TRACES = fileURLToPath(new URL("traces.jsonl", DEMO));
const RUN = fileURLToPath(new URL("run.json", DEMO));
const NOW = new Date("2026-10-28T16:00:00.000Z");

const SYSTEM = [
  "You triage support tickets for Example Cloud, a hosting product at example.com.",
  "Read the ticket and reply with one JSON object and nothing else:",
  '{"category": "billing" | "bug" | "account" | "feature_request" | "outage",',
  ' "priority": "low" | "medium" | "high" | "urgent",',
  ' "product": the product area named or implied (dashboard, api, billing, dns, storage, auth, email),',
  ' "customer_email": the sender\'s address or null,',
  ' "summary": one sentence of at most 20 words}',
].join("\n");

interface Ticket {
  subject: string;
  from: string | null;
  body: string;
  category: string;
  priority: string;
  product: string;
  summary: string;
}

const TICKETS: Ticket[] = [
  { subject: "Charged twice for October", from: "dana.reyes@example.com", body: "Hi, my card statement shows two charges of $49 for the October plan. Can you refund the duplicate?", category: "billing", priority: "medium", product: "billing", summary: "Customer was charged twice for the October plan and wants the duplicate refunded." },
  { subject: "API returns 500 on /deployments", from: "ops@example.com", body: "Since this morning every POST to /v2/deployments returns HTTP 500. Our CI pipeline is blocked.", category: "bug", priority: "high", product: "api", summary: "POST requests to the deployments endpoint fail with HTTP 500, blocking the customer's CI." },
  { subject: "Can't log in after password reset", from: "li.wei@example.com", body: "I reset my password but the login page keeps saying the link expired. I have tried three times.", category: "account", priority: "medium", product: "auth", summary: "Password reset links show as expired, so the customer cannot log in." },
  { subject: "Dark mode for the dashboard?", from: "sam.okafor@example.com", body: "Would love a dark theme in the dashboard. Staring at it late at night is rough.", category: "feature_request", priority: "low", product: "dashboard", summary: "Customer asks for a dark theme in the dashboard." },
  { subject: "Sites down in eu-west", from: "noc@example.com", body: "All of our sites in eu-west are timing out. Status page says operational but nothing loads.", category: "outage", priority: "urgent", product: "api", summary: "Every site in eu-west is timing out although the status page shows operational." },
  { subject: "Invoice needs our VAT number", from: "accounts@example.com", body: "Please reissue invoice INV-2207 with our VAT number EX123456 on it. Our auditors need it.", category: "billing", priority: "low", product: "billing", summary: "Customer needs invoice INV-2207 reissued with their VAT number." },
  { subject: "DNS changes not propagating", from: "maria.k@example.com", body: "I updated the A record for shop.example.com six hours ago and it still resolves to the old IP.", category: "bug", priority: "medium", product: "dns", summary: "An A record change made six hours ago has not taken effect." },
  { subject: "Add a teammate without a seat?", from: "jo@example.com", body: "Is there a way to give our accountant read-only access to invoices without paying for another seat?", category: "account", priority: "low", product: "billing", summary: "Customer wants read-only invoice access for an accountant without buying a seat." },
  { subject: "Uploads fail over 2 GB", from: "media-team@example.com", body: "Uploading files larger than 2 GB to storage fails at 99 percent with a network error. Smaller files are fine.", category: "bug", priority: "medium", product: "storage", summary: "Storage uploads over 2 GB fail near completion with a network error." },
  { subject: "Webhooks for failed payments", from: "dev@example.com", body: "We would like a webhook when a customer's payment fails so we can pause their account automatically.", category: "feature_request", priority: "low", product: "api", summary: "Customer requests a webhook for failed payments." },
  { subject: "Account locked after 2FA phone change", from: "priya.n@example.com", body: "I got a new phone and lost my authenticator. Now I am locked out and our launch is tomorrow.", category: "account", priority: "high", product: "auth", summary: "Customer lost their authenticator with a phone change and is locked out before a launch." },
  { subject: "Outbound email delayed", from: "support@example.com", body: "Transactional emails from our app are arriving 30 to 40 minutes late since yesterday afternoon.", category: "bug", priority: "high", product: "email", summary: "Transactional emails have been delayed by 30 to 40 minutes since yesterday." },
  { subject: "Downgrade to the starter plan", from: "hello@example.com", body: "We are a smaller team now. How do I move from Pro to Starter at the end of this billing cycle?", category: "billing", priority: "low", product: "billing", summary: "Customer wants to downgrade from Pro to Starter at the end of the cycle." },
  { subject: "Dashboard graphs blank in Safari", from: "tom.b@example.com", body: "The usage graphs on the dashboard render empty in Safari 19. Chrome shows them fine.", category: "bug", priority: "low", product: "dashboard", summary: "Dashboard usage graphs render blank in Safari but work in Chrome." },
  { subject: "Whole API unreachable", from: "sre@example.com", body: "api.example.com is refusing connections from every region we test. This is a full outage for us.", category: "outage", priority: "urgent", product: "api", summary: "The API refuses connections from every tested region, a full outage for the customer." },
  { subject: "Unexpected storage overage charge", from: "finance@example.com", body: "We were billed $310 in storage overage but our usage graph never went above the plan limit.", category: "billing", priority: "medium", product: "storage", summary: "Customer disputes a $310 storage overage that their usage graph does not show." },
  { subject: "SSO with our identity provider", from: "it-admin@example.com", body: "Do you support SAML single sign-on? We need it before we can roll out to the whole company.", category: "feature_request", priority: "medium", product: "auth", summary: "Customer asks whether SAML single sign-on is supported before a company rollout." },
  { subject: "Rate limit hit at low volume", from: "backend@example.com", body: "We get 429 responses at around 20 requests a minute, well under the documented limit of 600.", category: "bug", priority: "high", product: "api", summary: "Customer gets rate limited at 20 requests a minute, far below the documented limit." },
  { subject: "Change the account owner", from: "founder@example.com", body: "Our previous CTO owns the account and has left. Please transfer ownership to me.", category: "account", priority: "medium", product: "auth", summary: "Customer asks to transfer account ownership from a departed CTO." },
  { subject: "Export invoices as CSV", from: null, body: "It would save us hours each month if we could export all invoices as one CSV file.", category: "feature_request", priority: "low", product: "billing", summary: "Customer asks for a CSV export of all invoices." },
];

// --- deterministic noise ----------------------------------------------------

function hash(text: string): number {
  let h = 2166136261;
  for (let i = 0; i < text.length; i++) h = Math.imul(h ^ text.charCodeAt(i), 16777619);
  return (h >>> 0) / 4294967296;
}

function between(seed: string, low: number, high: number): number {
  return Math.round(low + hash(seed) * (high - low));
}

function ticketText(t: Ticket): string {
  return `Subject: ${t.subject}\nFrom: ${t.from ?? "(no address)"}\n\n${t.body}`;
}

function answer(t: Ticket, changes: Partial<Ticket> = {}): string {
  const v = { ...t, ...changes };
  return JSON.stringify({
    category: v.category,
    priority: v.priority,
    product: v.product,
    customer_email: v.from,
    summary: v.summary,
  });
}

function inputTokens(t: Ticket): number {
  return Math.ceil((SYSTEM.length + ticketText(t).length) / 4) + 12;
}

function outputTokens(text: string): number {
  return Math.ceil(text.length / 4);
}

// --- the traces -------------------------------------------------------------

async function writeTraces(): Promise<void> {
  const pricing = loadPricing({ now: () => NOW });
  const lines: string[] = [];
  const start = Date.parse("2026-10-27T08:00:00Z");
  TICKETS.forEach((t, i) => {
    const n = String(i + 1).padStart(4, "0");
    const ts = new Date(start + i * 37 * 60_000).toISOString();
    const text = answer(t);
    const usage = { input_tokens: inputTokens(t), output_tokens: outputTokens(text), cached_input_tokens: 0 };
    const cost = pricing.price({
      model: "openai/gpt-4.1-mini",
      inputTokens: usage.input_tokens,
      outputTokens: usage.output_tokens,
      cachedInputTokens: 0,
      inputIncludesCacheRead: true,
    });
    lines.push(
      JSON.stringify({
        schema: "metergraph.replay-trace/v1",
        id: `call-${n}`,
        trace_id: `ticket-${n}`,
        provider: "openai",
        request: {
          model: "gpt-4.1-mini",
          system: SYSTEM,
          messages: [{ role: "user", parts: [{ type: "text", content: ticketText(t) }] }],
          response_format: { type: "json_object" },
          params: { temperature: 0.2, max_tokens: 300 },
        },
        response: {
          text,
          finish_reason: "stop",
          usage,
          cost_usd: cost,
          latency_ms: between(`prod-${i}`, 690, 1340),
        },
        meta: { source: "example-helpdesk", ts, truncated: false, missing: [] },
      }),
    );
  });
  // Two calls the demo cannot replay, so the report shows why.
  const late = (k: number) => new Date(start + (TICKETS.length + k) * 37 * 60_000).toISOString();
  lines.push(
    JSON.stringify({
      schema: "metergraph.replay-trace/v1",
      id: "call-0021",
      trace_id: "ticket-0021",
      provider: "openai",
      request: {
        model: "gpt-4.1-mini",
        system: SYSTEM,
        messages: [{ role: "user", parts: [{ type: "text", content: "Subject: Refund for annual plan\nFrom: ari@example.com\n\nWe paid for the annual plan in" }] }],
        response_format: { type: "json_object" },
        params: { temperature: 0.2, max_tokens: 300 },
      },
      response: { text: answer({ ...TICKETS[0]!, from: "ari@example.com", summary: "Customer asks about a refund for the annual plan." }), finish_reason: "stop" },
      meta: { source: "example-helpdesk", ts: late(0), truncated: true, missing: [] },
    }),
  );
  lines.push(
    JSON.stringify({
      schema: "metergraph.replay-trace/v1",
      id: "call-0022",
      trace_id: "ticket-0022",
      provider: "openai",
      request: null,
      response: { text: answer(TICKETS[6]!), finish_reason: "stop" },
      meta: { source: "example-helpdesk", ts: late(1), truncated: false, missing: ["request"] },
    }),
  );
  await writeFile(TRACES, `${lines.join("\n")}\n`, "utf8");
}

// --- the fake models --------------------------------------------------------

function ticketOf(call: ReplayCall): number {
  const last = call.messages[call.messages.length - 1]?.content ?? "";
  const index = TICKETS.findIndex((t) => ticketText(t) === last);
  if (index < 0) throw new Error("unknown demo ticket");
  return index;
}

type Behaviour = (i: number, t: Ticket, call: ReplayCall) => { text: string; finish?: string; reasoning?: number; latency: number };

// A cheaper, faster model: terser summaries, and a few tickets labelled differently.
const flashLite: Behaviour = (i, t) => {
  const changes: Partial<Ticket> = { summary: t.summary.replace(/^Customer (asks|wants|requests) /, "Asks ").replace(/, (blocking|so|a full) .*\.$/, ".") };
  if (i === 3) changes.category = "feature_request";
  if (i === 6) changes.priority = "high";
  if (i === 11) changes.category = "outage";
  if (i === 15) changes.category = "bug";
  if (i === 17) changes.priority = "urgent";
  if (i === 10) changes.priority = "urgent";
  if (i === 18) changes.category = "account";
  return { text: answer(t, changes), latency: between(`lite-${i}`, 340, 560) };
};

// A small reasoning model: close to production, slower, more output tokens,
// and one reply cut off at the token limit.
const nano: Behaviour = (i, t, call) => {
  const changes: Partial<Ticket> = {};
  if (i === 8) changes.priority = "high";
  if (i === 16) changes.priority = "low";
  const reasoning = between(`nano-r-${i}`, 120, 230);
  if (i === 13) {
    const full = answer(t, changes);
    return { text: full.slice(0, Math.floor(full.length * 0.55)), finish: "length", reasoning: call.maxTokens - outputTokens(full.slice(0, Math.floor(full.length * 0.55))), latency: between(`nano-${i}`, 2600, 3100) };
  }
  return { text: answer(t, changes), reasoning, latency: between(`nano-${i}`, 1500, 2300) };
};

// Production's model again: mostly the same answer, with the small
// self-disagreement that sets the noise floor.
const floor = (replay: 1 | 2): Behaviour => (i, t) => {
  const changes: Partial<Ticket> = {};
  if (replay === 1 && i === 6) changes.priority = "high";
  if (replay === 2 && i === 12) changes.priority = "medium";
  if (replay === 2 && i === 17) changes.priority = "urgent";
  if (replay === 2 && i === 4) changes.summary = "Sites in eu-west time out while the status page reports everything operational.";
  return { text: answer(t, changes), latency: between(`floor${replay}-${i}`, 650, 1300) };
};

function fakeProvider(name: string, pick: (call: ReplayCall) => Behaviour): Provider {
  return {
    name,
    async complete(call) {
      const i = ticketOf(call);
      const t = TICKETS[i]!;
      const out = pick(call)(i, t, call);
      const visible = outputTokens(out.text);
      const reasoning = out.reasoning ?? 0;
      const input = Math.round(inputTokens(t) * (0.96 + hash(`${call.model}-${i}`) * 0.08));
      const outcome: ReplyOutcome = {
        status: "ok",
        output: out.text,
        finishReason: out.finish ?? "stop",
        refusal: false,
        usage: { inputTokens: input, outputTokens: visible + reasoning, cachedInputTokens: 0, reasoningTokens: reasoning || null },
        reportedCostUsd: null,
        latencyMs: out.latency,
        flags: [],
        error: null,
      };
      return outcome;
    },
  };
}

async function writeRun(): Promise<void> {
  const pricing = loadPricing({ now: () => NOW });
  const plan = await planRun({
    file: TRACES,
    sample: 20,
    treatments: [
      { model: "google/gemini-2.5-flash-lite", endpoint: "openrouter" },
      { model: "openai/gpt-5.4-nano", endpoint: "openrouter" },
    ],
    noiseFloor: true,
    floorEndpoint: "openrouter",
    trackedFields: ["$.category", "$.priority"],
    budgetUsd: 1,
    pricing,
    at: NOW.toISOString(),
  });
  // The floor's two replays alternate on one endpoint; tell them apart by call order per ticket.
  const floorSeen = new Map<number, number>();
  const provider = fakeProvider("openrouter", (call) => {
    if (call.model === "google/gemini-2.5-flash-lite") return flashLite;
    if (call.model === "openai/gpt-5.4-nano") return nano;
    const i = ticketOf(call);
    const n = (floorSeen.get(i) ?? 0) + 1;
    floorSeen.set(i, n);
    return floor(n === 1 ? 1 : 2);
  });
  // One call at a time, so the floor replays arrive in order.
  const run = await executeRun(plan, { providers: { openrouter: provider }, pricing, now: () => NOW, concurrency: 1, toolVersion: "0.0.0" });
  // An assumed volume for the made-up helpdesk, so the demo shows a monthly projection.
  run.settings.monthlyCalls = 250_000;
  await writeFile(RUN, `${JSON.stringify(run, null, 2)}\n`, "utf8");
  console.log(`Wrote ${run.replays.length} replays over ${run.cases.length} cases; spent $${run.spentUsd.toFixed(4)}.`);
}

await writeTraces();
await writeRun();
