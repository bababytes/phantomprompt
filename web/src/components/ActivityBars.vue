<script setup>
import { computed, onBeforeUnmount, onMounted, ref } from "vue";
import { kindStyle } from "../events.js";
import { BAR_SECONDS, layout } from "../layout.js";
import { time } from "../monitor.js";
import { timeline } from "../timeline.js";

const props = defineProps({ sizes: { type: Array, default: () => BAR_SECONDS } });

const SLOTS = 30;
const duration = (seconds) => (seconds >= 60 ? `${seconds / 60} min` : `${seconds} s`);
const barSeconds = computed(() => (props.sizes.includes(layout.barSeconds) ? layout.barSeconds : props.sizes[0]));
const BUCKET = computed(() => barSeconds.value * 1000);
const WINDOW = computed(() => BUCKET.value * SLOTS);
const TICK_STEP = { 1: 10, 4: 30, 10: 60, 60: 600 };
const ticks = computed(() => {
  const span = barSeconds.value * SLOTS;
  const step = TICK_STEP[barSeconds.value];
  return Array.from({ length: Math.floor(span / step) }, (_, i) => span - i * step).map((seconds) => ({ left: `${100 - (seconds / span) * 100}%`, label: `−${duration(seconds)}` }));
});

const now = ref(Date.now());
const hovered = ref(null);
let timer = null;

const at = (ms) => `${100 - ((now.value - ms) / WINDOW.value) * 100}%`;

const buckets = computed(() => {
  const groups = new Map();
  for (const sample of timeline.samples) {
    if (now.value - sample.time > WINDOW.value) continue;
    const key = Math.floor(sample.time / BUCKET.value);
    const group = groups.get(key) ?? { hits: 0, blocked: 0, kinds: new Map() };
    group.hits += 1;
    group.blocked += sample.blocked;
    for (const [kind, n] of Object.entries(sample.kinds)) group.kinds.set(kind, (group.kinds.get(kind) ?? 0) + n);
    groups.set(key, group);
  }
  const last = Math.floor(now.value / BUCKET.value);
  return Array.from({ length: SLOTS + 1 }, (_, i) => last - SLOTS + i).map((key) => {
    const group = groups.get(key);
    const parts = group ? [...group.kinds].filter(([, n]) => n > 0).map(([kind, n]) => ({ kind, n, ...kindStyle(kind) })) : [];
    return { key, center: key * BUCKET.value + BUCKET.value / 2, hits: group?.hits ?? 0, blocked: group?.blocked ?? 0, total: parts.reduce((sum, part) => sum + part.n, 0), parts };
  });
});

const peak = computed(() => Math.max(1, ...buckets.value.map((bucket) => bucket.total)));

const bars = computed(() => buckets.value.map((bucket) => {
  const values = bucket.parts.map((part) => `${part.label} ${part.n}`).join(", ");
  const range = `${time(bucket.key * BUCKET.value)}–${time((bucket.key + 1) * BUCKET.value)}`;
  const title = bucket.hits ? `${range}${values ? ` · ${values}` : ""}${bucket.blocked ? ` · ${bucket.blocked} blocked or failed` : ""}` : undefined;
  return { ...bucket, left: at(bucket.center), range, title, parts: bucket.parts.map((part) => ({ ...part, height: `${(part.n / peak.value) * 100}%` })) };
}));

const tip = computed(() => {
  const bar = bars.value.find((item) => item.key === hovered.value && item.hits);
  if (!bar) return null;
  const position = parseFloat(bar.left);
  return { ...bar, edge: position > 80 ? "end" : position < 12 ? "start" : "" };
});

const summary = computed(() => {
  const values = buckets.value.reduce((sum, bucket) => sum + bucket.total, 0);
  return `${values} values masked in the last ${duration(barSeconds.value * SLOTS)}, peak ${peak.value} values in ${duration(barSeconds.value)}`;
});

onMounted(() => {
  timer = setInterval(() => (now.value = Date.now()), 1000);
});

onBeforeUnmount(() => clearInterval(timer));
</script>

<template>
  <div class="activity-bars">
    <div class="bars-head">
      <slot />
      <div class="bars-controls">
        <span class="card-meta"><b class="bars-blocked-key">▮</b> blocked or failed</span>
        <div class="segmented bar-size" role="group" aria-label="Time per bar">
          <button v-for="size in sizes" :key="size" type="button" :class="{ active: barSeconds === size }" :aria-pressed="barSeconds === size" :title="`${duration(size)} per bar`" @click="layout.barSeconds = size">{{ duration(size) }}</button>
        </div>
      </div>
    </div>
    <div class="bars" role="img" :aria-label="summary">
      <span class="bars-peak" aria-hidden="true">{{ peak }}</span>
      <div class="bars-track">
        <span v-for="tick in ticks.slice(1)" :key="tick.left" class="bars-tick" :style="{ left: tick.left }" aria-hidden="true"></span>
        <template v-for="bar in bars" :key="bar.key">
        <span v-if="bar.hits" class="bar" :class="{ blocked: bar.blocked, hovered: hovered === bar.key }" :style="{ left: bar.left }" :aria-label="bar.title" @mouseenter="hovered = bar.key" @mouseleave="hovered = null">
          <span v-for="part in bar.parts" :key="part.kind" class="bar-part" :style="{ height: part.height, '--kind': part.color }"></span>
        </span>
        </template>
      </div>
      <span class="bars-now" aria-hidden="true"><span>now</span></span>
      <div v-if="tip" class="bar-tip" :class="tip.edge" :style="{ left: tip.left }" role="tooltip">
        <strong>{{ tip.range }}</strong>
        <span v-for="part in tip.parts" :key="part.kind" class="tip-row" :style="{ '--kind': part.color }"><i aria-hidden="true"></i>{{ part.label }}<b>{{ part.n }}</b></span>
        <span v-if="tip.blocked" class="tip-row blocked"><i aria-hidden="true"></i>Blocked or failed<b>{{ tip.blocked }}</b></span>
        <span v-if="!tip.parts.length && !tip.blocked" class="tip-row">No new values</span>
      </div>
    </div>
    <div class="bars-axis" aria-hidden="true">
      <span v-for="tick in ticks" :key="tick.left" :style="{ left: tick.left }">{{ tick.label }}</span>
    </div>
  </div>
