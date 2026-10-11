<script setup>
import { computed, reactive } from "vue";
import { folderName, monitor, time } from "../monitor.js";
import { valueRows } from "../valueRows.js";

const props = defineProps({ query: { type: String, default: "" }, services: { type: Object, required: true } });

const COLUMNS = [
  { key: "folder", label: "Folder", hint: "Project folder the AI tool worked in. — means no folder, e.g. the demo." },
  { key: "label", label: "Protected value", hint: "Value type or field name. The small tag says where it came from: typed by you, found by AI, a rule or a protected folder." },
  { key: "count", label: "Counter", hint: "Different values protected, each counted once per session. For a folder: how many times the AI reached for it." },
  { key: "services", label: "Services", hint: "AI tools that sent these values." },
  { key: "last", label: "Last seen", hint: "When a value of this kind was last protected." },
];
const sort = reactive({ key: "folder", dir: "asc" });

const rows = computed(() => {
  const needle = props.query.trim().toLowerCase();
  const list = valueRows(monitor.events, props.services)
    .filter((row) => !needle || `${row.folder} ${row.label} ${row.kind} ${row.services}`.toLowerCase().includes(needle));
  const value = { folder: (row) => row.folder || "\uffff", label: (row) => row.label, count: (row) => row.count, services: (row) => row.services, last: (row) => row.last };
  const compare = (key, a, b) => {
    const x = value[key](a);
    const y = value[key](b);
    return typeof x === "string" ? x.localeCompare(y) : x - y;
  };
  return list.sort((a, b) => {
    const order = sort.dir === "asc" ? compare(sort.key, a, b) : -compare(sort.key, a, b);
    if (order !== 0) return order;
    return sort.key === "folder" ? -compare("count", a, b) : compare("folder", a, b);
  });
});

function sortBy(key) {
  if (sort.key === key) sort.dir = sort.dir === "asc" ? "desc" : "asc";
  else Object.assign(sort, { key, dir: key === "label" || key === "services" || key === "folder" ? "asc" : "desc" });
}
</script>

<template>
  <div class="table-scroll">
    <table>
      <thead>
        <tr>
          <th v-for="column in COLUMNS" :key="column.key" :aria-sort="sort.key === column.key ? (sort.dir === 'asc' ? 'ascending' : 'descending') : 'none'">
            <button class="sort-button" :title="column.hint" @click="sortBy(column.key)">{{ column.label }}<span aria-hidden="true">{{ sort.key === column.key ? (sort.dir === "asc" ? " ▲" : " ▼") : "" }}</span></button>
          </th>
        </tr>
      </thead>
      <tbody id="value-rows">
        <tr v-for="row in rows" :key="row.key">
          <td class="value-folder" :title="row.folder">{{ row.folder ? folderName(row.folder) : "—" }}</td>
          <td><span class="value-label">{{ row.label }}</span><span class="value-kind">{{ row.kind }}</span></td>
          <td>{{ row.count }}</td>
          <td>{{ row.services }}</td>
          <td>{{ time(row.last) }}</td>
        </tr>
        <tr v-if="!rows.length"><td colspan="5" class="value-empty">{{ query.trim() ? "No protected value matches the search." : "No protected values yet." }}</td></tr>
      </tbody>
    </table>
  </div>
</template>

<style scoped>
.value-label { font-family: var(--mono); color: var(--text); }
.value-folder { font-family: var(--mono); color: var(--muted); max-width: 300px; overflow: hidden; text-overflow: ellipsis; }
.value-kind { margin-left: 10px; font-family: var(--mono); font-size: 10px; letter-spacing: .08em; color: var(--faint); text-transform: uppercase; }
.value-empty { color: var(--muted); font-family: var(--sans); }
</style>
