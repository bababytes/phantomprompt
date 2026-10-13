<script setup>
import { computed, onBeforeUnmount, onMounted, ref } from "vue";
import { SERVICES, maskedCount, quietBlock } from "../events.js";
import { blockedSince } from "./data.js";
import { ask } from "../dialog.js";
import { clearEvents, monitor } from "../monitor.js";
import { clearTimeline, timeline } from "./timeline.js";

const HOUR = 60 * 60 * 1000;
const now = ref(Date.now());
let timer = null;

const lastMasked = computed(() => {
  const sample = timeline.samples.findLast((item) => Object.keys(item.kinds).length);
  if (sample) return { time: sample.time, adapter: sample.adapter };
  const event = monitor.events.find((item) => item.status < 400 && item.phase !== "control" && maskedCount(item) > 0);
  return event ? { time: Date.parse(event.time), adapter: event.adapter } : null;
});
const lastBlocked = computed(() => monitor.events.find((event) => event.status >= 400 && event.phase !== "control" && !quietBlock(event)));
const blocked = computed(() => blockedSince(monitor.events, now.value - HOUR));
const overhead = computed(() => (monitor.totals.prompts?.checked ? `${monitor.totals.prompts.average_overhead_ms.toFixed(2)} ms` : "—"));
const live = computed(() => {
  if (monitor.stopped) return { text: "stopped", cls: "down" };
  if (monitor.error) return { text: "offline", cls: "down" };
  return { text: monitor.updated ? `updated ${monitor.updated.toLocaleTimeString()}` : "connecting…", cls: monitor.protection ? "" : "off" };
});

function ago(time) {
  const seconds = Math.max(0, Math.round((now.value - new Date(time).getTime()) / 1000));
  if (seconds < 60) return `${seconds} s ago`;
  if (seconds < 3600) return `${Math.floor(seconds / 60)} min ago`;
  if (seconds < 86400) return `${Math.floor(seconds / 3600)} h ago`;
  return `on ${new Date(time).toLocaleDateString()}`;
}

async function reset() {
  if (!(await ask({ title: "Clear the history and reset all counters?", message: "Protection, rules and open terminals stay as they are; the daily log is kept.", confirm: "Clear and reset", danger: true }))) return;
  await clearEvents();
  clearTimeline();
}

const recent = (item, ms) => Boolean(item) && now.value - new Date(item.time).getTime() < ms;

onMounted(() => {
  timer = setInterval(() => (now.value = Date.now()), 1000);
});

onBeforeUnmount(() => clearInterval(timer));
</script>

<template>
  <div class="live-strip" :class="live.cls" role="status" :title="monitor.error || undefined">
    <span id="live-state" class="strip-cell"><i aria-hidden="true"></i><b>{{ live.cls === "down" ? "Offline" : monitor.protection ? "Live" : "Protection off" }}</b><span>{{ live.text }}</span></span>
    <span id="live-masked" class="strip-cell" :class="{ fresh: recent(lastMasked, 30000) }" :title="lastMasked ? new Date(lastMasked.time).toLocaleString() : undefined">
      <template v-if="lastMasked">Last masked <b>{{ ago(lastMasked.time) }}</b><span>· {{ SERVICES[lastMasked.adapter] ?? lastMasked.adapter }}</span></template>
      <template v-else>No masked prompt yet</template>
    </span>
    <span id="live-blocked" class="strip-cell blocked" :class="{ hot: blocked > 0, fresh: recent(lastBlocked, 60000) }" :title="`Blocked or failed AI traffic; stopped telemetry is listed in Recent activity but not counted here${lastBlocked ? ` · last blocked ${ago(lastBlocked.time)}` : ''}`"><b>{{ blocked }}</b> blocked · last hour</span>
    <span id="live-overhead" class="strip-cell" title="Average time PhantomPrompt adds to mask a request and restore the reply">Avg overhead <b>{{ overhead }}</b></span>
    <button id="compact-reset" type="button" class="strip-reset" title="Clear the history and reset all counters" :disabled="monitor.stopped" @click="reset"><svg viewBox="0 0 16 16" aria-hidden="true"><path d="M3.2 6.2A5 5 0 1 1 3 9.6M3.2 2.8v3.4h3.4" /></svg>Reset</button>
  </div>
