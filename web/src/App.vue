<script setup>
import { computed, onMounted, ref } from "vue";
import AppHeader from "./components/AppHeader.vue";
import TotalsOverview from "./components/TotalsOverview.vue";
import ProtectedTerminals from "./components/ProtectedTerminals.vue";
import ConnectionsCard from "./components/ConnectionsCard.vue";
import RulesPanel from "./components/RulesPanel.vue";
import ProtectedPaths from "./components/ProtectedPaths.vue";
import RuleBuilder from "./components/RuleBuilder.vue";
import LocalTester from "./components/LocalTester.vue";
import PromptHistory from "./components/PromptHistory.vue";
import DemoDialog from "./components/DemoDialog.vue";
import FormatAlerts from "./components/FormatAlerts.vue";
import ProtectionPipeline from "./components/ProtectionPipeline.vue";
import ClaudeSetup from "./components/ClaudeSetup.vue";
import QuickActions from "./components/QuickActions.vue";
import { author } from "./config.js";
import { GHOST_EYE_RADIUS, GHOST_EYES, GHOST_PATH } from "./ghost.js";
import { monitor, startPolling } from "./monitor.js";
import { initialize } from "./workspace.js";

const demo = ref(null);

const state = computed(() => {
  if (monitor.stopped) return "stopped";
  return monitor.protection ? "on" : "off";
});
const terminals = computed(() => (monitor.totals.cli_sessions ?? []).filter((session) => session.active).length);
const headline = computed(() => ({ on: "Protection active", off: "Protection OFF", stopped: "Stopped" })[state.value]);

onMounted(() => {
  initialize();
  startPolling();
});
</script>

<template>
  <main>
    <AppHeader />
    <div v-if="!monitor.protection" id="protection-banner" role="alert">⚠ Protection is OFF — traffic to AI is NOT masked. Your data leaves unmasked. Turn it back on when done.</div>
    <div v-if="monitor.stopped" id="stopped-banner" role="alert">PhantomPrompt has been stopped. This page is no longer live. Start it again with the PhantomPrompt app icon or <code>phantom open</code>.</div>
    <section id="section-status" class="page-section" aria-labelledby="section-status-label">
      <p id="section-status-label" class="section-label">Status</p>
      <FormatAlerts />
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
      <p class="note">Counters include proxy traffic handled by this process, not local previews. One phrase match counts as one interception. Totals reset on restart.</p>
    </section>
    <section id="section-connect" class="page-section" aria-labelledby="section-connect-label">
      <p id="section-connect-label" class="section-label">Connect</p>
      <ClaudeSetup />
      <ProtectedTerminals />
      <ConnectionsCard />
      <p class="note">Coding CLIs are protected only when started through <code>phantom</code> (for example <code>phantom claude</code>) or the <strong>Open protected terminal</strong> button.</p>
    </section>
    <section id="section-rules" class="page-section" aria-labelledby="section-rules-label">
      <p id="section-rules-label" class="section-label">Rules · what gets masked</p>
      <RulesPanel />
      <ProtectedPaths />
      <RuleBuilder />
    </section>
    <section id="section-test" class="page-section" aria-labelledby="section-test-label">
      <p id="section-test-label" class="section-label">Test</p>
      <div class="lab-grid">
        <LocalTester />
        <article class="workspace-card">
          <p class="eyebrow">LIVE DEMO</p>
          <h2>See what leaves your machine</h2>
          <p>Send a sample prompt through the real local HTTPS proxy and compare what the AI receives with what you typed.</p>
          <button id="open-demo" class="primary corner-action" @click="demo.open()">Open live demo ↗</button>
        </article>
      </div>
    </section>
    <section id="section-history" class="page-section" aria-labelledby="section-history-label">
      <p id="section-history-label" class="section-label">History</p>
      <PromptHistory />
    </section>
    <DemoDialog ref="demo" />
    <QuickActions />
    <footer><span>{{ author }}</span><span>Runs only on this Mac</span></footer>
  </main>
</template>
