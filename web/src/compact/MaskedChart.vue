<script setup>
import { computed, ref } from "vue";
import { kindStyle } from "../events.js";
import { kindTotals } from "../activity.js";
import { monitor } from "../monitor.js";
import SectionLink from "./SectionLink.vue";

const RADIUS = 74;
const CIRCLE = 2 * Math.PI * RADIUS;
const GAP = 3;
const TICKS = Array.from({ length: 60 }, (_, i) => {
  const angle = (i / 60) * 2 * Math.PI;
  const major = i % 5 === 0;
  const [from, to] = major ? [85, 93] : [88, 91];
  return { major, x1: 100 + from * Math.sin(angle), y1: 100 - from * Math.cos(angle), x2: 100 + to * Math.sin(angle), y2: 100 - to * Math.cos(angle) };
});

const active = ref("");
const kinds = computed(() => kindTotals(monitor.events).map((row) => ({ ...row, ...kindStyle(row.kind) })));
const total = computed(() => kinds.value.reduce((sum, row) => sum + row.count, 0));
const largest = computed(() => Math.max(1, ...kinds.value.map((row) => row.count)));

const segments = computed(() => {
  let start = 0;
  return kinds.value.map((row) => {
    const length = (row.count / total.value) * CIRCLE;
    const segment = { ...row, dash: kinds.value.length > 1 ? Math.max(length - GAP, 1) : CIRCLE, offset: -start };
    start += length;
    return segment;
  });
});

const share = (count) => (total.value ? `${Math.round((count / total.value) * 100)}%` : "");
const summary = computed(() => (total.value ? `${total.value} unique values masked: ${kinds.value.map((row) => `${row.label} ${row.count}`).join(", ")}` : "No values masked yet"));
</script>

<template>
  <article class="workspace-card compact-card chart-card" aria-labelledby="chart-title">
    <div class="card-head">
      <h2 id="chart-title">Masked by data type</h2>
      <span class="card-meta" title="Unique values, counted from the prompt history kept in memory">unique values</span>
    </div>
    <div class="chart-body">
      <div class="ring" :class="{ empty: !total, paused: !monitor.protection || monitor.stopped }">
        <svg viewBox="0 0 200 200" role="img" :aria-label="summary">
          <defs>
            <radialGradient id="ring-core"><stop offset="0" stop-color="#4cc995" stop-opacity=".16" /><stop offset="1" stop-color="#4cc995" stop-opacity="0" /></radialGradient>
          </defs>
          <circle cx="100" cy="100" r="62" fill="url(#ring-core)" />
          <line v-for="(tick, i) in TICKS" :key="i" :class="['ring-tick', { major: tick.major }]" :x1="tick.x1" :y1="tick.y1" :x2="tick.x2" :y2="tick.y2" />
          <circle class="ring-orbit" cx="100" cy="100" r="97" />
          <circle class="ring-scan" cx="100" cy="100" r="97" />
          <circle class="ring-track" cx="100" cy="100" :r="RADIUS" />
          <circle v-for="segment in segments" :key="segment.kind" class="ring-segment" :class="{ dim: active && active !== segment.kind }" cx="100" cy="100" :r="RADIUS" :stroke-dasharray="`${segment.dash} ${CIRCLE}`" :stroke-dashoffset="segment.offset" :style="{ '--kind': segment.color }" @mouseenter="active = segment.kind" @mouseleave="active = ''" />
          <circle class="ring-inner" cx="100" cy="100" r="60" />
        </svg>
        <div class="ring-center" aria-hidden="true">
          <strong>{{ total.toLocaleString() }}</strong>
          <span>{{ total ? "values masked" : "nothing yet" }}</span>
        </div>
      </div>
      <ol v-if="kinds.length" class="legend" aria-label="Masked values by type">
        <li v-for="row in kinds" :key="row.kind" :class="{ dim: active && active !== row.kind }" :style="{ '--kind': row.color }" @mouseenter="active = row.kind" @mouseleave="active = ''">
          <span class="legend-label"><i aria-hidden="true"></i>{{ row.label }}</span>
          <span class="legend-count">{{ row.count.toLocaleString() }}<small>{{ share(row.count) }}</small></span>
          <span class="legend-bar" aria-hidden="true"><span :style="{ width: `${(row.count / largest) * 100}%` }"></span></span>
        </li>
      </ol>
      <p v-else class="chart-empty">Each type shows up here as soon as a prompt contains a value to mask.</p>
    </div>
    <SectionLink section="section-history" />
  </article>
</template>