</template>

<style scoped>
.bars-head { display: flex; flex-wrap: wrap; justify-content: space-between; align-items: center; gap: 8px 12px; }
.bars-blocked-key { font-weight: 400; color: var(--danger); }
.bars-controls { display: flex; align-items: center; gap: 12px; }
.bar-size { padding: 2px; }
.bar-size > button { padding: 2px 9px; font-size: 10.5px; line-height: 1.5; }
.bars { position: relative; height: 50px; margin: 20px 0 0 26px; border-bottom: 1px solid var(--line-strong); }
.bars-track { position: absolute; inset: 0; overflow: hidden; background: linear-gradient(rgb(var(--ink-rgb) / .05) 1px, transparent 1px) 0 0 / 100% 50%; }
.bars-peak { position: absolute; top: -2px; left: -26px; font: 10px/1 var(--mono); color: var(--faint); }
.bars-tick { position: absolute; top: 0; bottom: 0; border-left: 1px dashed rgb(var(--ink-rgb) / .08); }
.bar { position: absolute; bottom: 0; display: flex; flex-direction: column-reverse; width: calc(100% / 31 - 8px); height: 100%; border-radius: 3px 3px 0 0; transform: translateX(-50%); transition: left 1s linear; }
.bar:hover, .bar.hovered { background: rgb(var(--ink-rgb) / .08); }
.bar-tip { position: absolute; bottom: calc(100% + 12px); z-index: 6; display: grid; gap: 5px; min-width: 170px; padding: 9px 11px; border: 1px solid var(--line-strong); border-radius: 10px; background: var(--bg-raise); box-shadow: 0 14px 30px -16px rgb(0 0 0 / .8); font-size: 11.5px; color: var(--text); transform: translateX(-50%); transition: left 1s linear; pointer-events: none; }
.bar-tip.start { transform: translateX(-12px); }
.bar-tip.end { transform: translateX(calc(-100% + 12px)); }
.bar-tip strong { font: 500 10.5px/1.2 var(--mono); color: var(--muted); }
.tip-row { display: grid; grid-template-columns: 8px minmax(0, 1fr) auto; align-items: center; gap: 8px; white-space: nowrap; }
.tip-row i { width: 7px; height: 7px; border-radius: 2px; background: var(--kind, var(--faint)); }
.tip-row b { font: 500 11px/1 var(--mono); }
.tip-row.blocked { color: var(--danger); }
.tip-row.blocked i { background: var(--danger); }
.bar-part { flex-shrink: 0; background: linear-gradient(180deg, var(--kind), color-mix(in srgb, var(--kind) 30%, transparent)); box-shadow: 0 0 10px -2px var(--kind); transition: height .8s cubic-bezier(.2, .7, .2, 1); }
.bar-part:last-child { border-radius: 3px 3px 0 0; }
.bar.blocked::after { content: ""; display: block; flex: 0 0 auto; height: 7px; border-radius: 3px 3px 0 0; background: repeating-linear-gradient(135deg, rgb(240 117 96 / .9) 0 3px, rgb(240 117 96 / .4) 3px 6px); box-shadow: 0 0 8px -1px var(--danger); }
.bar.blocked:not(:has(.bar-part))::after { height: 34%; }
.bars-now { position: absolute; top: -4px; right: 0; bottom: -1px; width: 2px; border-radius: 1px; background: var(--accent); box-shadow: 0 0 10px 1px rgb(76 201 149 / .6); animation: bars-now 2.4s ease-in-out infinite; }
.bars-axis { position: relative; height: 12px; margin: 6px 40px 0 26px; font: 10px/1 var(--mono); color: var(--faint); }
.bars-axis > span { position: absolute; top: 0; transform: translateX(-50%); white-space: nowrap; }
.bars-axis > span:first-child { transform: none; }
.bars-now > span { position: absolute; top: -14px; right: -1px; font: 10px/1 var(--mono); color: var(--accent); }
@keyframes bars-now { 0%, 100% { opacity: .55; } 50% { opacity: 1; } }
@media (max-width: 550px) {
  .bar { width: calc(100% / 31 - 3px); }
}
</style>
