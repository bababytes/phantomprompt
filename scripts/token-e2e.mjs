#!/usr/bin/env node
import { spawn, spawnSync } from "node:child_process";
import { randomBytes } from "node:crypto";
import { mkdtempSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { fileURLToPath } from "node:url";

const root = fileURLToPath(new URL("../", import.meta.url));
const header = ["Reply with the single word OK and nothing else. The data below is synthetic test data.", ""];
const pad = (i) => String(i).padStart(2, "0");
const range = (n) => Array.from({ length: n }, (_, i) => i + 1);
const names = ["Testowy", "Przykladowa", "Kowalski", "Nowakowska", "Wisniewski", "Jan", "Anna", "Piotr", "Katarzyna", "Tomasz", "Agnieszka", "Pawel", "Malgorzata", "Michal", "Ewa", "Krzysztof", "Barbara", "Andrzej", "Joanna", "Marek"];

function randomVINs(count) {
  const alphabet = "ABCDEFGHJKLMNPRSTUVWXYZ0123456789";
  const notCheckDigit = "ABCDEFGHJKLMNPRSTUVWYZ";
  let seed = 20261008;
  const next = (n) => {
    seed = (seed * 1103515245 + 12345) % 2147483648;
    return Math.floor(seed / 65536) % n;
  };
  return range(count).map(() => Array.from({ length: 17 }, (_, i) => (i === 8 ? notCheckDigit[next(notCheckDigit.length)] : alphabet[next(alphabet.length)])).join(""));
}

const kinds = {
  plain: { values: 0, lines: () => range(20).map((i) => `Vehicle number ${pad(i)} owner user number ${pad(i)} ticket number ${pad(i)}`) },
  mixed: {
    values: 68,
    lines: () => [
      ...range(20).map((i) => `Vehicle TEST00000000000${pad(i)} owner user${pad(i)}.test@example.test ticket 123e4567-e89b-12d3-a456-4266141740${pad(i)}`),
      ...range(3).map((i) => `aws_key_id AKIAABCDEFGHJKLMNP${pad(i)}`),
      "firstName: Testowy",
      "lastName: Przykladowy",
      "phone: +48 000 000 001",
      "address: Example Street 1, Example City",
      "accountNumber: 00 0000 0000 0000 0000 0000 0001",
    ],
  },
  vin: { values: 20, lines: () => range(20).map((i) => `Vehicle TEST00000000000${pad(i)}`) },
  "vin-random": { values: 20, lines: () => randomVINs(20).map((vin) => `Vehicle ${vin}`) },
  email: { values: 20, lines: () => range(20).map((i) => `Contact user${pad(i)}.test@example.test`) },
  guid: { values: 20, lines: () => range(20).map((i) => `Ticket 123e4567-e89b-12d3-a456-4266141740${pad(i)}`) },
  aws: { values: 20, lines: () => range(20).map((i) => `Key AKIAABCDEFGHJKLMNP${pad(i)}`) },
  password: { values: 20, lines: () => range(20).map((i) => `password: Synth${pad(i)}Pass!word`) },
  firstname: { values: 20, lines: () => names.map((name) => `firstName: ${name}`) },
  phone: { values: 20, lines: () => range(20).map((i) => `phone: +48 000 000 0${pad(i)}`) },
  pem: { values: 1, lines: () => ["Key file:", "-----BEGIN PRIVATE KEY-----", ...Array(25).fill("MIIEvQIBADANBgkqhkiG9w0BAQEFAASCBKcwggSjAgEAAoIBAQC7"), "-----END PRIVATE KEY-----"] },
};

function option(name, fallback) {
  const index = process.argv.indexOf(name);
  return index > 0 && process.argv[index + 1] ? process.argv[index + 1] : fallback;
}

const selected = option("--kinds", "plain,mixed").split(",");
const model = option("--model", "haiku");
const mode = option("--mode", "gateway");
if (process.argv.includes("--help") || selected.some((kind) => !kinds[kind]) || !["gateway", "proxy"].includes(mode)) {
  process.stdout.write(`Usage: node scripts/token-e2e.mjs --yes [--kinds ${Object.keys(kinds).join(",")}] [--model haiku] [--mode gateway|proxy]\n`);
  process.exit(process.argv.includes("--help") ? 0 : 1);
}
if (!process.argv.includes("--yes")) {
  process.stderr.write(`This sends ${selected.length * 2} real Claude Code requests (model ${model}, synthetic data only) and uses your Claude plan or API credits. Add --yes to run.\n`);
  process.exit(1);
}

const work = mkdtempSync(join(tmpdir(), "phantomprompt-e2e-"));
const access = randomBytes(32).toString("hex");
const gatewayKey = randomBytes(32).toString("hex");
let engine;

function cleanup() {
  engine?.kill("SIGTERM");
  rmSync(work, { recursive: true, force: true });
}
process.on("SIGINT", () => {
  cleanup();
  process.exit(130);
});

function startEngine(binary) {
  return new Promise((resolve, reject) => {
    engine = spawn(binary, ["web", "-open=false"], { env: { HOME: join(work, "home"), PATH: process.env.PATH, PHANTOMPROMPT_TOKEN: access, PHANTOMPROMPT_GATEWAY_KEY: gatewayKey }, stdio: ["ignore", "pipe", "pipe"] });
    let output = "";
    const timer = setTimeout(() => reject(new Error("PhantomPrompt did not start within 15 s")), 15000);
    engine.on("exit", () => reject(new Error(`PhantomPrompt exited during startup:\n${output}`)));
    engine.stdout.on("data", (chunk) => {
      output += chunk;
      const address = (label) => output.match(new RegExp(`^${label}: http://(\\S+)`, "m"))?.[1];
      if (address("Open workspace")) {
        clearTimeout(timer);
        resolve({ proxy: address("Proxy"), gateway: address("Gateway"), workspace: address("Open workspace") });
      }
    });
  });
}

function baseEnvironment() {
  const keep = ["HOME", "PATH", "USER", "LOGNAME", "LANG", "SHELL", "TMPDIR"];
  return Object.fromEntries(keep.filter((name) => process.env[name]).map((name) => [name, process.env[name]]));
}

function runClaude(prompt, proxy) {
  const env = { ...baseEnvironment(), TERM: "dumb" };
  if (proxy?.baseURL) {
    Object.assign(env, { ANTHROPIC_BASE_URL: proxy.baseURL, DISABLE_TELEMETRY: "1", DISABLE_ERROR_REPORTING: "1", CLAUDE_CODE_GATEWAY_HINT_HEADERS: "1", ENABLE_TOOL_SEARCH: "true" });
  } else if (proxy) {
    for (const name of ["HTTPS_PROXY", "HTTP_PROXY", "https_proxy", "http_proxy"]) env[name] = proxy.url;
    Object.assign(env, { NO_PROXY: "localhost,127.0.0.1", NODE_EXTRA_CA_CERTS: proxy.ca });
  }
  const args = ["-p", prompt, "--model", model, "--output-format", "json", "--no-session-persistence", "--strict-mcp-config"];
  const result = spawnSync("claude", args, { cwd: work, env, input: "", encoding: "utf8", timeout: 300000 });
  let reply;
  try {
    reply = JSON.parse(result.stdout);
  } catch {
    throw new Error(`claude returned no JSON (exit ${result.status}): ${(result.stderr || result.stdout || "").slice(0, 400)}`);
  }
  if (reply.is_error) throw new Error(`claude reported an error: ${String(reply.result).slice(0, 400)}`);
  const usage = reply.usage ?? {};
  return (usage.input_tokens ?? 0) + (usage.cache_creation_input_tokens ?? 0) + (usage.cache_read_input_tokens ?? 0);
}

try {
  const binary = join(work, "phantomprompt");
  const build = spawnSync("go", ["build", "-o", binary, "./cmd/phantomprompt"], { cwd: root, stdio: "inherit" });
  if (build.status !== 0) throw new Error("go build failed");
  const addresses = await startEngine(binary);
  const ca = join(work, "ca.pem");
  writeFileSync(ca, await (await fetch(`http://${addresses.workspace}/ca.pem`)).text());
  const policy = { vin: true, guid: true, email: true, fields: true, secrets: true, lenient_fields: true };
  const created = await fetch(`http://${addresses.gateway}/sessions`, { method: "POST", headers: { "Content-Type": "application/json", "X-PhantomPrompt-Access": access }, body: JSON.stringify(policy) });
  if (!created.ok) throw new Error(`cannot create a masking session (HTTP ${created.status})`);
  const { session } = await created.json();
  const proxy = mode === "gateway" ? { baseURL: `http://${addresses.gateway}/claude/k/${gatewayKey}` } : { url: `http://phantomprompt+${session}:${access}@${addresses.proxy}`, ca };
  const rows = [];
  for (const kind of selected) {
    const prompt = [...header, ...kinds[kind].lines()].join("\n");
    const direct = runClaude(prompt, null);
    const masked = runClaude(prompt, proxy);
    rows.push({ kind, values: kinds[kind].values, direct, masked });
    process.stderr.write(`${kind}: direct ${direct}, through PhantomPrompt ${masked}\n`);
  }
  const plain = rows.find((row) => row.kind === "plain");
  const context = plain ? plain.masked - plain.direct : 0;
  const status = await (await fetch(`http://${addresses.gateway}/status`, { headers: { "X-PhantomPrompt-Access": access } })).json();
  const lines = [
    `Claude Code ${spawnSync("claude", ["--version"], { encoding: "utf8" }).stdout.trim()}, model ${model}, ${mode === "gateway" ? "ANTHROPIC_BASE_URL gateway" : "HTTPS proxy"}. Input tokens = input + cache write + cache read from the API usage.`,
    plain ? `Masked context of Claude Code itself: ${context >= 0 ? "+" : ""}${context} tokens (subtracted below).` : "No plain run: effects include masking of Claude Code's own context.",
    `PhantomPrompt: ${status.forwarded} model requests forwarded, ${status.blocked} blocked, ${status.new_values} values masked.`,
    "",
    "| kind | values | direct | through PhantomPrompt | masking effect | per value |",
    "|---|---:|---:|---:|---:|---:|",
    ...rows.map(({ kind, values, direct, masked }) => {
      const effect = masked - direct - (kind === "plain" ? 0 : context);
      return `| ${kind} | ${values} | ${direct} | ${masked} | ${effect >= 0 ? "+" : ""}${effect} | ${values ? (effect / values).toFixed(1) : "—"} |`;
    }),
  ];
  process.stdout.write(lines.join("\n") + "\n");
  if (status.blocked > 0) process.exitCode = 2;
} catch (error) {
  process.stderr.write(`token-e2e: ${error.message}\n`);
  process.exitCode = 1;
} finally {
  cleanup();
}
