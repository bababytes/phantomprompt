<script setup>
import { computed, reactive, ref, watch } from "vue";
import { logsInFinder } from "../config.js";
import { clearEvents, folderName, monitor, refresh, showLogFolder, time, tokenOverhead } from "../monitor.js";
import RuleSummary from "./RuleSummary.vue";
import ValueSummary from "./ValueSummary.vue";
import { valueRows } from "../valueRows.js";

const SERVICES = { "copilot-cli": "GitHub Copilot CLI", "claude-code": "Claude Code", "codex-cli": "Codex CLI", demo: "Local demo", "openai-chat": "OpenAI API", "anthropic-messages": "Anthropic API" };
const RESULTS = {
  accepted: "Masked",
  stream_complete: "Masked · reply restored",
  masked_and_restored: "Masked · reply restored",
  passed_without_restore: "Sent · reply not restored",
  sensitive_value_outside_prompt: "Blocked · protected value outside the prompt",
  attachments_not_supported: "Blocked · attachment",
  unknown_token_in_input: "Blocked · unknown token",
  new_content_type_masked: "Masked · new content type (check for a PhantomPrompt update)",
  secret_kept_masked_in_tool: "Secret kept masked in a shell or web command",
  placeholders_not_restored: "Reply · placeholders not restored",
  unsupported_content: "Blocked · unknown request format",
  masking_limit: "Blocked · masking limit",
  tool_arguments_masked: "Masked · tool call to GitHub",
  model_format_not_supported_yet: "Blocked · model format not supported yet (choose a Claude model)",
  session_expired_restart_cli: "Blocked · session expired, restart the CLI",
  sensitive_value_not_masked_yet: "Blocked · protected value in an unsupported channel",
  upgrade_not_supported: "Blocked · websocket upgrade",
  body_limit: "Blocked · request too large",
  body_limit_or_encoding_error: "Blocked · request too large or unreadable",
  masked_body_limit: "Blocked · masked request too large",
  invalid_compression: "Blocked · broken compression",
  unsupported_encoding: "Blocked · unsupported compression",
  unsupported_content_or_masking_limit: "Blocked · unknown format or masking limit",
  session_expired_or_wrong_origin: "Blocked · session expired",
  session_unavailable: "Blocked · session unavailable",
  concurrency_limit: "Blocked · too many requests at once",
  route_or_method_not_supported: "Blocked · route not supported",
  upstream_TLS_or_connection_failed: "Failed · AI service unreachable",
  upstream_rejected_request: "Failed · AI service rejected the request",
  unsupported_response_content: "Failed · reply format unknown",
  unsupported_upstream_response: "Failed · reply format unknown",
  compressed_upstream_response_not_supported: "Failed · compressed reply",
  response_limit: "Failed · reply too large",
  stream_interrupted: "Failed · stream interrupted",
  protection_enabled: "Protection turned on",
  protection_disabled: "Protection turned OFF",
};
const COUNT_LABELS = { vin: "VIN", guid: "GUID", email: "email", fields: "field", custom: "custom", pesel: "PESEL", secret: "secret/password" };

const expanded = reactive(new Set());
const query = ref("");
const view = ref(null);
const VIEWS = [{ key: "field", label: "By field" }, { key: "prompt", label: "By prompt" }, { key: "rule", label: "By rule" }];
const seenAt = reactive({ field: 0, prompt: 0, rule: 0 });
let baselined = false;
const sort = reactive({ key: "time", dir: "desc" });
const COLUMNS = [
  { key: "time", label: "Time", hint: "When the prompt was sent." },
  { key: "service", label: "Service", hint: "AI tool that sent the prompt, with its project folder." },
  { key: "masked", label: "Masked values", hint: "New values per type, split by source: typed by you, found by AI, or added by the tool itself. Folders show blocked or masked access." },
  { key: "protection", label: "Protection time", hint: "Time PhantomPrompt added to mask the prompt and restore the reply." },
  { key: "status", label: "Status", hint: "Result: protected, blocked, failed, or a warning to check. Click the row for details." },
];

const summary = computed(() => {
  const requests = monitor.totals.prompts;
  return requests?.checked
    ? `${monitor.totals.prompts_sent ?? 0} prompt(s) sent in ${requests.checked} AI request(s) · average PhantomPrompt overhead ${requests.average_overhead_ms.toFixed(2)} ms per request.`
    : "No prompts yet.";
});

const unrestored = computed(() => monitor.events.reduce((sum, event) => sum + (event.unrestored ?? 0), 0));

