<script setup>
import { computed } from "vue";
import { folderTotals } from "../activity.js";
import { monitor } from "../monitor.js";
import ActivityBars from "../components/ActivityBars.vue";
import FormatAlerts from "../components/FormatAlerts.vue";
import LiveStatus from "../components/LiveStatus.vue";
import RecentActivity from "../components/RecentActivity.vue";
import TotalsOverview from "../components/TotalsOverview.vue";
import ConnectionMap from "./ConnectionMap.vue";
import MaskedChart from "./MaskedChart.vue";
import SectionLink from "./SectionLink.vue";

const homePath = (path) => path.replace(/^\/Users\/[^/]+(?=\/|$)/, "~");
const count = (n, word) => `${n} ${word}${n === 1 ? "" : "s"}`;

const reached = computed(() => folderTotals(monitor.events));
const folders = computed(() => [
  ...(monitor.totals.protected_paths ?? []).map((status) => ({ path: status.path, reach: reached.value.get(status.path), mode: status.blocked ? "Blocked" : "Masked", cls: status.blocked ? "blocked" : "masked", detail: status.error || `${count(status.files, "file")} · ${count(status.lines, "line")}${status.skipped ? ` · ${status.skipped} skipped` : ""}`, error: Boolean(status.error) })),
  ...(monitor.totals.trusted_paths ?? []).map((status) => ({ path: status.path, mode: "Trusted", cls: "trusted", detail: status.error ? `${status.error} · stays masked` : `${count(status.files, "file")} · ${count(status.values ?? 0, "value")}`, error: Boolean(status.error) })),
]);
</script>

<template>
  <section id="compact-view-page" class="compact-view" aria-label="Compact view">
    <FormatAlerts />
    <div class="compact-grid">
      <TotalsOverview class="compact-totals" compact />
      <LiveStatus class="compact-strip" reset />
      <div class="compact-side">
        <MaskedChart />
        <article class="workspace-card compact-card compact-folders" aria-labelledby="compact-folders-title">
          <div class="card-head">
            <h2 id="compact-folders-title">Protected folders and files</h2>
            <span class="card-meta">{{ folders.length }} configured</span>
          </div>
          <ul v-if="folders.length" id="compact-folders" class="folder-list card-scroll">
            <li v-for="entry in folders" :key="entry.mode + entry.path">
              <span class="folder-path" :title="entry.path">{{ homePath(entry.path) }}</span>
              <span class="mode" :class="entry.cls">{{ entry.mode }}</span>
              <small :class="{ 'status-warn': entry.error }"><span :title="entry.detail">{{ entry.detail }}</span><template v-if="entry.reach?.masked"> · <b class="reach-masked">AI masked ×{{ entry.reach.masked }}</b></template><template v-if="entry.reach?.blocked"> · <b class="reach-blocked">AI blocked ×{{ entry.reach.blocked }}</b></template></small>
            </li>
          </ul>
          <p v-else class="compact-empty">No protected or trusted folders. Add them on the full page under Rules and Connect.</p>
          <SectionLink section="section-rules" />
        </article>
      </div>
      <div class="compact-main">
        <ConnectionMap />
        <article class="workspace-card compact-card compact-activity" aria-labelledby="compact-activity-title">
          <div class="card-head">
            <h2 id="compact-activity-title">Recent activity</h2>
            <span class="card-meta">{{ count(monitor.events.length, "prompt") }} in history</span>
          </div>
          <RecentActivity class="card-scroll" />
          <SectionLink section="section-history" />
        </article>
      </div>
      <article class="workspace-card compact-card compact-bars" aria-labelledby="bars-title">
        <ActivityBars><h2 id="bars-title">Masked over time</h2></ActivityBars>
        <SectionLink section="section-history" />
      </article>
    </div>
  </section>
</template>

