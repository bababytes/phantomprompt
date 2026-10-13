<script setup>
import { computed } from "vue";
import { monitor, tokenShare } from "../monitor.js";

defineProps({ compact: { type: Boolean, default: false } });

const COMPACT = new Intl.NumberFormat("en", { notation: "compact", maximumFractionDigits: 1 });
const SIGNED = new Intl.NumberFormat("en", { notation: "compact", maximumFractionDigits: 1, signDisplay: "exceptZero" });

const masked = computed(() => monitor.totals.new_values ?? 0);
const typed = computed(() => monitor.totals.typed_values ?? 0);
const context = computed(() => monitor.totals.context_values ?? 0);
const overhead = computed(() => monitor.totals.token_overhead ?? 0);
const input = computed(() => monitor.totals.input_tokens ?? 0);
const usageOverhead = computed(() => monitor.totals.usage_overhead ?? 0);

const tiles = computed(() => [
  {
    id: "total-masked",
    value: masked.value,
    label: "Unique values protected",
    short: "Values protected",
    sources: masked.value ? `${typed.value} typed · ${Math.max(masked.value - typed.value - context.value, 0)} found by AI · ${context.value} tool context` : "",
    detail: "First-seen values: typed by you, found by the AI in files and commands, or added by the tool itself.",
  },
  { id: "total-custom", value: monitor.totals.custom ?? 0, label: "Custom word / phrase matches", short: "Custom matches", detail: "Matches of your own Rule Builder rules, counted in every request, history repeats included." },
  { id: "total-prompts", value: monitor.totals.prompts_sent ?? 0, label: "Prompts sent", short: "Prompts sent", detail: `Your prompts to an AI service. They took ${(monitor.totals.forwarded ?? 0).toLocaleString()} AI request(s): helper calls, tool steps, retries.` },
  { id: "total-blocked", value: monitor.totals.blocked ?? 0, label: "Requests / streams blocked", short: "Blocked", detail: "Stopped because they could not be protected (e.g. an attachment) or failed (e.g. AI service unreachable)." },
  {
    id: "total-token-overhead",
    value: -overhead.value || 0,
    signed: true,
    approximate: overhead.value !== 0,
    share: tokenShare(usageOverhead.value, input.value),
    extra: usageOverhead.value > 0,
    label: "Token change from masking (net estimate)",
    short: "Token change (est.)",
    detail: "+ saved, − extra vs. no masking. Local estimate, per request incl. resent history. % of all AI input tokens.",
  },
]);
</script>

<template>
  <section class="overview" aria-label="Process totals">
    <div v-for="tile in tiles" :key="tile.id" tabindex="0" :aria-describedby="`${tile.id}-detail`">
      <strong :id="tile.id" :title="tile.value.toLocaleString()">{{ tile.approximate ? "≈ " : "" }}{{ (tile.signed ? SIGNED : COMPACT).format(tile.value).replace("-", "−") }}<small v-if="tile.share" class="overview-share" :class="{ extra: tile.extra }" :title="`of ${input.toLocaleString()} input tokens reported by the AI services`">{{ tile.share }}</small></strong>
      <span :title="compact && tile.sources ? tile.sources : undefined">{{ compact ? tile.short : tile.label }}</span>
      <small v-if="tile.sources" class="overview-sources" :title="tile.sources">{{ tile.sources }}</small>
      <p :id="`${tile.id}-detail`" class="overview-detail" role="tooltip">{{ tile.detail }}</p>
    </div>
  </section>
</template>