<style scoped>
.chart-body { display: grid; flex: 1 1 auto; grid-template-columns: 168px minmax(0, 1fr); align-items: center; gap: 16px; min-height: 0; margin-top: 12px; padding-bottom: 16px; }
.ring { position: relative; width: 168px; aspect-ratio: 1; }
.ring svg { display: block; width: 100%; height: 100%; overflow: visible; }
.ring-tick { stroke: rgb(var(--ink-rgb) / .1); stroke-width: 1; }
.ring-tick.major { stroke: rgb(var(--ink-rgb) / .22); }
.ring-orbit { fill: none; stroke: rgb(var(--ink-rgb) / .12); stroke-width: 1; stroke-dasharray: 1 5; transform-box: fill-box; transform-origin: center; animation: ring-spin 60s linear infinite; }
.ring-scan { fill: none; stroke: var(--accent); stroke-width: 1.6; stroke-linecap: round; stroke-dasharray: 46 564; opacity: .7; filter: drop-shadow(0 0 4px rgb(76 201 149 / .8)); transform-box: fill-box; transform-origin: center; animation: ring-spin 7s linear infinite; }
.ring.empty .ring-scan { stroke: var(--faint); filter: none; opacity: .4; }
.ring.paused .ring-orbit, .ring.paused .ring-scan, .ring.paused .ring-inner { animation-play-state: paused; }
.ring.paused .ring-scan { stroke: var(--warn); filter: none; opacity: .5; }
.ring-track { fill: none; stroke: rgb(var(--ink-rgb) / .06); stroke-width: 12; }
.ring-segment { fill: none; stroke: var(--kind); stroke-width: 12; transform: rotate(-90deg); transform-origin: 100px 100px; filter: drop-shadow(0 0 5px var(--kind)); transition: stroke-dasharray .8s cubic-bezier(.2, .7, .2, 1), stroke-dashoffset .8s cubic-bezier(.2, .7, .2, 1), opacity .2s; cursor: default; }
.ring-segment.dim, .legend li.dim { opacity: .25; }
.ring-inner { fill: none; stroke: rgb(var(--ink-rgb) / .14); stroke-width: 1; stroke-dasharray: 2 6; transform-box: fill-box; transform-origin: center; animation: ring-spin 40s linear infinite reverse; }
.ring-center { position: absolute; inset: 0; display: grid; place-content: center; justify-items: center; gap: 4px; pointer-events: none; }
.ring-center strong { font-size: 36px; font-weight: 300; line-height: 1; letter-spacing: -.02em; color: var(--text); font-variant-numeric: tabular-nums; text-shadow: 0 0 18px rgb(76 201 149 / .35); }
.ring-center span { font: 500 9px/1 var(--mono); letter-spacing: .16em; text-transform: uppercase; color: var(--muted); }
.legend { display: grid; gap: 6px; margin: 0; padding: 0; list-style: none; }
.legend li { display: grid; grid-template-columns: minmax(0, 1fr) auto; gap: 4px 10px; transition: opacity .2s; }
.legend-label { display: flex; align-items: center; gap: 8px; min-width: 0; overflow: hidden; font-size: 12px; color: var(--text); white-space: nowrap; text-overflow: ellipsis; }
.legend-label i { width: 7px; height: 7px; flex-shrink: 0; border-radius: 2px; background: var(--kind); box-shadow: 0 0 6px var(--kind); }
.legend-count { font: 500 12px/1 var(--mono); color: var(--text); font-variant-numeric: tabular-nums; }
.legend-count small { margin-left: 6px; font-size: 10px; color: var(--faint); }
.legend-bar { grid-column: 1 / -1; height: 2px; border-radius: 2px; background: rgb(var(--ink-rgb) / .06); overflow: hidden; }
.legend-bar span { display: block; height: 100%; border-radius: inherit; background: linear-gradient(90deg, transparent, var(--kind)); box-shadow: 0 0 8px var(--kind); transition: width .8s cubic-bezier(.2, .7, .2, 1); }
.workspace-card .chart-empty { margin: 0; font-size: 12px; line-height: 1.5; color: var(--faint); }
:root[data-theme="light"] .ring-segment { filter: drop-shadow(0 0 2px var(--kind)); }
@keyframes ring-spin { to { transform: rotate(360deg); } }
@media (max-width: 1150px) {
  .chart-body { flex: none; grid-template-columns: 132px minmax(0, 1fr); gap: 14px; height: 226px; }
  .ring { width: 132px; }
  .ring-center strong { font-size: 28px; }
  .ring-center span { font-size: 8px; letter-spacing: .08em; }
}
@media (max-width: 550px) {
  .chart-body { grid-template-columns: 1fr; justify-items: center; height: auto; }
  .legend { width: 100%; }
}
</style>
