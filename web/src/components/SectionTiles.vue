<script setup>
import { computed, ref, watch } from "vue";
import { blockedSince } from "../activity.js";
import { layout } from "../layout.js";
import { monitor } from "../monitor.js";
import { workspace } from "../workspace.js";

const ICONS = {
  "section-rules": "M3 4.5h6M12 4.5h1M3 8h1M7 8h6M3 11.5h7M13 11.5h0M9 3v3M4 6.5v3M10 10v3",
  "section-test": "M6.2 2.2h3.6M6.8 2.2v4L3.2 12.4a1 1 0 0 0 .9 1.4h7.8a1 1 0 0 0 .9-1.4L9.2 6.2v-4M4.6 9.8h6.8",
  "section-history": "M8 4.6V8l2.3 1.5M2.6 8a5.4 5.4 0 1 0 1.6-3.8M2.4 2.6v2.6H5",
};

const count = (n, word) => `${n} ${word}${n === 1 ? "" : "s"}`;
const seenAt = ref(0);
let baselined = false;

const prompts = computed(() => monitor.events.filter((event) => event.phase !== "control"));
const latest = (events) => Math.max(0, ...events.map((event) => Date.parse(event.time)).filter(Number.isFinite));

watch([prompts, () => layout.panel, () => layout.compact], ([events, panel, compact]) => {
  if (!monitor.updated) return;
  if (!baselined || (panel === "section-history" && !compact)) seenAt.value = Math.max(seenAt.value, latest(events));
  baselined = true;
}, { immediate: true });

const unseen = computed(() => prompts.value.filter((event) => Date.parse(event.time) > seenAt.value).length);

const rules = computed(() => {
  const types = [...Object.values(workspace.rules), workspace.pesel];
  const folders = workspace.protectedPaths.length;
  const broken = (monitor.totals.protected_paths ?? []).filter((status) => status.error).length;
  return {
    lines: [`${types.filter(Boolean).length} of ${types.length} built-in types on`, `${count(workspace.customRules.length, "custom rule")} · ${count(folders, "protected folder")}`],
    alert: broken ? `${count(broken, "folder")} with a problem` : "",
  };
});

const history = computed(() => {
  const blocked = blockedSince(monitor.events, 0);
  return { lines: [`${count(prompts.value.length, "prompt")} in history`, `${blocked} blocked`], hot: blocked > 0 };
});

const tiles = computed(() => [
  { id: "section-rules", title: "Rules", eyebrow: "What gets masked", lines: rules.value.lines, alert: rules.value.alert },
  { id: "section-test", title: "Test", eyebrow: "Try it safely", lines: ["Local tester, nothing is sent", "Live demo through the proxy"] },
  { id: "section-history", title: "History", eyebrow: "What was protected", lines: history.value.lines, hot: history.value.hot, badge: unseen.value },
]);

function toggle(id) {
  layout.panel = layout.panel === id ? null : id;
}
</script>

<template>
  <div class="section-tiles">
    <button v-for="tile in tiles" :id="`tile-${tile.id}`" :key="tile.id" type="button" class="section-tile" :class="{ open: layout.panel === tile.id }" :aria-expanded="layout.panel === tile.id" :aria-controls="tile.id" @click="toggle(tile.id)">
      <span class="tile-head">
        <svg class="tile-icon" viewBox="0 0 16 16" aria-hidden="true"><path :d="ICONS[tile.id]" /></svg>
        <span class="tile-title">{{ tile.title }}</span>
        <span v-if="tile.badge" class="tile-badge" :aria-label="`${tile.badge} new`">{{ tile.badge > 99 ? "99+" : tile.badge }} new</span>
        <svg class="tile-chevron" viewBox="0 0 16 16" aria-hidden="true"><path d="m4.5 6.5 3.5 3.5 3.5-3.5" /></svg>
      </span>
      <span class="tile-eyebrow">{{ tile.eyebrow }}</span>
      <span v-for="(line, index) in tile.lines" :key="line" class="tile-line" :class="{ hot: tile.hot && index === tile.lines.length - 1 }">{{ line }}</span>
      <span v-if="tile.alert" class="tile-line alert">⚠ {{ tile.alert }}</span>
    </button>
  </div>
