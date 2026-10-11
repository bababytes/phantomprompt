<script setup>
import { onBeforeUnmount, onMounted, ref, useId } from "vue";
import { monitor, setProtection, stopApp } from "../monitor.js";

const COMMANDS = [
  { command: "phantom", purpose: "Open a protected terminal" },
  { command: "phantom claude --continue", purpose: "Resume Claude Code under protection" },
  { command: "phantom open", purpose: "Start PhantomPrompt and open this panel" },
  { command: "phantom stop", purpose: "Stop PhantomPrompt" },
  { command: "phantom kill", purpose: "Stop everything, protected terminals too" },
];

const open = ref(false);
const copied = ref("");
const root = ref(null);
const toggleButton = ref(null);
const panelId = useId();

async function toggleProtection(event) {
  if (!(await setProtection(event.target.checked))) event.target.checked = monitor.protection;
}

async function copy(command) {
  await navigator.clipboard.writeText(command);
  copied.value = command;
  setTimeout(() => {
    if (copied.value === command) copied.value = "";
  }, 2000);
}

function openTerminal() {
  open.value = false;
  document.getElementById("section-connect")?.scrollIntoView({ behavior: "smooth", block: "start" });
  const button = document.getElementById("open-terminal");
  if (button && button.getAttribute("aria-expanded") !== "true") button.click();
}

function close(event) {
  if (!open.value) return;
  if (event.type === "keydown" && event.key !== "Escape") return;
  if (event.type === "pointerdown" && root.value?.contains(event.target)) return;
  open.value = false;
  if (event.type === "keydown") toggleButton.value?.focus();
}

onMounted(() => {
  document.addEventListener("pointerdown", close);
  document.addEventListener("keydown", close);
});

onBeforeUnmount(() => {
  document.removeEventListener("pointerdown", close);
  document.removeEventListener("keydown", close);
});
</script>

<template>
  <div ref="root" class="quick-actions">
    <section v-if="open" :id="panelId" class="quick-panel" aria-label="Quick actions">
      <p class="eyebrow">In the app</p>
      <div class="quick-row">
        <label class="switch"><input type="checkbox" :checked="monitor.protection" :disabled="monitor.stopped" @change="toggleProtection"> <span :class="{ off: !monitor.protection }">{{ monitor.protection ? "Protection on" : "Protection OFF" }}</span></label>
      </div>
      <div class="quick-row">
        <button type="button" class="secondary compact" :disabled="monitor.stopped" @click="openTerminal">Open protected terminal</button>
        <button type="button" class="secondary compact" :disabled="monitor.stopped" @click="stopApp">Stop</button>
      </div>
      <p class="eyebrow">Terminal commands</p>
      <ul class="quick-commands">
        <li v-for="item in COMMANDS" :key="item.command">
          <div><code>{{ item.command }}</code><span>{{ item.purpose }}</span></div>
          <button type="button" class="text-button" :aria-label="`Copy ${item.command}`" @click="copy(item.command)">{{ copied === item.command ? "Copied" : "Copy" }}</button>
        </li>
      </ul>
    </section>
    <button ref="toggleButton" type="button" class="quick-toggle" :aria-expanded="open" :aria-controls="panelId" aria-label="Quick actions" title="Quick actions" @click="open = !open">
      <svg viewBox="0 0 24 24" aria-hidden="true"><path d="M4 7h16M4 12h16M4 17h10" /></svg>
    </button>
  </div>
</template>

<style scoped>
.quick-actions { position: fixed; right: 24px; bottom: 24px; z-index: 20; display: flex; flex-direction: column; align-items: flex-end; gap: 12px; }
.quick-toggle { display: grid; place-items: center; width: 48px; height: 48px; padding: 0; border: 1px solid var(--line-strong); border-radius: 50%; background: rgb(14 18 17 / .9); color: var(--text); box-shadow: 0 16px 40px -14px rgb(0 0 0 / .9); -webkit-backdrop-filter: blur(12px); backdrop-filter: blur(12px); }
.quick-toggle:hover, .quick-toggle[aria-expanded="true"] { border-color: rgb(76 201 149 / .5); color: var(--accent); }
.quick-toggle svg { width: 20px; height: 20px; fill: none; stroke: currentColor; stroke-width: 1.8; stroke-linecap: round; }
.quick-panel { width: min(380px, calc(100vw - 48px)); max-height: calc(100vh - 120px); overflow-y: auto; padding: 18px; border: 1px solid var(--line-strong); border-radius: 18px; background: rgb(14 18 17 / .96); box-shadow: 0 24px 60px -20px rgb(0 0 0 / .9); -webkit-backdrop-filter: blur(14px); backdrop-filter: blur(14px); }
.quick-panel .eyebrow { margin: 0 0 10px; }
.quick-panel .eyebrow:not(:first-child) { margin-top: 18px; }
.quick-row { display: flex; flex-wrap: wrap; align-items: center; gap: 8px; margin-bottom: 10px; }
.quick-row .switch span.off { color: var(--warn); }
.quick-commands { display: grid; gap: 6px; margin: 0; padding: 0; list-style: none; }
.quick-commands li { display: flex; align-items: center; justify-content: space-between; gap: 12px; padding: 8px 10px; border: 1px solid var(--line); border-radius: 12px; background: var(--bg-sunk); }
.quick-commands li > div { display: grid; gap: 3px; min-width: 0; }
.quick-commands code { justify-self: start; border: 0; background: none; padding: 0; color: var(--text); }
.quick-commands span { font-size: 12px; color: var(--muted); }
.quick-commands .text-button { flex-shrink: 0; }
@media (max-width: 550px) {
  .quick-actions { right: 16px; bottom: 16px; }
}
</style>
