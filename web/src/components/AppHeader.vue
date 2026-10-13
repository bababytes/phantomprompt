<script setup>
import { computed, onBeforeUnmount, onMounted, reactive, ref, watch } from "vue";
import { GHOST_EYE_RADIUS, GHOST_EYES, GHOST_PATH } from "../ghost.js";
import { SECTIONS, layout, showSection } from "../layout.js";
import { monitor, setProtection, stopApp } from "../monitor.js";

const current = ref(SECTIONS[0].id);
const scrolled = ref(false);
const nav = ref(null);
const pill = reactive({ x: 0, width: 0 });
let observer = null;
let resize = null;

const engine = computed(() => {
  if (monitor.stopped || monitor.error || !monitor.updated) return { state: "down", text: "Engine not responding: this page is not live." };
  if (monitor.totals.second_instance) return { state: "second", text: "Second copy of PhantomPrompt: protected terminals report to the other copy, not to this panel." };
  return { state: "live", text: "Engine running: this is the main panel, protected terminals report here." };
});

async function toggle(event) {
  if (!(await setProtection(event.target.checked))) event.target.checked = monitor.protection;
}

function go(event, id) {
  current.value = id;
  if (!layout.compact) return;
  event.preventDefault();
  showSection(id);
}

function placePill() {
  const link = nav.value?.querySelector(".active");
  if (link) Object.assign(pill, { x: link.offsetLeft, width: link.offsetWidth });
}

function spotlight(event) {
  const box = event.currentTarget.getBoundingClientRect();
  event.currentTarget.style.setProperty("--spot-x", `${event.clientX - box.left}px`);
  event.currentTarget.style.setProperty("--spot-y", `${event.clientY - box.top}px`);
}

function onScroll() {
  scrolled.value = window.scrollY > 8;
}

watch(current, placePill, { flush: "post" });

onMounted(() => {
  observer = new IntersectionObserver((entries) => {
    const visible = entries.filter((entry) => entry.isIntersecting);
    if (visible.length) current.value = visible[0].target.id;
  }, { rootMargin: "-30% 0px -60% 0px" });
  for (const section of SECTIONS) {
    const element = document.getElementById(section.id);
    if (element) observer.observe(element);
  }
  resize = new ResizeObserver(placePill);
  resize.observe(nav.value);
  placePill();
  onScroll();
  window.addEventListener("scroll", onScroll, { passive: true });
});

onBeforeUnmount(() => {
  observer?.disconnect();
  resize?.disconnect();
  window.removeEventListener("scroll", onScroll);
});
</script>

