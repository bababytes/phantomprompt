<script setup>
import { computed } from "vue";
import { eventRows } from "../activity.js";
import { SERVICES, blockInfo, kindStyle, maskedValues } from "../events.js";
import { folderName, monitor, time } from "../monitor.js";

const props = defineProps({ limit: { type: Number, default: Infinity } });

const homePath = (path) => path.replace(/^\/Users\/[^/]+(?=\/|$)/, "~");

const rows = computed(() => monitor.events.slice(0, props.limit).flatMap((event) => {
  const masked = maskedValues(event);
  const block = blockInfo(event, monitor.discovery);
  const repeats = event.repeats ? ` ×${event.repeats + 1}` : "";
  return eventRows(event).map((row, index) => {
    const blocked = block && row.key.endsWith(":status") ? block : null;
    const label = blocked && !blocked.affects && event.reason === "sensitive_value_not_masked_yet" ? `${blocked.place[0].toUpperCase()}${blocked.place.slice(1)} stopped${repeats}` : row.label;
    const found = blocked?.kind ? { ...kindStyle(blocked.kind), where: blocked.where } : null;
    return { ...row, label, block: blocked, found, event, first: index === 0, service: SERVICES[event.adapter] ?? "—", project: folderName(event.folder), masked: blocked ? blocked.effect : masked };
  });
}).slice(0, props.limit));
</script>

<template>
  <p v-if="!rows.length" class="activity-empty">Nothing yet: prompts with protected values, blocks and AI service errors show up here.</p>
  <div v-else class="table-scroll activity-table">
    <table>
      <colgroup><col class="col-clock"><col class="col-service"><col class="col-result"><col></colgroup>
      <thead><tr><th>Time</th><th>Service</th><th>Category</th><th>Masked values</th></tr></thead>
      <tbody id="activity-rows">
        <tr v-for="row in rows" :key="row.key" :class="{ first: row.first }">
          <td>{{ time(row.event.time) }}</td>
          <td :title="row.event.folder ? `${row.service} · ${row.event.folder}` : row.service">{{ row.service }}<span v-if="row.project" class="row-project">{{ row.project }}</span></td>
          <td :class="row.cls" :title="row.block?.why || row.label"><span v-if="row.color" class="category" :style="{ '--kind': row.color }">{{ row.label }}</span><template v-else>{{ row.label }}</template></td>
          <td class="kinds-cell" :title="row.masked">
            <span v-for="chip in row.kinds" :key="chip.kind" class="kind-chip" :style="{ '--kind': chip.color }" :title="chip.title">{{ chip.short }} <b>{{ chip.n }}</b></span>
            <span v-for="entry in row.folders" :key="entry.action + entry.folder" class="kind-chip folder" :class="entry.action">{{ homePath(entry.folder) }} {{ entry.action }}<b v-if="entry.n > 1"> ×{{ entry.n }}</b></span>
            <template v-if="row.block">
              <span v-if="row.found" class="kind-chip" :style="{ '--kind': row.found.color }" :title="`${row.block.why || `${row.found.label} found in this request, so it was stopped.`} Technical location: ${row.found.where}`">{{ row.found.short }}</span>
              <span class="kind-chip effect" :class="{ affects: row.block.affects }">{{ row.block.affects ? "AI request not sent" : "no effect on AI answers" }}</span>
            </template>
            <template v-else-if="!row.kinds.length && !row.folders.length">—</template>
          </td>
        </tr>
      </tbody>
    </table>
  </div>
</template>

<style scoped>
.activity-empty { margin: 10px 0 0; font-size: 12px; line-height: 1.5; color: var(--faint); }
.activity-table.table-scroll { max-height: none; margin-top: 10px; }
th, td { padding: 7px 10px; overflow: hidden; text-overflow: ellipsis; }
th:first-child, td:first-child { padding-left: 0; }
.col-clock { width: 84px; }
.col-service { width: 22%; }
.col-result { width: 24%; }
tr.first:not(:first-child) td { border-top: 1px solid var(--line-strong); }
.category { display: inline-flex; align-items: center; gap: 7px; color: var(--text); }
.category::before { content: ""; flex-shrink: 0; width: 7px; height: 7px; border-radius: 2px; background: var(--kind); box-shadow: 0 0 6px var(--kind); }
td.kinds-cell { padding-top: 5px; padding-bottom: 5px; white-space: normal; }
.kind-chip.folder { --kind: var(--accent); font-family: var(--mono); }
.kind-chip.folder.blocked { --kind: var(--danger); }
.kind-chip.effect { --kind: var(--accent); }
.kind-chip.effect.affects { --kind: var(--danger); }
.row-project { margin-left: 8px; font-family: var(--mono); font-size: 11px; color: var(--muted); }
@media (min-width: 851px) {
  table { table-layout: fixed; }
}
@media (max-width: 550px) {
  th:nth-child(2), td:nth-child(2) { display: none; }
  td:nth-child(3) { white-space: normal; }
}
</style>
