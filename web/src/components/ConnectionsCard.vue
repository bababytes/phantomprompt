<script setup>
import { computed } from "vue";
import { monitor, serviceState, time } from "../monitor.js";

const integrations = computed(() => monitor.totals.integrations ?? []);

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
          <tr v-for="item in integrations" :key="item.service">
            <td>{{ item.service }}</td>
            <td>{{ serviceState(item) }}</td>
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
