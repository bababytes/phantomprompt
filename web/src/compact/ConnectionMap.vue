<script setup>
import { computed, onBeforeUnmount, onMounted, ref } from "vue";
import { terminalActivity, terminalTitle } from "./data.js";
import { GHOST_EYE_RADIUS, GHOST_EYES, GHOST_PATH } from "../ghost.js";
import { closeTerminal, confirmCloseTerminal, folderName, monitor, terminalName, terminalState, time } from "../monitor.js";
import { serviceActive, serviceState } from "../services.js";
import { tell } from "../dialog.js";
import ActivitySpark from "./ActivitySpark.vue";
import SectionLink from "./SectionLink.vue";

const ROW = 70;
const GAP = 10;
const SPARK = 16;
const GATE = 44;
const GATE_HEIGHT = 172;
const GLINT = 2.2;

const middle = ref(null);
const width = ref(220);
const hover = ref(null);
let observer = null;

const count = (n, word) => `${n ?? 0} ${word}${n === 1 ? "" : "s"}`;

async function close(item) {
  if (!(await confirmCloseTerminal(item))) return;
  try {
    await closeTerminal(item.id);
  } catch (failure) {
    await tell({ title: "Could not close the terminal", message: failure.message });
  }
}

function uptime(started) {
  const minutes = Math.max(0, Math.floor((Date.now() - Date.parse(started)) / 60000));
  return minutes < 60 ? `up ${minutes} min` : `up ${Math.floor(minutes / 60)} h ${minutes % 60} min`;
}

const sessions = computed(() => monitor.totals.cli_sessions ?? []);
const terminals = computed(() => sessions.value.map((item) => {
  const state = terminalState(item);
  const dot = item.active ? "live" : state.cls === "status-block" ? "blocked" : "open";
  return { ...item, state, dot, short: state.text.split(" · ")[0], uses: item.services ?? [], activity: terminalActivity(monitor.events, item, SPARK) };
}));

const services = computed(() => {
  const known = monitor.totals.integrations ?? [];
  const extra = [...new Set(sessions.value.flatMap((item) => item.services ?? []))].filter((name) => !known.some((item) => item.service === name)).map((service) => ({ service, protected: true }));
  const all = [...known, ...extra];
  const prompts = all.reduce((sum, item) => sum + (item.prompts ?? 0), 0);
  return all.map((item) => {
    const dot = item.protected ? (item.last_seen && serviceActive(item, sessions.value) ? "live" : "idle") : item.blocked ? "blocked" : "open";
    const state = serviceState(item, sessions.value);
    return { ...item, dot, state, short: state.text.split(" · ").at(-1), share: prompts ? Math.round(((item.prompts ?? 0) / prompts) * 100) : 0 };
  });
});

const rows = computed(() => Math.max(1, terminals.value.length, services.value.length));
const height = computed(() => rows.value * ROW + (rows.value - 1) * GAP);
const center = (index, n) => height.value / 2 + (index - (Math.max(n, 1) - 1) / 2) * (ROW + GAP);
const place = (index, n) => ({ top: `${center(index, n) - ROW / 2}px` });
const paused = computed(() => !monitor.protection || monitor.stopped);
const hubY = computed(() => height.value / 2);
const gateHeight = computed(() => Math.min(height.value - 4, GATE_HEIGHT));

const related = (terminal, service) => terminal.uses.includes(service.service);
const lit = (kind, index) => {
  if (!hover.value) return true;
  if (hover.value.kind === kind) return hover.value.index === index;
  const other = hover.value.kind === "terminal" ? terminals.value[hover.value.index] : services.value[hover.value.index];
  return kind === "terminal" ? related(terminals.value[index], other) : related(other, services.value[index]);
};

