<script setup>
import { computed } from "vue";
import { monitor, time } from "../monitor.js";
import { serviceState } from "../services.js";

const integrations = computed(() => monitor.totals.integrations ?? []);
const rows = computed(() => integrations.value.map((item) => ({ ...item, state: serviceState(item, monitor.totals.cli_sessions ?? []) })));

const connectionStatus = computed(() => {
  const active = integrations.value.filter((item) => item.last_seen).map((item) => item.service);
  return { text: active.length ? `Traffic seen from: ${active.join(", ")}.` : "Services appear here when their traffic reaches the proxy.", kind: "" };
});

</script>

<template>
  <section class="workspace-card connections-card" aria-labelledby="connections-title">
    <div class="section-heading">
      <div><p class="eyebrow">AI CONNECTIONS</p><h2 id="connections-title">Detected AI services</h2></div>
    </div>
    <p id="connections-status" role="status" aria-live="polite" :class="connectionStatus.kind">{{ connectionStatus.text }}</p>
    <div class="table-scroll">
      <table>
        <thead><tr><th>Service</th><th>Status</th><th>Prompts</th><th>Blocked</th><th>Last seen</th></tr></thead>
        <tbody id="integration-rows">
          <tr v-for="item in rows" :key="item.service">
            <td>{{ item.service }}</td>
            <td :class="item.state.cls">{{ item.state.text }}</td>
            <td :title="`${item.requests} AI requests`">{{ item.prompts ?? 0 }}</td>
            <td>{{ item.blocked }}</td>
            <td>{{ time(item.last_seen) }}</td>
          </tr>
        </tbody>
      </table>
    </div>
    <p class="note">Protected: Claude Code, Codex and GitHub Copilot CLI started through <code>phantom</code>. Other AI services reached from a protected terminal are recognised and blocked; ordinary traffic (package registries, git, documentation) passes unchanged.</p>
  </section>
</template>
