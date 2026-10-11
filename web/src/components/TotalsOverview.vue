<script setup>
import { computed } from "vue";
import { monitor } from "../monitor.js";

const COMPACT = new Intl.NumberFormat("en", { notation: "compact", maximumFractionDigits: 1 });

const masked = computed(() => monitor.totals.new_values ?? 0);
const overhead = computed(() => monitor.totals.token_overhead ?? 0);
const tiles = computed(() => [
  { id: "total-masked", value: masked.value, label: "Unique values protected", detail: "Values replaced with tokens when first seen in a session. Repeats in that session are not counted again." },
  { id: "total-custom", value: monitor.totals.custom ?? 0, label: "Custom word / phrase matches", detail: "Matches of your own Rule Builder rules, counted in every request, history repeats included." },
  { id: "total-forwarded", value: monitor.totals.forwarded ?? 0, label: "Requests sent upstream", detail: "Requests masked and sent to an AI service, whatever it answered. One question is often several requests." },
  { id: "total-blocked", value: monitor.totals.blocked ?? 0, label: "Requests / streams blocked", detail: "Requests or replies stopped because they could not be protected, e.g. an unknown format or an attachment." },
  {
    id: "total-token-overhead",
    value: Math.abs(overhead.value),
    approximate: overhead.value !== 0,
    label: overhead.value < 0 ? "Input tokens saved by masking (net estimate)" : "Input tokens added by masking (net estimate)",
    detail: "Masked minus original input, by a local token estimate. Summed over every request, resent history included.",
  },
]);
</script>

<template>
  <section class="overview" aria-label="Process totals">
    <div v-for="tile in tiles" :key="tile.id" tabindex="0" :aria-describedby="`${tile.id}-detail`">
      <strong :id="tile.id" :title="tile.value.toLocaleString()">{{ tile.approximate ? "≈ " : "" }}{{ COMPACT.format(tile.value) }}</strong>
      <span>{{ tile.label }}</span>
      <p :id="`${tile.id}-detail`" class="overview-detail" role="tooltip">{{ tile.detail }}</p>
    </div>
  </section>
</template>