const yTerminal = (index) => center(index, terminals.value.length);
const yService = (index) => center(index, services.value.length);
const curve = (x1, y1, x2, y2) => `M${x1} ${y1} C${x1 + (x2 - x1) * 0.55} ${y1} ${x1 + (x2 - x1) * 0.45} ${y2} ${x2} ${y2}`;
const linkState = (item) => {
  if (!terminals.value.some((terminal) => related(terminal, item))) return "waiting";
  return terminals.value.some((terminal) => terminal.active && related(terminal, item)) ? "live" : "idle";
};

const inbound = computed(() => {
  const end = width.value / 2 - GATE / 2 - 2;
  return terminals.value.map((item, index) => ({ key: item.id, state: !item.uses.length ? "waiting" : item.active ? "live" : "idle", lit: lit("terminal", index), focus: Boolean(hover.value) && lit("terminal", index), delay: `${(index * 0.9) % (GLINT * 2)}s`, d: curve(0, yTerminal(index), end, hubY.value) }));
});

const trunk = computed(() => {
  const start = width.value / 2 + GATE / 2 + 2;
  const split = start + (width.value - start) * 0.3;
  const states = services.value.map(linkState);
  const state = states.includes("live") ? "live" : states.includes("idle") ? "idle" : "waiting";
  return { split, state, delay: `${GLINT}s`, d: `M${start} ${hubY.value} L${split} ${hubY.value}` };
});

const outbound = computed(() => services.value.map((item, index) => ({ key: item.service, state: linkState(item), lit: lit("service", index), focus: Boolean(hover.value) && lit("service", index), delay: `${GLINT * 2 + index * 0.3}s`, d: curve(trunk.value.split, hubY.value, width.value, yService(index)) })));

const summary = computed(() => {
  const active = terminals.value.filter((item) => item.active).length;
  const seen = services.value.filter((item) => item.last_seen).length;
  return `${active} active terminal${active === 1 ? "" : "s"} · ${seen} service${seen === 1 ? "" : "s"} seen`;
});

onMounted(() => {
  observer = new ResizeObserver(([entry]) => (width.value = entry.contentRect.width));
  observer.observe(middle.value);
});

onBeforeUnmount(() => observer?.disconnect());
</script>

