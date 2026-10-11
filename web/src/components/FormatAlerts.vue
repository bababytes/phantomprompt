<script setup>
import { computed, ref } from "vue";
import { monitor, time } from "../monitor.js";

const SERVICES = { "claude-code": "Claude Code", "copilot-cli": "GitHub Copilot CLI", "codex-cli": "Codex CLI" };
const REASONS = {
  unsupported_content: "unknown request format",
  unsupported_content_or_masking_limit: "unknown request format or masking limit",
  unsupported_response_content: "unknown reply format",
  unsupported_upstream_response: "unknown reply format",
  compressed_upstream_response_not_supported: "compressed reply",
};
const STORAGE = "phantomprompt.dismissedFormatAlerts";

function load() {
  try {
    return JSON.parse(localStorage.getItem(STORAGE) ?? "{}");
  } catch {
    return {};
  }
}

const dismissed = ref(load());
const key = (alert) => `${alert.adapter}|${alert.route}|${alert.reason}`;
const alerts = computed(() => (monitor.totals.format_alerts ?? []).filter((alert) => (dismissed.value[key(alert)] ?? 0) < alert.count));

function dismiss() {
  const next = { ...dismissed.value };
  for (const alert of alerts.value) next[key(alert)] = alert.count;
  dismissed.value = next;
  try {
    localStorage.setItem(STORAGE, JSON.stringify(next));
  } catch {}
}
</script>

<template>
  <section v-if="alerts.length" class="format-alert" role="alert">
    <div class="format-alert-head">
      <strong>Possible format change — PhantomPrompt needs an update</strong>
      <button class="text-button" @click="dismiss">Dismiss</button>
    </div>
    <p>An AI tool sent requests or replies that PhantomPrompt does not recognise. Unrecognised requests are blocked, so nothing leaves unmasked; unrecognised replies are shown without restoring your values. This usually means the tool was updated.</p>
    <ul>
      <li v-for="alert in alerts" :key="key(alert)">
        <span class="format-alert-service">{{ SERVICES[alert.adapter] ?? (alert.adapter || "PhantomPrompt") }}</span>
        <span>{{ alert.route }}</span>
        <span>{{ REASONS[alert.reason] ?? alert.reason }} ×{{ alert.count }}</span>
        <span>last {{ time(alert.last) }}</span>
        <span v-if="alert.detail" class="format-alert-detail">{{ alert.detail }}</span>
      </li>
    </ul>
  </section>
</template>

<style scoped>
.format-alert { margin: 16px 0 0; padding: 14px 18px; border: 1px solid rgb(227 178 90 / .45); border-radius: 12px; background: var(--warn-soft); color: var(--warn); }
.format-alert-head { display: flex; justify-content: space-between; align-items: center; gap: 12px; }
.format-alert-head strong { font-size: 13px; font-weight: 600; }
.format-alert-head .text-button { color: var(--warn); }
.format-alert p { margin: 8px 0 10px; font-size: 12px; line-height: 1.6; color: var(--text); opacity: .85; }
.format-alert ul { list-style: none; margin: 0; padding: 0; display: grid; gap: 6px; }
.format-alert li { display: flex; flex-wrap: wrap; gap: 6px 14px; font-family: var(--mono); font-size: 11px; color: var(--text); }
.format-alert-service { color: var(--warn); }
.format-alert-detail { color: var(--muted); }
</style>