<template>
  <header class="topbar" :class="{ scrolled }">
    <div class="topbar-card" :class="{ compact: layout.compact }" @pointermove="spotlight">
      <a class="brand" href="#section-status" @click="current = SECTIONS[0].id">
        <svg class="brand-mark" viewBox="0 0 32 32" aria-hidden="true">
          <defs>
            <linearGradient id="brand-tile" x1=".3" y1="0" x2=".7" y2="1"><stop offset="0" stop-color="#3f986f" /><stop offset="1" stop-color="#13352a" /></linearGradient>
            <linearGradient id="brand-ghost" x1="0" y1="0" x2="0" y2="1"><stop offset="0" stop-color="#f6f8f3" /><stop offset="1" stop-color="#d3e4d8" /></linearGradient>
          </defs>
          <rect width="32" height="32" rx="7.2" fill="url(#brand-tile)" />
          <path :d="GHOST_PATH" fill="url(#brand-ghost)" />
          <rect v-for="eye in GHOST_EYES" :key="eye.x" :x="eye.x" :y="eye.y" :width="eye.width" :height="eye.height" :rx="GHOST_EYE_RADIUS" fill="#17352d" />
        </svg>
        <span class="brand-text">
          <span class="brand-name"><span>Phantom</span><span>Prompt</span></span>
          <span class="brand-tagline">Local · secrets stay home</span>
        </span>
      </a>
      <nav ref="nav" class="segmented section-nav" :class="{ idle: layout.compact }" aria-label="Page sections" :style="{ '--pill-x': `${pill.x}px`, '--pill-width': `${pill.width}px` }">
        <a v-for="section in SECTIONS" :key="section.id" :href="`#${section.id}`" :class="{ active: !layout.compact && current === section.id }" :aria-current="!layout.compact && current === section.id ? 'location' : undefined" :title="layout.compact ? `Open ${section.label} on the full page` : undefined" @click="go($event, section.id)">{{ section.label }}</a>
      </nav>
      <div class="header-controls">
        <button id="compact-view" type="button" class="secondary compact view-toggle" :aria-pressed="layout.compact" :title="layout.compact ? 'Back to the full page' : 'Compact view: every status on one screen'" @click="layout.compact = !layout.compact"><svg viewBox="0 0 16 16" aria-hidden="true"><rect x="1.75" y="1.75" width="5" height="5" rx="1.2" /><rect x="9.25" y="1.75" width="5" height="5" rx="1.2" /><rect x="1.75" y="9.25" width="5" height="5" rx="1.2" /><rect x="9.25" y="9.25" width="5" height="5" rx="1.2" /></svg><span>Compact</span></button>
        <label class="switch" :class="{ live: engine.state === 'live' && monitor.protection, down: engine.state === 'down' }" :title="engine.text"><input id="protection" type="checkbox" :checked="monitor.protection" aria-describedby="engine-state" @change="toggle"> <span id="protection-state" :class="{ off: !monitor.protection }">{{ monitor.protection ? "Protection on" : "Protection OFF" }}</span></label>
        <span id="engine-state" class="sr-only">{{ engine.text }}</span>
        <button id="stop-app" class="secondary compact" title="Stop PhantomPrompt" :disabled="monitor.stopped" @click="stopApp">Stop</button>
        <button v-if="layout.compact" id="theme-toggle" type="button" class="secondary compact theme-toggle" :aria-pressed="layout.light" aria-label="Light mode" :title="layout.light ? 'Switch to dark mode' : 'Switch to light mode'" @click="layout.light = !layout.light">
          <svg v-if="layout.light" viewBox="0 0 16 16" aria-hidden="true"><path d="M13.4 9.7A5.6 5.6 0 0 1 6.3 2.6a5.6 5.6 0 1 0 7.1 7.1Z" /></svg>
          <svg v-else viewBox="0 0 16 16" aria-hidden="true"><circle cx="8" cy="8" r="2.9" /><path d="M8 1.6v1.5M8 12.9v1.5M1.6 8h1.5M12.9 8h1.5M3.5 3.5l1 1M11.5 11.5l1 1M3.5 12.5l1-1M11.5 4.5l1-1" /></svg>
        </button>
      </div>
    </div>
  </header>
</template>

<style scoped>
.topbar { position: sticky; top: 0; z-index: 10; padding-top: var(--bar-gap); }
.topbar::before { content: ""; position: absolute; inset: 0 -40px -28px; z-index: -1; pointer-events: none; background: linear-gradient(var(--bg) calc(100% - 40px), transparent); opacity: 0; transition: opacity .3s; }
.topbar.scrolled::before { opacity: 1; }