<style scoped>
:root:not([data-theme="light"]) .compact-view { --bg-raise: #151a19; --bg-sunk: #0d1110; --line: #232928; --line-strong: #313837; --faint: #84918b; }
:root:not([data-theme="light"]) .compact-view :deep(.workspace-card) { background: linear-gradient(180deg, rgb(255 255 255 / .035), transparent 140px), var(--bg-raise); box-shadow: inset 0 1px rgb(255 255 255 / .05), 0 18px 40px -28px rgb(0 0 0 / .8); }
.compact-grid { --row: clamp(408px, calc(100vh - 382px), 680px); --top: 360px; display: grid; grid-template-columns: 420px minmax(0, 1fr); gap: 14px; padding-top: 22px; }
.compact-totals { grid-column: 1 / -1; grid-template-columns: repeat(5, minmax(0, 1fr)); gap: 10px; }
.compact-totals > :deep(div) { display: grid; grid-template-columns: minmax(0, 1fr) auto; align-items: center; column-gap: 12px; height: 54px; padding: 0 18px; }
.compact-totals > :deep(div::before) { inset: 4px; }
.compact-totals > :deep(div:last-child:nth-child(odd)) { grid-column: auto; }
.compact-totals :deep(strong) { grid-column: 2; grid-row: 1; font-size: 22px; }
.compact-totals :deep(span) { grid-column: 1; grid-row: 1; max-width: none; margin: 0; overflow: hidden; font-size: 12px; white-space: nowrap; text-overflow: ellipsis; }
.compact-totals :deep(.overview-sources), .compact-totals :deep(.overview-share) { display: none; }
.compact-side, .compact-main { display: flex; flex-direction: column; gap: 14px; min-width: 0; height: var(--row); }
.compact-side > .chart-card, .compact-main > .connection-card { flex: 0 0 var(--top); }
.compact-folders, .compact-activity { flex: 1 1 0; }
.compact-bars, .compact-strip { grid-column: 1 / -1; }
.compact-strip { margin-top: -2px; }
.card-scroll { flex: 1 1 0; min-height: 0; overflow: auto; padding-bottom: 22px; }
.workspace-card .compact-empty { margin: 10px 0 0; font-size: 12px; line-height: 1.5; color: var(--faint); }
.folder-list { display: grid; align-content: start; gap: 6px; margin: 10px 0 0; padding-left: 0; list-style: none; }
.folder-list li { display: grid; grid-template-columns: minmax(0, 1fr) auto; align-items: center; gap: 3px 10px; padding: 7px 10px; border: 1px solid var(--line); border-radius: 10px; background: var(--bg-sunk); }
.folder-path { overflow: hidden; font: 12px/1.3 var(--mono); color: var(--text); white-space: nowrap; text-overflow: ellipsis; }
.folder-list small { grid-column: 1 / -1; overflow: hidden; font: 11px/1.3 var(--mono); color: var(--muted); white-space: nowrap; text-overflow: ellipsis; }
.folder-list b { font-weight: 500; }
.reach-masked { color: var(--accent); }
.reach-blocked { color: var(--danger); }
.mode { padding: 2px 8px; border: 1px solid; border-radius: 999px; font: 600 10px/1.2 var(--mono); letter-spacing: .06em; text-transform: uppercase; }
.mode.masked { border-color: rgb(76 201 149 / .4); background: var(--accent-soft); color: var(--accent); }
.mode.blocked { border-color: rgb(240 117 96 / .4); background: var(--danger-soft); color: var(--danger); }
.mode.trusted { border-color: rgb(227 178 90 / .4); background: var(--warn-soft); color: var(--warn); }
@media (max-height: 820px) {
  .compact-grid { --top: 294px; }
}
@media (max-width: 1150px) {
  .compact-grid { grid-template-columns: minmax(0, 1fr); }
  .compact-side, .compact-main { height: auto; }
  .compact-side > .chart-card, .compact-main > .connection-card, .compact-folders, .compact-activity { flex: none; }
  .compact-main > .connection-card { max-height: 420px; }
  .card-scroll { flex: none; max-height: 260px; }
}
@media (max-width: 850px) {
  .compact-totals { grid-template-columns: repeat(2, minmax(0, 1fr)); }
  .compact-totals > :deep(div:last-child:nth-child(odd)) { grid-column: 1 / -1; }
}
</style>