<template>
  <article class="workspace-card compact-card connection-card" aria-labelledby="connections-map-title">
    <div class="card-head">
      <h2 id="connections-map-title" class="map-title">Terminals<span class="sr-only"> to </span><svg class="title-arrow" viewBox="0 0 30 12" aria-hidden="true"><defs><linearGradient id="title-arrow-ink" x1="0" y1="0" x2="1" y2="0"><stop offset="0" stop-color="#a99bff" /><stop offset="1" stop-color="#4cc995" /></linearGradient></defs><path d="M1.5 6h25M21.5 1.8 26.5 6l-5 4.2" /></svg>AI services</h2>
      <span class="card-meta">{{ summary }}</span>
    </div>
    <div class="map-scroll">
      <div class="map" :class="{ paused }">
        <span class="map-caption">Protected terminals</span>
        <span class="map-caption">{{ paused ? "Protection off" : `Masked here · ${(monitor.totals.new_values ?? 0).toLocaleString()}` }}</span>
        <span class="map-caption">AI services</span>
        <div ref="middle" class="map-middle" :style="{ height: `${height}px` }">
          <svg class="map-links" :viewBox="`0 0 ${width} ${height}`" aria-hidden="true">
            <g class="map-in">
              <template v-for="link in inbound" :key="link.key">
                <path class="map-link" :class="[link.state, { dim: !link.lit, focus: link.focus }]" :d="link.d" />
                <path v-if="link.state === 'live' && !paused" class="map-glint" :class="{ dim: !link.lit }" :d="link.d" pathLength="100" :style="{ animationDelay: link.delay }" />
              </template>
            </g>
            <g class="map-out">
              <template v-if="services.length">
                <path class="map-link" :class="[trunk.state, { focus: Boolean(hover) }]" :d="trunk.d" />
                <path v-if="trunk.state === 'live' && !paused" class="map-glint" :d="trunk.d" pathLength="100" :style="{ animationDelay: trunk.delay }" />
              </template>
              <template v-for="link in outbound" :key="link.key">
                <path class="map-link" :class="[link.state, { dim: !link.lit, focus: link.focus }]" :d="link.d" />
                <path v-if="link.state === 'live' && !paused" class="map-glint" :class="{ dim: !link.lit }" :d="link.d" pathLength="100" :style="{ animationDelay: link.delay }" />
              </template>
            </g>
          </svg>
          <div class="map-gate" :class="{ short: gateHeight < 160 }" :style="{ top: `${hubY - gateHeight / 2}px`, height: `${gateHeight}px` }" :title="paused ? 'Protection is off: traffic is not masked' : 'Every request passes PhantomPrompt and is masked before it reaches an AI service'">
            <span class="gate-word">Phantom</span>
            <svg class="gate-ghost" viewBox="7.6 5.6 16.8 19.6" aria-hidden="true"><path :d="GHOST_PATH" /><rect v-for="eye in GHOST_EYES" :key="eye.x" :x="eye.x" :y="eye.y" :width="eye.width" :height="eye.height" :rx="GHOST_EYE_RADIUS" /></svg>
            <span class="gate-word prompt">Prompt</span>
          </div>
        </div>
        <div class="map-side terminals" :style="{ height: `${height}px` }">
        <div v-for="(item, index) in terminals" :key="item.id" class="node terminal" :class="{ dim: !lit('terminal', index) }" :style="place(index, terminals.length)" :title="`${item.state.text} · started as ${item.label} · ${item.uses.length ? `uses ${item.uses.join(', ')}` : 'no AI request yet'} · last activity ${time(item.last_activity)}`" @mouseenter="hover = { kind: 'terminal', index }" @mouseleave="hover = null">
          <div class="node-line"><i class="dot" :class="item.dot" aria-hidden="true"></i><strong>{{ terminalTitle(item.label) }}</strong><span class="pill" :class="item.dot">{{ item.short }}</span><button v-if="item.pid || item.disconnected" type="button" class="node-close" :aria-label="`Close ${terminalName(item)}`" title="Close this terminal" @click.stop="close(item)">×</button></div>
          <div class="node-meta"><template v-if="item.pid">pid {{ item.pid }} · </template>{{ item.folder ? folderName(item.folder) : "—" }} · {{ uptime(item.started) }}<template v-if="!item.uses.length"> · waiting for first request</template></div>
          <div class="node-line node-stats"><span>{{ count(item.prompts, "prompt") }} · <b class="masked">{{ item.masked ?? 0 }} masked</b><template v-if="item.blocked"> · <b class="status-block">{{ item.blocked }} blocked</b></template></span><ActivitySpark :points="item.activity" :slots="SPARK" /></div>
        </div>
        <div v-if="!terminals.length" class="node terminal empty" :style="place(0, 1)"><span>No protected terminal yet. Run <code>phantom</code> in a terminal.</span></div>
        </div>
        <div class="map-side services" :style="{ height: `${height}px` }">
        <div v-for="(item, index) in services" :key="item.service" class="node service" :class="{ dim: !lit('service', index) }" :style="place(index, services.length)" :title="`${item.state.text} · last seen ${time(item.last_seen)}`" @mouseenter="hover = { kind: 'service', index }" @mouseleave="hover = null">
          <div class="node-line"><i class="dot" :class="item.dot" aria-hidden="true"></i><strong>{{ item.service }}</strong><span class="pill" :class="item.dot">{{ item.short }}</span></div>
          <div class="node-meta">{{ count(item.requests, "request") }} · {{ count(item.prompts, "prompt") }}<template v-if="item.blocked"> · <b class="status-block">{{ item.blocked }} blocked</b></template></div>
          <div class="node-line node-share"><span class="share-bar" aria-hidden="true"><span :style="{ width: `${item.share}%` }"></span></span><small>{{ item.share }}% of prompts</small></div>
        </div>
        <div v-if="!services.length" class="node service empty" :style="place(0, 1)"><span>AI services appear here when their traffic reaches the proxy.</span></div>
        </div>
      </div>
    </div>
    <SectionLink section="section-connect" />
  </article>