const historyStatus = computed(() => {
  if (monitor.error) return monitor.error;
  if (!monitor.updated) return "Loading prompt history…";
  const warning = unrestored.value ? `⚠ Replies kept ${unrestored.value} placeholder(s) that were not restored; see By prompt. ` : "";
  return warning + (monitor.events.length ? `${monitor.events.length} prompt(s) with protected values, blocks or AI service errors. Click a row for details. Updated ${monitor.updated.toLocaleTimeString()}.` : "Nothing to show: no prompt contained protected values and nothing was blocked.");
});

const activity = computed(() => ({
  field: valueRows(monitor.events, SERVICES).map((row) => row.last),
  prompt: monitor.events.map((event) => Date.parse(event.time)),
  rule: [...(monitor.totals.custom_rules ?? []), ...(monitor.totals.secret_rules ?? [])].filter((stat) => stat.hits > 0).map((stat) => Date.parse(stat.last_hit)),
}));

const unseen = computed(() => Object.fromEntries(VIEWS.map(({ key }) => [key, activity.value[key].filter((last) => last > seenAt[key]).length])));

watch([activity, view], ([times, open]) => {
  if (!monitor.updated) return;
  for (const { key } of VIEWS) {
    if (!baselined || key === open) seenAt[key] = Math.max(seenAt[key], ...times[key].filter(Number.isFinite));
  }
  baselined = true;
}, { immediate: true });

function choose(key) {
  view.value = view.value === key ? null : key;
}

function channel(event) {
  return ["copilot-cli", "claude-code", "codex-cli"].includes(event.adapter) ? "CLI" : "proxy";
}

function statusInfo(event) {
  if (event.phase === "control") return { text: RESULTS[event.reason] ?? event.reason, cls: event.reason === "protection_disabled" ? "status-warn" : "status-ok" };
  if (event.reason === "passed_without_restore") return { text: event.status >= 400 ? `AI service answered ${event.status} · passed to the tool` : "Sent · reply not restored", cls: "status-warn" };
  if (event.status >= 400) {
    const label = RESULTS[event.reason] ?? `Blocked · ${event.reason}`;
    const text = label.startsWith("Blocked") || label.startsWith("Failed") ? label : `Blocked · ${label}`;
    return { text: event.repeats ? `${text} ×${event.repeats + 1}` : text, cls: "status-block" };
  }
  if (event.unrestored > 0) return { text: `⚠ ${event.unrestored} placeholder(s) not restored in the reply`, cls: "status-warn" };
  if (event.matches.attachments > 0) return { text: `⚠ ${event.matches.attachments} image(s) or file(s) sent without checking`, cls: "status-warn" };
  const blockedFolders = Object.keys(event.matches.blocked_folders ?? {});
  if (blockedFolders.length) return { text: `🚫 Folder access blocked (${blockedFolders.join(", ")})`, cls: "status-secret" };
  if ((event.matches.secret ?? 0) > 0) return { text: `🔒 Password/secret masked (${event.matches.secret})`, cls: "status-secret" };
  return { text: "Protected", cls: "status-ok" };
}

function kindList(kinds) {
  return Object.entries(kinds).filter(([kind, count]) => COUNT_LABELS[kind] && count > 0).map(([kind, count]) => `${COUNT_LABELS[kind]} ${count}`).join(", ");
}

function maskedValues(event) {
  const unique = event.matches.new_kinds;
  if (!unique) return [...folderAccess(event), kindList(event.matches)].filter(Boolean).join(" · ") || "—";
  const typed = event.matches.typed_kinds ?? {};
  const context = event.matches.context_kinds ?? {};
  const found = Object.fromEntries(Object.entries(unique).map(([kind, n]) => [kind, n - (typed[kind] ?? 0) - (context[kind] ?? 0)]));
  const sources = [["you", typed], ["found by AI", found], [SERVICES[event.adapter] ?? "tool", context]].map(([source, kinds]) => [source, kindList(kinds)]).filter(([, list]) => list);
  return [...folderAccess(event), ...sources.map(([source, list]) => `${source}: ${list}`)].join(" · ") || "—";
}

function folderAccess(event) {
  const access = (folders, action) => Object.entries(folders ?? {}).map(([folder, n]) => (n > 1 ? `${folder} ${action} ×${n}` : `${folder} ${action}`));
  return [...access(event.matches.blocked_folders, "blocked"), ...access(event.matches.masked_folders, "masked")];
}

