<script setup>
import { computed } from "vue";
import { monitor, time } from "../monitor.js";
import { describe } from "../rules.js";
import { applyRules, workspace } from "../workspace.js";

const stats = computed(() => new Map((monitor.totals.custom_rules ?? []).map((stat) => [stat.pattern, stat])));

function usage(pattern) {
  const stat = stats.value.get(pattern);
  if (!stat?.hits) return "No matches yet";
  return `${stat.hits.toLocaleString()} masked · ${stat.unique.toLocaleString()} different value${stat.unique === 1 ? "" : "s"} · last ${time(stat.last_hit)}`;
}

async function remove(index) {
  workspace.customRules.splice(index, 1);
  await applyRules();
}
</script>

<template>
  <p v-if="!workspace.customRules.length" class="note">No custom rules yet. Create one in the Rule Builder below.</p>
  <ul v-else id="regex-rules" class="rule-list" aria-labelledby="rules-title">
    <li v-for="(rule, index) in workspace.customRules" :key="rule.pattern" class="rule-row">
      <div class="rule-info">
        <strong v-if="rule.name" class="rule-name">{{ rule.name }}</strong>
        <code>{{ rule.pattern }}</code>
        <span class="rule-meaning">{{ describe(rule.pattern) }}<template v-if="rule.secret"> · masked as a secret</template></span>
        <small class="rule-usage">{{ usage(rule.pattern) }}</small>
      </div>
      <button class="text-button" :aria-label="`Remove rule ${rule.name || index + 1}`" :disabled="workspace.working" @click="remove(index)">Remove</button>
    </li>
  </ul>
  <p class="note">Counts cover requests since PhantomPrompt started; the masked values themselves are never stored. Use “Try the active rules” to preview what a rule masks in your own text.</p>
</template>

<style scoped>
.rule-list { margin-top: 0; }
.rule-name { font-size: 14px; font-weight: 600; }
.rule-row { display: flex; align-items: flex-start; justify-content: space-between; gap: 12px; }
.rule-row:first-child { border-top: 0; padding-top: 0; }
.rule-info { display: flex; flex-direction: column; gap: 2px; min-width: 0; }
.rule-info code { overflow-wrap: anywhere; }
.rule-meaning { font-size: 13px; }
.rule-usage { font-size: 12px; opacity: .75; }
</style>
