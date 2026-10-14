import { reactive } from "vue";
import { locked, showLocked } from "./config.js";
import { ask, tell } from "./dialog.js";
import { layout } from "./layout.js";
import { headers } from "./workspace.js";

export const monitor = reactive({
  totals: {},
  events: [],
  discovery: [],
  protection: true,
  stopped: false,
  updated: null,
  error: "",
});

let refreshing = false;
let protectionPending = false;

export async function refresh() {
  if (refreshing) return;
  refreshing = true;
  try {
    const responses = await Promise.all([fetch("/status", { headers: headers(), signal: AbortSignal.timeout(10000) }), fetch("/events", { headers: headers(), signal: AbortSignal.timeout(10000) })]);
    if (responses.some((response) => response.status === 401)) showLocked();
    if (responses.some((response) => !response.ok)) throw new Error("Request history unavailable.");
    const [totals, events] = await Promise.all(responses.map((response) => response.json()));
    monitor.totals = totals;
    if (!protectionPending) monitor.protection = totals.protection !== false;
    monitor.events = events.reverse();
    if (monitor.events.some((event) => event.status >= 400)) monitor.discovery = await loadDiscovery();
    monitor.updated = new Date();
    monitor.error = "";
  } catch (error) {
    monitor.error = error.message;
  } finally {
    refreshing = false;
  }
}

async function loadDiscovery() {
  try {
    const response = await fetch("/discovery", { headers: headers(), signal: AbortSignal.timeout(10000) });
    return response.ok ? await response.json() : monitor.discovery;
  } catch {
    return monitor.discovery;
  }
}

export const FULL_VIEW_POLL_MS = 3000;

export function startPolling() {
  let last = Date.now();
  refresh();
  setInterval(() => {
    const every = document.hidden ? 12000 : layout.compact ? Math.min(2000, layout.barSeconds * 1000) : FULL_VIEW_POLL_MS;
    if (locked || Date.now() - last < every - 250) return;
    last = Date.now();
    refresh();
  }, 500);
}

export async function setProtection(on) {
  protectionPending = true;
  try {
    const response = await fetch("/api/protection", { method: "POST", headers: headers(), body: JSON.stringify({ on }), signal: AbortSignal.timeout(10000) });
    if (!response.ok) return false;
    monitor.protection = on;
    return true;
  } catch {
    return false;
  } finally {
    protectionPending = false;
  }
}

export async function stopApp() {
  if (monitor.stopped || !(await ask({ title: "Stop PhantomPrompt?", message: "All protection stops until you start it again with the app icon or phantom open.", confirm: "Stop", danger: true }))) return;
  try {
    const response = await fetch("/api/shutdown", { method: "POST", headers: headers(), signal: AbortSignal.timeout(10000) });
    if (!response.ok) throw new Error("Could not stop PhantomPrompt.");
    monitor.stopped = true;
  } catch (error) {
    await tell({ title: "Could not stop PhantomPrompt", message: error.message });
  }
}

export const terminalName = (item) => (item.pid ? `${item.label} (pid ${item.pid})` : item.label);

export const confirmCloseTerminal = async (item) => !(item.active || item.disconnected) || ask({ title: `Close the terminal ${terminalName(item)}?`, message: "Everything running in it stops.", confirm: "Close terminal", danger: true });

export async function closeTerminal(id) {
  const response = await fetch("/api/terminal/close", { method: "POST", headers: headers(), body: JSON.stringify({ id }), signal: AbortSignal.timeout(10000) });
  if (!response.ok) throw new Error((await response.text()).trim() || "Could not close the terminal.");
  await refresh();
}

export async function showLogFolder() {
  const response = await fetch("/api/logs/show", { method: "POST", headers: headers(), signal: AbortSignal.timeout(10000) });
  if (!response.ok) throw new Error((await response.text()).trim() || "Could not open the log folder.");
}

export async function clearEvents() {
  if (!(await ask({ title: "Clear the history and reset all counters?", message: "Protection, rules and open terminals stay as they are; the daily log is kept.", confirm: "Clear and reset", danger: true }))) return;
  try {
    const response = await fetch("/events", { method: "DELETE", headers: headers(), signal: AbortSignal.timeout(10000) });
    if (!response.ok) throw new Error("Cannot clear request history.");
    await refresh();
  } catch (error) {
    monitor.error = error.message;
  }
}

export function terminalState(item) {
  if (item.disconnected) return { text: "Disconnected · PhantomPrompt was restarted, AI requests fail · close it and open a new one", cls: "status-block" };
  if (item.expired) return { text: "Session expired · restart the command", cls: "status-block" };
  if (item.active) return { text: "Protected · active", cls: "status-ok" };
  return { text: "No heartbeat · terminal closed?", cls: "status-warn" };
}

export const time = (value) => (value ? new Date(value).toLocaleTimeString() : "—");

export function folderName(path) {
  if (!path) return "";
  const home = /^\/Users\/[^/]+(?=\/|$)/.exec(path)?.[0] ?? "";
  const parts = path.slice(home.length).split("/").filter(Boolean);
  return parts.at(-1) ?? (home ? "~" : "/");
}

const PERCENT = new Intl.NumberFormat("en", { maximumSignificantDigits: 2 });

export function tokenShare(tokens, total) {
  if (total <= 0) return "";
  const percent = (Math.abs(tokens) / total) * 100;
  if (percent === 0) return "0%";
  return percent < 0.001 ? "<0.001%" : `${PERCENT.format(percent)}%`;
}

export function tokenOverhead(overhead, input = 0, usageOverhead = overhead) {
  if (!overhead) return "—";
  const share = input > 0 ? ` (${tokenShare(usageOverhead, input)} of ${input.toLocaleString()})` : "";
  return `≈ ${overhead > 0 ? "−" : "+"}${Math.abs(overhead).toLocaleString()} ${overhead > 0 ? "extra" : "saved"}${share}`;
}
