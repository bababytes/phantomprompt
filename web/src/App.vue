<script setup>
import { computed, onMounted, ref } from "vue";
import AppHeader from "./components/AppHeader.vue";
import TotalsOverview from "./components/TotalsOverview.vue";
import LiveActivity from "./components/LiveActivity.vue";
import LiveStatus from "./components/LiveStatus.vue";
import ProtectedTerminals from "./components/ProtectedTerminals.vue";
import ConnectionsCard from "./components/ConnectionsCard.vue";
import RulesPanel from "./components/RulesPanel.vue";
import ProtectedPaths from "./components/ProtectedPaths.vue";
import RuleBuilder from "./components/RuleBuilder.vue";
import LocalTester from "./components/LocalTester.vue";
import PromptHistory from "./components/PromptHistory.vue";
import SectionTiles from "./components/SectionTiles.vue";
import DemoDialog from "./components/DemoDialog.vue";
import FormatAlerts from "./components/FormatAlerts.vue";
import ProtectionPipeline from "./components/ProtectionPipeline.vue";
import ClaudeSetup from "./components/ClaudeSetup.vue";
import QuickActions from "./components/QuickActions.vue";
import AppDialog from "./components/AppDialog.vue";
import CompactView from "./compact/CompactView.vue";
import { author } from "./config.js";
import { GHOST_EYE_RADIUS, GHOST_EYES, GHOST_PATH } from "./ghost.js";
import { layout } from "./layout.js";
import { monitor, startPolling } from "./monitor.js";
import { watchTabAlerts } from "./tabAlert.js";
import { initialize } from "./workspace.js";

const demo = ref(null);
const firstScreen = ref(null);

const state = computed(() => {
  if (monitor.stopped) return "stopped";
  return monitor.protection ? "on" : "off";
});
const terminals = computed(() => (monitor.totals.cli_sessions ?? []).filter((session) => session.active).length);
const headline = computed(() => ({ on: "Protection active", off: "Protection OFF", stopped: "Stopped" })[state.value]);

function fitFirstScreen() {
  const element = firstScreen.value;
  if (element?.offsetParent) element.style.setProperty("--first-screen-top", `${Math.round(element.getBoundingClientRect().top + window.scrollY)}px`);
}

onMounted(() => {
  new ResizeObserver(fitFirstScreen).observe(document.body);
  initialize();
  startPolling();
  watchTabAlerts();
});
</script>

<template>
  <main>
    <AppHeader />
    <div v-if="!monitor.protection" id="protection-banner" role="alert">⚠ Protection is OFF — traffic to AI is NOT masked. Your data leaves unmasked. Turn it back on when done.</div>
    <div v-if="monitor.totals.second_instance && !monitor.stopped" id="second-instance-banner" role="alert">This is a second copy of PhantomPrompt. Your protected terminals report to the other copy, so they don't show here. Click Stop to close this copy, then open the panel with the app icon or <code>phantom open</code>.</div>
    <div v-if="monitor.stopped" id="stopped-banner" role="alert">PhantomPrompt has been stopped. This page is no longer live. Start it again with the PhantomPrompt app icon or <code>phantom open</code>.</div>
    <CompactView v-if="layout.compact" />
    <section v-show="!layout.compact" id="section-status" class="page-section" aria-labelledby="section-status-label">
      <p id="section-status-label" class="section-label">Status</p>
      <FormatAlerts v-if="!layout.compact" />
      <div ref="firstScreen" class="first-screen">
        <div class="hero">
          <div class="hero-copy">
            <p class="eyebrow">Local privacy proxy</p>
            <h1>Keep private words<br>close to home.</h1>
            <p class="lead">Sensitive values are replaced with tokens before a prompt leaves this machine and restored in the reply. Mappings live only in memory.</p>
            <p class="hero-status" :class="state" role="status" aria-live="polite"><svg class="status-ghost" viewBox="7.6 5.6 16.8 19.6" aria-hidden="true"><path :d="GHOST_PATH" /><rect v-for="eye in GHOST_EYES" :key="eye.x" :x="eye.x" :y="eye.y" :width="eye.width" :height="eye.height" :rx="GHOST_EYE_RADIUS" /></svg><strong>{{ headline }}</strong><span>· {{ terminals }} protected terminal{{ terminals === 1 ? "" : "s" }}</span></p>
          </div>
          <div class="hero-stage">
            <div class="hero-screen"><ProtectionPipeline /></div>
          </div>
        </div>
        <TotalsOverview />
        <LiveStatus v-if="!layout.compact" class="first-screen-strip" />
        <p class="note">Counters include proxy traffic handled by this process, not local previews. One phrase match counts as one interception. Totals reset on restart and with Clear history.</p>
      </div>
    </section>
    <section v-show="!layout.compact" id="section-live" class="page-section" aria-labelledby="section-live-label">
      <p id="section-live-label" class="section-label">Right now</p>
      <LiveActivity v-if="!layout.compact" />
    </section>
    <section v-show="!layout.compact" id="section-connect" class="page-section" aria-labelledby="section-connect-label">
      <p id="section-connect-label" class="section-label">Connect</p>
      <ClaudeSetup />
      <ProtectedTerminals />
      <ConnectionsCard />
      <p class="note">Coding CLIs are protected only when started through <code>phantom</code> (for example <code>phantom claude</code>) or the <strong>Open protected terminal</strong> button.</p>
    </section>
    <section v-show="!layout.compact" id="section-more" class="page-section" aria-labelledby="section-more-label">
      <p id="section-more-label" class="section-label">Rules · Test · History</p>
      <SectionTiles />
      <div v-show="layout.panel === 'section-rules'" id="section-rules" class="more-panel" role="region" aria-labelledby="tile-section-rules">
        <RulesPanel />
        <ProtectedPaths />
        <RuleBuilder />
      </div>
      <div v-show="layout.panel === 'section-test'" id="section-test" class="more-panel" role="region" aria-labelledby="tile-section-test">
        <div class="lab-grid">
          <LocalTester />
          <article class="workspace-card">
            <p class="eyebrow">LIVE DEMO</p>
            <h2>See what leaves your machine</h2>
            <p>Send a sample prompt through the real local HTTPS proxy and compare what the AI receives with what you typed.</p>
            <button id="open-demo" class="primary corner-action" @click="demo.open()">Open live demo ↗</button>
          </article>
        </div>
      </div>
      <div v-show="layout.panel === 'section-history'" id="section-history" class="more-panel" role="region" aria-labelledby="tile-section-history">
        <PromptHistory />
      </div>
    </section>
    <DemoDialog ref="demo" />
    <QuickActions />
    <AppDialog />
    <footer><span>{{ author }}</span><span>Runs only on this Mac</span></footer>
  </main>
</template>