</template>

<style scoped>
.section-tiles { display: grid; grid-template-columns: repeat(3, minmax(0, 1fr)); gap: 16px; }
.section-tile { position: relative; isolation: isolate; display: flex; flex-direction: column; align-items: stretch; gap: 6px; margin: 0; padding: 20px 22px 22px; border: 1px solid var(--line); border-radius: 12px; background: var(--bg-sunk); color: var(--text); font: inherit; text-align: left; cursor: pointer; transition: border-color .25s, box-shadow .25s, transform .25s cubic-bezier(.2, .7, .2, 1); }
.section-tile::before { content: ""; position: absolute; inset: 5px; z-index: -1; border: 1px solid var(--line-strong); border-radius: 8px; background: linear-gradient(180deg, rgb(var(--ink-rgb) / .025), transparent 70%), var(--bg-raise); transition: border-color .25s, background .25s; }
.section-tile:hover { border-color: rgb(var(--ink-rgb) / .16); transform: translateY(-2px); box-shadow: 0 18px 36px -22px rgb(0 0 0 / .8); }
.section-tile:focus-visible { outline: 2px solid var(--accent); outline-offset: 3px; }
.section-tile.open { border-color: rgb(76 201 149 / .5); box-shadow: 0 0 0 1px rgb(76 201 149 / .12), 0 18px 40px -24px rgb(76 201 149 / .55); }
.section-tile.open::before { border-color: rgb(76 201 149 / .28); background: linear-gradient(180deg, var(--accent-soft), transparent 75%), var(--bg-raise); }
.section-tile.open::after { content: ""; position: absolute; left: 50%; bottom: -9px; width: 14px; height: 14px; border-right: 1px solid rgb(76 201 149 / .5); border-bottom: 1px solid rgb(76 201 149 / .5); background: var(--bg-sunk); transform: translateX(-50%) rotate(45deg); }
.tile-head { display: flex; align-items: center; gap: 10px; }
.tile-icon { width: 18px; height: 18px; flex-shrink: 0; fill: none; stroke: var(--accent); stroke-width: 1.4; stroke-linecap: round; stroke-linejoin: round; }
.tile-title { font-size: 17px; font-weight: 500; letter-spacing: -.2px; }
.tile-badge { padding: 1px 7px; border: 1px solid rgb(76 201 149 / .35); border-radius: 999px; background: var(--accent-soft); color: var(--accent); font: 600 10px/16px var(--mono); }
.tile-chevron { width: 16px; height: 16px; margin-left: auto; fill: none; stroke: var(--muted); stroke-width: 1.6; stroke-linecap: round; stroke-linejoin: round; transition: transform .3s cubic-bezier(.2, .7, .2, 1), stroke .25s; }
.section-tile.open .tile-chevron { stroke: var(--accent); transform: rotate(180deg); }
.tile-eyebrow { margin: 2px 0 6px; font: 500 10px/1.2 var(--mono); letter-spacing: .14em; text-transform: uppercase; color: var(--faint); }
.tile-line { overflow: hidden; font-size: 12.5px; line-height: 1.35; color: var(--muted); white-space: nowrap; text-overflow: ellipsis; }
.tile-line.hot { color: var(--danger); }
.tile-line.alert { color: var(--warn); }
@media (max-width: 850px) {
  .section-tiles { gap: 10px; }
  .section-tile { padding: 16px 16px 18px; }
  .tile-eyebrow { display: none; }
}
@media (max-width: 550px) {
  .section-tile { padding: 14px 12px 16px; }
  .tile-title { font-size: 15px; }
  .tile-line { white-space: normal; }
  .tile-line + .tile-line { display: none; }
  .tile-badge { position: absolute; top: 8px; right: 8px; }
  .tile-chevron { display: none; }
}
</style>