.topbar-card { --spot-x: -400px; --spot-y: -400px; position: relative; display: grid; grid-template-columns: 1fr auto 1fr; align-items: center; gap: 16px; padding: 12px 14px 12px 16px; border-radius: 22px; background: radial-gradient(220px circle at var(--spot-x) var(--spot-y), rgb(255 255 255 / .05), transparent 70%), radial-gradient(420px 160px at 0 0, rgb(255 255 255 / .035), transparent 70%), linear-gradient(180deg, rgb(255 255 255 / .04), rgb(255 255 255 / .01)), rgb(17 19 19 / .92); -webkit-backdrop-filter: blur(14px) saturate(1.1); backdrop-filter: blur(14px) saturate(1.1); box-shadow: inset 0 1px rgb(255 255 255 / .07), 0 18px 40px -26px rgb(0 0 0 / .9); transition: box-shadow .3s; }
.topbar-card::before { content: ""; position: absolute; inset: 0; padding: 1px; border-radius: inherit; pointer-events: none; background: linear-gradient(105deg, rgb(255 255 255 / .2), rgb(255 255 255 / .07) 30%, rgb(255 255 255 / .05) 70%, rgb(255 255 255 / .16)); -webkit-mask: linear-gradient(#000 0 0) content-box, linear-gradient(#000 0 0); -webkit-mask-composite: xor; mask-composite: exclude; }
.scrolled .topbar-card { box-shadow: inset 0 1px rgb(255 255 255 / .07), 0 22px 44px -18px rgb(0 0 0 / .95); }

.brand { display: flex; align-items: center; gap: 12px; min-width: 0; color: inherit; text-decoration: none; border-radius: 10px; }
.brand-mark { width: 34px; height: 34px; flex-shrink: 0; }
.brand-text { display: grid; gap: 4px; min-width: 0; }
.brand-name { overflow: hidden; font-size: 19px; font-weight: 600; line-height: 1.1; letter-spacing: -.4px; white-space: nowrap; text-overflow: ellipsis; }
.brand-name > :last-child { background: linear-gradient(90deg, var(--brand-violet) 40%, #f3f1ff 50%, var(--brand-violet) 60%) 100% 0 / 300% 100%; -webkit-background-clip: text; background-clip: text; color: transparent; animation: shimmer 9s ease-in-out infinite; }
.brand-tagline { overflow: hidden; font: 500 10px/1.2 var(--mono); letter-spacing: .14em; text-transform: uppercase; color: var(--faint); white-space: nowrap; text-overflow: ellipsis; }

.section-nav { position: relative; }
.section-nav::before { content: ""; position: absolute; top: 3px; bottom: 3px; left: var(--pill-x); width: var(--pill-width); border-radius: 999px; background: rgb(255 255 255 / .07); box-shadow: inset 0 0 0 1px rgb(255 255 255 / .12), 0 0 18px -8px rgb(255 255 255 / .35); transition: left .4s cubic-bezier(.2, .8, .2, 1), width .4s cubic-bezier(.2, .8, .2, 1), opacity .3s; }
.section-nav.idle::before { opacity: 0; }
.section-nav > a { position: relative; z-index: 1; }
.section-nav > a.active { background: none; color: var(--text); }

.header-controls { grid-column: -2; justify-self: end; display: flex; align-items: center; gap: 14px; }
.header-controls .switch { white-space: nowrap; }
.view-toggle { display: inline-flex; align-items: center; gap: 7px; }
.view-toggle svg { width: 13px; height: 13px; fill: none; stroke: currentColor; stroke-width: 1.4; }
.header-controls .theme-toggle { display: grid; place-items: center; width: 31px; height: 31px; padding: 0; border-radius: 50%; }
.theme-toggle svg { width: 15px; height: 15px; fill: none; stroke: currentColor; stroke-width: 1.5; stroke-linecap: round; stroke-linejoin: round; }
:root[data-theme="light"] .topbar-card { background: radial-gradient(220px circle at var(--spot-x) var(--spot-y), rgb(0 0 0 / .03), transparent 70%), rgb(255 255 255 / .92); box-shadow: 0 1px 2px rgb(0 0 0 / .05), 0 18px 40px -28px rgb(0 0 0 / .35); }
:root[data-theme="light"] .topbar-card::before { background: linear-gradient(105deg, rgb(0 0 0 / .12), rgb(0 0 0 / .05) 30%, rgb(0 0 0 / .04) 70%, rgb(0 0 0 / .1)); }
:root[data-theme="light"] .brand-name > :last-child { background-image: linear-gradient(90deg, var(--brand-violet) 40%, #b9adff 50%, var(--brand-violet) 60%); }
.view-toggle[aria-pressed=true] { border-color: rgb(76 201 149 / .5); background: var(--accent-soft); color: var(--accent); }
.switch.live input[type=checkbox]::after { animation: engine-pulse 2.8s ease-in-out infinite; }
.switch.down input[type=checkbox]::after { background: var(--faint); }


@keyframes engine-pulse { 0%, 100% { opacity: .6; box-shadow: 0 0 0 0 transparent; } 50% { opacity: 1; box-shadow: 0 0 8px 1px rgb(76 201 149 / .65); } }
@keyframes shimmer { 0%, 55% { background-position: 100% 0; } 85%, 100% { background-position: 0 0; } }

@media (max-width: 1100px) {
  .header-controls .view-toggle { padding: 7px 9px; }
  .view-toggle span { display: none; }
}

@media (max-width: 1000px) {
  .topbar-card.compact #protection-state { display: none; }
}

@media (max-width: 850px) {
  .topbar-card { grid-template-columns: 1fr auto; row-gap: 10px; }
  .section-nav { grid-column: 1 / -1; grid-row: 2; justify-self: start; }
}

@media (max-width: 550px) {
  .topbar-card { padding: 10px 12px; gap: 12px; }
  .brand { gap: 10px; }
  .brand-mark { width: 30px; height: 30px; }
  .brand-name { font-size: 17px; }
  .brand-tagline { font-size: 9px; letter-spacing: .1em; }
  .header-controls { gap: 8px; }
}
</style>