function details(event) {
  const overhead = event.overhead_ms > 0;
  const aiTime = overhead && event.elapsed_ms > event.overhead_ms ? `${((event.elapsed_ms - event.overhead_ms) / 1000).toFixed(1)} s` : null;
  return [
    `Channel: ${channel(event)}`,
    event.folder ? `Folder: ${folderName(event.folder)}` : null,
    `Reason: ${RESULTS[event.reason] ?? event.reason}`,
    overhead ? `PhantomPrompt overhead: ${event.overhead_ms.toFixed(2)} ms` : null,
    aiTime ? `AI response: ${aiTime}` : null,
    event.route ? `Route: ${event.route}` : null,
    event.requests > 1 ? `Model calls in this turn: ${event.requests}` : null,
    event.ai_errors ? `AI service errors passed to the tool, e.g. rate limit 429: ${event.ai_errors}` : null,
    event.matches.attachments ? `Images or files sent to the AI without checking: ${event.matches.attachments}` : null,
    event.unrestored ? `Placeholders left in the reply (changed by the model or not restored): ${event.unrestored}` : null,
    event.rules ? `Rules that matched: ${Object.entries(event.rules).map(([name, use]) => `${name} (${use.values} value${use.values === 1 ? "" : "s"}, ${use.hits} match${use.hits === 1 ? "" : "es"}${use.secret ? ", secret" : ""})`).join(", ")}` : null,
    folderAccess(event).length ? `Protected folders: ${folderAccess(event).join(", ")} (folder content is not logged)` : null,
    event.matches.context_kinds ? `From the tool's own context (system prompt, reminders): ${Object.entries(event.matches.context_kinds).map(([kind, n]) => `${COUNT_LABELS[kind] ?? kind} ${n}`).join(", ")}` : null,
    event.matches.token_overhead ? `Tokens from masking (net estimate) ${tokenOverhead(event.matches.token_overhead, event.input_tokens)}` : null,
    event.input_tokens ? `Input tokens reported by the AI service: ${event.input_tokens.toLocaleString()}` : null,
    `Input → masked: ${event.input_bytes} → ${event.masked_bytes} B`,
  ].filter(Boolean).join("  ·  ");
}

function protectionTime(event) {
  if (!(event.overhead_ms > 0)) return "—";
  return event.overhead_ms < 10 ? `${event.overhead_ms.toFixed(1)} ms` : `${Math.round(event.overhead_ms)} ms`;
}

function uniqueTotal(event) {
  const counts = [event.matches.new_kinds, event.matches.blocked_folders, event.matches.masked_folders].flatMap((group) => Object.values(group ?? {}));
  return counts.reduce((sum, n) => sum + n, 0);
}

const rows = computed(() => {
  const needle = query.value.trim().toLowerCase();
  const list = monitor.events.map((event) => ({ event, service: SERVICES[event.adapter] ?? "—", project: folderName(event.folder), masked: maskedValues(event), status: statusInfo(event), protection: protectionTime(event) }))
    .filter((row) => !needle || [time(row.event.time), row.service, row.event.folder ?? "", row.masked, row.status.text, RESULTS[row.event.reason] ?? row.event.reason, row.event.route].join(" ").toLowerCase().includes(needle));
  const value = { time: (row) => Date.parse(row.event.time), service: (row) => row.service, masked: (row) => uniqueTotal(row.event), protection: (row) => row.event.overhead_ms || 0, status: (row) => row.status.text };
  const get = value[sort.key];
  return list.sort((a, b) => {
    const x = get(a);
    const y = get(b);
    const order = typeof x === "string" ? x.localeCompare(y) : x - y;
    return sort.dir === "asc" ? order : -order;
  });
});

function sortBy(key) {
  if (sort.key === key) sort.dir = sort.dir === "asc" ? "desc" : "asc";
  else Object.assign(sort, { key, dir: key === "service" || key === "status" ? "asc" : "desc" });
}

function toggle(id) {
  if (expanded.has(id)) expanded.delete(id);
  else expanded.add(id);
}

const logFolderCopied = ref(false);

async function copyLogFolder() {
  await navigator.clipboard.writeText(monitor.totals.log_folder);
  logFolderCopied.value = true;
  setTimeout(() => (logFolderCopied.value = false), 2000);
}

const logFolderError = ref("");

async function openLogFolder() {
  logFolderError.value = "";
  try {
    await showLogFolder();
  } catch (failure) {
    logFolderError.value = failure.message;
  }
}
</script>