</template>

<style scoped>
.live-strip { position: relative; display: flex; align-items: stretch; height: 32px; overflow: hidden; border: 1px solid var(--line-strong); border-radius: 8px; background: linear-gradient(90deg, rgb(76 201 149 / .05), rgb(var(--ink-rgb) / .015) 30%, rgb(var(--ink-rgb) / .015) 70%, rgb(169 155 255 / .05)), var(--bg-sunk); font: 500 10.5px/1 var(--mono); letter-spacing: .08em; text-transform: uppercase; color: var(--faint); }
.live-strip::before, .live-strip::after { content: ""; position: absolute; pointer-events: none; }
.live-strip::before { inset: 0; background: repeating-linear-gradient(90deg, transparent 0 7px, rgb(var(--ink-rgb) / .025) 7px 8px); }
.live-strip::after { top: 0; bottom: 0; left: -160px; width: 160px; background: linear-gradient(90deg, transparent, rgb(76 201 149 / .12), transparent); animation: strip-sweep 7s linear infinite; }
.live-strip.off::after, .live-strip.down::after { animation-play-state: paused; opacity: 0; }
.strip-cell { position: relative; z-index: 1; display: flex; flex: 1 1 0; align-items: center; justify-content: center; gap: 7px; min-width: 0; padding: 0 14px; overflow: hidden; white-space: nowrap; }
.strip-cell + .strip-cell { border-left: 1px solid var(--line-strong); }
.strip-reset { position: relative; z-index: 1; display: flex; flex: none; align-items: center; gap: 6px; margin: 0; padding: 0 14px; border: 0; border-left: 1px solid var(--line-strong); background: transparent; font: inherit; letter-spacing: inherit; text-transform: inherit; color: var(--muted); cursor: pointer; }
.strip-reset:hover:enabled { background: rgb(var(--ink-rgb) / .05); color: var(--text); }
.strip-reset svg { width: 12px; height: 12px; fill: none; stroke: currentColor; stroke-width: 1.6; stroke-linecap: round; stroke-linejoin: round; }
.strip-cell b { font-weight: 600; color: var(--text); }
.strip-cell span { overflow: hidden; text-overflow: ellipsis; }
#live-state i { width: 7px; height: 7px; flex-shrink: 0; border-radius: 50%; background: var(--accent); box-shadow: 0 0 8px var(--accent); animation: strip-pulse 2.2s ease-in-out infinite; }
#live-state b { color: var(--accent); }
.live-strip.off #live-state i, .live-strip.off #live-state b { color: var(--warn); background: var(--warn); box-shadow: 0 0 8px var(--warn); animation: none; }
.live-strip.off #live-state b { background: none; box-shadow: none; }
.live-strip.down #live-state i { background: var(--faint); box-shadow: none; animation: none; }
.live-strip.down #live-state b { color: var(--faint); }
#live-masked.fresh b { color: var(--accent); text-shadow: 0 0 10px rgb(76 201 149 / .6); }
.strip-cell.blocked.hot { background: var(--danger-soft); color: var(--danger); }
.strip-cell.blocked.hot b { color: var(--danger); text-shadow: 0 0 10px rgb(240 117 96 / .7); }
.strip-cell.blocked.hot.fresh { animation: strip-alarm 1.6s ease-in-out infinite; }
@keyframes strip-sweep { to { left: 100%; } }
@keyframes strip-pulse { 0%, 100% { opacity: .55; } 50% { opacity: 1; } }
@keyframes strip-alarm { 0%, 100% { background: var(--danger-soft); } 50% { background: rgb(240 117 96 / .22); } }
@media (max-width: 850px) {
  .live-strip { display: grid; grid-template-columns: repeat(2, minmax(0, 1fr)); height: auto; }
  .strip-cell { min-height: 30px; }
  .strip-cell:nth-child(3) { border-left: 0; }
  .strip-cell:nth-child(n + 3), .strip-reset { border-top: 1px solid var(--line-strong); }
  .strip-reset { grid-column: 1 / -1; justify-content: center; min-height: 30px; border-left: 0; }
}
</style>
