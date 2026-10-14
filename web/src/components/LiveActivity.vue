<script setup>
import { computed } from "vue";
import { kindTotals } from "../activity.js";
import { kindStyle } from "../events.js";
import { BAR_SECONDS, showSection } from "../layout.js";
import { FULL_VIEW_POLL_MS, monitor } from "../monitor.js";
import ActivityBars from "./ActivityBars.vue";
import RecentActivity from "./RecentActivity.vue";

const RECENT = 5;
const sizes = BAR_SECONDS.filter((seconds) => seconds * 1000 > FULL_VIEW_POLL_MS);

const kinds = computed(() => kindTotals(monitor.events).map((row) => ({ ...row, ...kindStyle(row.kind) })));
const total = computed(() => kinds.value.reduce((sum, row) => sum + row.count, 0));
const share = (count) => `${Math.round((count / total.value) * 100)}%`;
const prompts = computed(() => `${monitor.events.length} prompt${monitor.events.length === 1 ? "" : "s"} in history`);
</script>

<template>
  <article id="live-activity" class="workspace-card live-card" aria-labelledby="live-activity-title">
    <div class="section-heading">
      <div><h2 id="live-activity-title">Live activity</h2></div>
      <div><button id="more-details" type="button" class="secondary compact history-link" @click="showSection('section-history')">More details ↓</button></div>
    </div>
    <div class="live-part">
      <div class="live-part-head"><h3 id="live-types-title">Masked by data type</h3><span class="card-meta" title="Unique values, counted from the prompt history kept in memory">{{ total.toLocaleString() }} unique values</span></div>
      <ul v-if="kinds.length" id="live-types" class="live-types" aria-labelledby="live-types-title">
        <li v-for="row in kinds" :key="row.kind" class="kind-chip" :style="{ '--kind': row.color }">{{ row.label }} <b>{{ row.count.toLocaleString() }}</b><small>{{ share(row.count) }}</small></li>
      </ul>
      <p v-else class="live-empty">Each type shows up here as soon as a prompt contains a value to mask.</p>
    </div>
    <div class="live-part">
      <div class="live-part-head"><h3>Recent activity</h3><span class="card-meta">{{ prompts }}</span></div>
      <RecentActivity :limit="RECENT" />
    </div>
    <div class="live-part">
      <ActivityBars :sizes="sizes"><h3 id="bars-title">Masked over time</h3></ActivityBars>
    </div>
  </article>
</template>

<style scoped>
.live-card { position: relative; margin: 0 0 16px; }
.live-part { margin-top: 22px; }
.live-part-head { display: flex; flex-wrap: wrap; justify-content: space-between; align-items: baseline; gap: 6px 12px; }
.live-card h3 { margin: 0; font-size: 14px; font-weight: 500; color: var(--text); }
.live-types { display: flex; flex-wrap: wrap; gap: 8px; margin: 12px 0 0; padding: 0; list-style: none; }
.live-types .kind-chip { gap: 7px; margin: 0; padding: 4px 12px 4px 10px; font-size: 12.5px; line-height: 18px; }
.live-types .kind-chip::before { width: 7px; height: 7px; border-radius: 2px; box-shadow: 0 0 6px var(--kind); }
.live-types .kind-chip b { font-size: 12.5px; line-height: 18px; }
.live-types .kind-chip small { font: 10.5px/18px var(--mono); color: var(--faint); }
.workspace-card .live-empty { margin: 10px 0 0; font-size: 12px; line-height: 1.5; color: var(--faint); }
</style>