</template>

<style scoped>
.map-scroll { display: flex; flex: 1 1 0; flex-direction: column; min-height: 0; margin-top: 10px; padding-bottom: 22px; overflow: auto; }
.map { display: grid; flex: none; grid-template-columns: minmax(0, 1fr) minmax(64px, 30%) minmax(0, 1fr); grid-template-rows: 16px auto; row-gap: 10px; margin: auto 0; }
.map-side { position: relative; grid-row: 2; min-width: 0; }
.map-side.terminals { grid-column: 1; }
.map-side.services { grid-column: 3; }
.map-caption { grid-row: 1; align-self: end; font: 500 9.5px/1 var(--mono); letter-spacing: .14em; text-align: center; text-transform: uppercase; color: var(--faint); }
.map-middle { position: relative; grid-column: 2; grid-row: 2; }
.map-links { position: absolute; inset: 0; width: 100%; height: 100%; overflow: visible; }
.map-link { fill: none; stroke-width: 1.6; transition: d .45s cubic-bezier(.2, .7, .2, 1), opacity .3s; }
.map-in .map-link { stroke: rgb(169 155 255 / .7); }
.map-out .map-link { stroke: rgb(76 201 149 / .75); }
.map-link.idle { opacity: .45; }
.map-link.focus { stroke-width: 2.6; opacity: 1; }
.map-title { display: flex; align-items: center; gap: 9px; }
.title-arrow { width: 28px; height: 12px; flex-shrink: 0; fill: none; stroke: url(#title-arrow-ink); stroke-width: 1.6; stroke-linecap: round; stroke-linejoin: round; }
.map-link.waiting { stroke: rgb(var(--ink-rgb) / .22); stroke-dasharray: 3 5; }
.map-link.dim, .map-glint.dim { opacity: .12; }
.map-glint { fill: none; stroke-width: 2; stroke-linecap: round; stroke-dasharray: 14 200; stroke-dashoffset: 14; animation: map-glint 4.4s cubic-bezier(.45, .05, .55, .95) infinite; }
.map-in .map-glint { stroke: #d6cfff; }
.map-out .map-glint { stroke: #b9f0d6; }
.map.paused .map-link:not(.waiting) { stroke: var(--warn); opacity: .6; }
.map-gate { position: absolute; left: 50%; z-index: 1; display: flex; flex-direction: column; align-items: center; justify-content: center; gap: 8px; width: 44px; overflow: hidden; border: 1px solid rgb(76 201 149 / .3); border-radius: 22px; background: linear-gradient(90deg, rgb(169 155 255 / .05), transparent 45%, transparent 55%, rgb(76 201 149 / .06)), var(--bg-raise); transform: translateX(-50%); }
.gate-word { position: relative; z-index: 1; font: 600 8.5px/1 var(--mono); letter-spacing: .16em; text-transform: uppercase; color: var(--text); writing-mode: vertical-rl; }
.gate-word.prompt { color: var(--brand-violet); }
.map-gate.short .gate-word { display: none; }
.gate-ghost { position: relative; z-index: 1; width: 20px; height: 22px; flex-shrink: 0; fill: var(--accent); }
.gate-ghost rect { fill: var(--bg-raise); }
.map.paused .map-gate { border-color: rgb(227 178 90 / .4); }
.map.paused .gate-ghost { fill: var(--warn); }
@keyframes map-glint { 0% { stroke-dashoffset: 14; opacity: 0; } 8% { opacity: .55; } 42% { opacity: .55; } 50%, 100% { stroke-dashoffset: -100; opacity: 0; } }
.node { position: absolute; left: 0; right: 0; height: 70px; display: grid; align-content: center; gap: 5px; min-width: 0; padding: 0 12px; border: 1px solid var(--line); border-radius: 12px; background: linear-gradient(180deg, rgb(var(--ink-rgb) / .025), transparent), var(--bg-sunk); font-size: 12px; transition: top .45s cubic-bezier(.2, .7, .2, 1), border-color .3s, box-shadow .3s, opacity .3s; }
.node:hover { border-color: var(--line-strong); box-shadow: 0 8px 22px -14px rgb(0 0 0 / .7); }
.node.dim { opacity: .4; }
.node.terminal::after, .node.service::before { content: ""; position: absolute; top: 50%; width: 8px; height: 8px; margin-top: -4px; border: 1px solid var(--line-strong); border-radius: 50%; background: var(--bg-raise); }
.node.terminal::after { right: -5px; }
.node.service::before { left: -5px; }
.node.empty { display: flex; align-items: center; color: var(--faint); line-height: 1.4; }
.node.empty::after, .node.empty::before { display: none; }
.node-line { display: flex; align-items: center; gap: 8px; min-width: 0; }
.node-line strong { overflow: hidden; font-weight: 600; color: var(--text); white-space: nowrap; text-overflow: ellipsis; }
.node-line small { font: 10px/1 var(--mono); color: var(--faint); white-space: nowrap; }
.dot { width: 7px; height: 7px; flex-shrink: 0; border-radius: 50%; background: var(--faint); }
.dot.live { background: var(--accent); box-shadow: 0 0 7px var(--accent); }
.dot.blocked { background: var(--danger); box-shadow: 0 0 7px var(--danger); }
.dot.open { background: var(--warn); box-shadow: 0 0 7px var(--warn); }
.node-close { display: grid; place-items: center; width: 20px; height: 20px; flex-shrink: 0; margin: 0 -4px 0 0; padding: 0; border: 0; border-radius: 50%; background: transparent; color: var(--muted); font-size: 14px; line-height: 1; }
.node-close:hover { background: rgb(var(--ink-rgb) / .08); color: var(--text); }
.pill { margin-left: auto; padding: 2px 7px; border: 1px solid var(--line-strong); border-radius: 999px; font: 600 9px/1.2 var(--mono); letter-spacing: .06em; text-transform: uppercase; color: var(--faint); white-space: nowrap; }
.pill.live { border-color: rgb(76 201 149 / .4); background: var(--accent-soft); color: var(--accent); }
.pill.blocked { border-color: rgb(240 117 96 / .4); background: var(--danger-soft); color: var(--danger); }
.pill.open { border-color: rgb(227 178 90 / .4); background: var(--warn-soft); color: var(--warn); }
.node-meta, .node-stats span { overflow: hidden; font: 11px/1.2 var(--mono); color: var(--muted); white-space: nowrap; text-overflow: ellipsis; }
.node-stats { justify-content: space-between; }
.node b { font-weight: 500; }
.node b.masked { color: var(--secret); }
.node-share small { margin-left: auto; }
.share-bar { flex: 1; height: 3px; border-radius: 2px; background: rgb(var(--ink-rgb) / .07); overflow: hidden; }
.share-bar span { display: block; height: 100%; border-radius: inherit; background: linear-gradient(90deg, rgb(76 201 149 / .4), var(--accent)); box-shadow: 0 0 8px rgb(76 201 149 / .6); }
@media (max-width: 1150px) {
  .map-scroll { flex: none; }
}
@media (max-width: 550px) {
  .map { grid-template-columns: minmax(0, 1fr) 30px minmax(0, 1fr); }
  .map-caption:nth-child(2), .gate-word, .node .spark, .node-line small { display: none; }
  .map-gate { width: 26px; }
  .node { padding: 0 8px; }
}
</style>
