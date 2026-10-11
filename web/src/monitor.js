import { reactive } from "vue";
import { locked, showLocked } from "./config.js";
import { headers } from "./workspace.js";

export const monitor = reactive({
  totals: {},
  events: [],
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
    monitor.updated = new Date();
    monitor.error = "";
  } catch (error) {
    monitor.error = error.message;
  } finally {
    refreshing = false;
  }
}

export function startPolling() {
  refresh();
  setInterval(() => { if (!document.hidden && !locked) refresh(); }, 3000);
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
  if (monitor.stopped || !confirm("Stop PhantomPrompt? All protection stops until you start it again with the app icon or phantom open.")) return;
  try {
    const response = await fetch("/api/shutdown", { method: "POST", headers: headers(), signal: AbortSignal.timeout(10000) });
    if (!response.ok) throw new Error("Could not stop PhantomPrompt.");
    monitor.stopped = true;
  } catch (error) {
    alert(error.message);
  }
}

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
  try {
    const response = await fetch("/events", { method: "DELETE", headers: headers(), signal: AbortSignal.timeout(10000) });
    if (!response.ok) throw new Error("Cannot clear request history.");
    await refresh();
  } catch (error) {
    monitor.error = error.message;
  }
}

export const time = (value) => (value ? new Date(value).toLocaleTimeString() : "—");

export function folderName(path) {
  if (!path) return "";
  const home = /^\/Users\/[^/]+(?=\/|$)/.exec(path)?.[0] ?? "";
  const parts = path.slice(home.length).split("/").filter(Boolean);
  return parts.at(-1) ?? (home ? "~" : "/");
}

export function tokenOverhead(overhead, input = 0) {
  if (!overhead) return "—";
  const share = input > 0 ? ` (${((Math.abs(overhead) / input) * 100).toFixed(1)}% of ${input.toLocaleString()})` : "";
  return `≈ ${Math.abs(overhead).toLocaleString()} ${overhead > 0 ? "added" : "saved"}${share}`;
}