<template>
  <section class="workspace-card events-card">
    <div class="section-heading">
      <div><p class="eyebrow">PROMPT HISTORY</p><h2>Protected values</h2></div>
      <div><button id="refresh-events" class="secondary" @click="refresh">Refresh</button><button id="clear-events" class="secondary" @click="clearEvents">Clear history</button></div>
    </div>
    <p>What PhantomPrompt protected, per field, per prompt or per rule. Only metadata is kept, without prompt text, original values, credentials or session IDs: the last 200 entries here in RAM; errors, blocked requests and protection changes also go to the daily log.</p>
    <details class="history-help">
      <summary>How to read this</summary>
      <p><strong>By field</strong>: one row per protected value type or field; the counter shows how many different values from your data were protected. Protected folders appear as one row each, with the number of times an AI reached for them and whether it was blocked or masked; what the folder contains is never shown.</p>
      <p><strong>By prompt</strong>: one row per question, with all model calls of that question merged; click a row for timing and token details. Values the AI tool adds itself (system prompt, reminders) are counted separately as tool context.</p>
      <p>A list opens when you click its button and closes on a second click. A small green number on a button counts entries with new catches since you last opened that list.</p>
      <p><strong>By rule</strong>: one row per masking rule that has caught something, your own or a built-in secret pattern, with how many times it matched, how many different values it caught and when it last fired.</p>
      <p>Hover a column title for a short explanation of what it shows.</p>
    </details>
    <details v-if="monitor.totals.log_folder" id="log-folder" class="history-help">
      <summary>Log folder</summary>
      <p><code>{{ monitor.totals.log_folder }}</code><button type="button" class="text-button" @click="copyLogFolder">{{ logFolderCopied ? "Copied" : "Copy path" }}</button><button v-if="logsInFinder" id="show-logs" type="button" class="text-button" @click="openLogFolder">Show in Finder</button></p>
      <p v-if="logFolderError" class="error" role="alert">{{ logFolderError }}</p>
      <p>One file per day with important entries only: errors, blocked requests, protection changes, app start and stop. Kept for 7 days, also after a restart and after Clear history.</p>
    </details>
    <p id="events-summary" class="summary">{{ summary }}</p>
    <p id="events-status" role="status" aria-live="polite">{{ historyStatus }}</p>
    <div class="history-search">
      <div class="segmented" role="group" aria-label="History view">
        <button v-for="option in VIEWS" :key="option.key" :class="{ active: view === option.key }" :aria-pressed="view === option.key" @click="choose(option.key)">
          {{ option.label }}<span v-if="unseen[option.key]" class="view-new" :aria-label="`${unseen[option.key]} new`">{{ unseen[option.key] > 99 ? "99+" : unseen[option.key] }}</span>
        </button>
      </div>
      <template v-if="view">
        <label class="sr-only" for="history-search">Search history</label>
        <input id="history-search" v-model="query" type="text" placeholder="Search: field name, service, status…" autocomplete="off" spellcheck="false">
      </template>
      <span v-if="query.trim() && view === 'prompt'">{{ rows.length }} of {{ monitor.events.length }} rows</span>
    </div>
    <ValueSummary v-if="view === 'field'" :query="query" :services="SERVICES" />
    <RuleSummary v-else-if="view === 'rule'" :query="query" />
    <div v-else-if="view === 'prompt'" class="table-scroll">
      <table>
        <thead>
          <tr>
            <th v-for="column in COLUMNS" :key="column.key" :aria-sort="sort.key === column.key ? (sort.dir === 'asc' ? 'ascending' : 'descending') : 'none'">
              <button class="sort-button" :title="column.hint" @click="sortBy(column.key)">{{ column.label }}<span aria-hidden="true">{{ sort.key === column.key ? (sort.dir === "asc" ? " ▲" : " ▼") : "" }}</span></button>
            </th>
          </tr>
        </thead>
        <tbody id="event-rows">
          <template v-for="row in rows" :key="row.event.id">
            <tr class="event-row" tabindex="0" :aria-expanded="expanded.has(row.event.id)" @click="toggle(row.event.id)" @keydown.enter.prevent="toggle(row.event.id)" @keydown.space.prevent="toggle(row.event.id)">
              <td>{{ time(row.event.time) }}</td>
              <td>{{ row.service }}<span v-if="row.project" class="row-project" :title="row.event.folder">{{ row.project }}</span></td>
              <td>{{ row.masked }}</td>
              <td>{{ row.protection }}</td>
              <td :class="row.status.cls">{{ row.status.text }}</td>
            </tr>
            <tr v-if="expanded.has(row.event.id)" class="detail-row"><td colspan="5">{{ details(row.event) }}</td></tr>
          </template>
        </tbody>
      </table>
    </div>
  </section>
</template>

<style scoped>
.history-help { margin: -4px 0 14px; }
.history-help summary { width: fit-content; color: var(--accent); font-size: 12px; cursor: pointer; }
.history-help p { margin: 8px 0 0; }
.history-help code { overflow-wrap: anywhere; }
.history-search { display: flex; align-items: center; gap: 12px; margin-top: 12px; }
.history-search input { max-width: 420px; }
.history-search > span { font-family: var(--mono); font-size: 11px; color: var(--muted); white-space: nowrap; }
.view-new { display: inline-block; min-width: 16px; height: 16px; margin-left: 6px; padding: 0 5px; border: 1px solid rgb(76 201 149 / .35); border-radius: 999px; background: var(--accent-soft); color: var(--accent); font: 600 10px/14px var(--mono); text-align: center; vertical-align: 1px; }
.row-project { margin-left: 8px; font-family: var(--mono); font-size: 11px; color: var(--muted); }
</style>
