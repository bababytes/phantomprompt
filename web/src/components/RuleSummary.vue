<script setup>
import { computed, reactive } from "vue";
import { monitor, time } from "../monitor.js";
import { describe, literalText } from "../rules.js";
import { workspace } from "../workspace.js";

const props = defineProps({ query: { type: String, default: "" } });

const COLUMNS = [
  { key: "rule", label: "Rule" },
  { key: "caught", label: "Caught" },
  { key: "hits", label: "Matches" },
  { key: "unique", label: "Different values" },
  { key: "last", label: "Last seen" },
];
const sort = reactive({ key: "hits", dir: "desc" });

const stats = computed(() => {
  const rules = new Map(workspace.customRules.map((rule) => [rule.pattern, rule]));
  return (monitor.totals.custom_rules ?? []).map((stat) => {
    const rule = rules.get(stat.pattern) ?? {};
    const literal = literalText(stat.pattern);
    return {
      pattern: stat.pattern,
      rule: rule.name || describe(stat.pattern),
      secret: Boolean(rule.secret),
      caught: literal === null ? stat.examples ?? [] : stat.hits ? [literal] : [],
      hits: stat.hits,
      unique: stat.unique,
      last: stat.last_hit ? Date.parse(stat.last_hit) : 0,
    };
  }).concat((monitor.totals.secret_rules ?? []).map((stat) => ({
    pattern: stat.pattern,
    rule: stat.name,
    secret: true,
    builtIn: true,
    caught: stat.examples ?? [],
    hits: stat.hits,
    unique: stat.unique,
    last: stat.last_hit ? Date.parse(stat.last_hit) : 0,
  })));
});

const rows = computed(() => {
  const needle = props.query.trim().toLowerCase();
  const list = stats.value.filter((row) => row.hits > 0 && (!needle || [row.rule, row.pattern, ...row.caught].join(" ").toLowerCase().includes(needle)));
  const value = { rule: (row) => row.rule, caught: (row) => row.caught.length, hits: (row) => row.hits, unique: (row) => row.unique, last: (row) => row.last };
  return list.sort((a, b) => {
    const x = value[sort.key](a);
    const y = value[sort.key](b);
    const order = typeof x === "string" ? x.localeCompare(y) : x - y;
    return sort.dir === "asc" ? order : -order;
  });
});

const emptyText = computed(() => {
  if (props.query.trim()) return "No rule matches the search.";
  if (!stats.value.length) return "No rules are on. Turn on Passwords & secrets or add a rule in the Rule Builder.";
  return "No rule has caught anything yet.";
});

function sortBy(key) {
  if (sort.key === key) sort.dir = sort.dir === "asc" ? "desc" : "asc";
  else Object.assign(sort, { key, dir: key === "rule" ? "asc" : "desc" });
}
</script>

<template>
  <div class="table-scroll">
    <table>
      <thead>
        <tr>
          <th v-for="column in COLUMNS" :key="column.key" :aria-sort="sort.key === column.key ? (sort.dir === 'asc' ? 'ascending' : 'descending') : 'none'">
            <button class="sort-button" @click="sortBy(column.key)">{{ column.label }}<span aria-hidden="true">{{ sort.key === column.key ? (sort.dir === "asc" ? " ▲" : " ▼") : "" }}</span></button>
          </th>
        </tr>
      </thead>
      <tbody id="rule-rows">
        <tr v-for="row in rows" :key="row.pattern">
          <td class="rule-cell" :title="row.pattern"><span class="rule-name">{{ row.rule }}</span><span v-if="row.secret" class="rule-kind">{{ row.builtIn ? "built-in secret" : "secret" }}</span></td>
          <td class="caught-cell">
            <span v-for="value in row.caught" :key="value" class="caught">{{ value }}</span>
            <span v-if="!row.caught.length" class="rule-none">—</span>
          </td>
          <td>{{ row.hits }}</td>
          <td>{{ row.unique }}</td>
          <td>{{ row.last ? time(row.last) : "—" }}</td>
        </tr>
        <tr v-if="!rows.length"><td colspan="5" class="rule-none">{{ emptyText }}</td></tr>
      </tbody>
    </table>
    <p class="note">Values caught by patterns are shown partly hidden and secrets completely (the last 12 per rule), only in this panel and only in memory; Clear history and a restart remove them. Exact-text rules show their own text.</p>
  </div>
</template>

<style scoped>
.rule-cell { max-width: 280px; overflow: hidden; text-overflow: ellipsis; }
.rule-name { color: var(--text); }
.rule-kind { margin-left: 10px; font-family: var(--mono); font-size: 10px; letter-spacing: .08em; color: var(--secret); text-transform: uppercase; }
.caught-cell { white-space: normal; }
.caught { display: inline-block; margin: 2px 6px 2px 0; padding: 2px 8px; border: 1px solid var(--line-strong); border-radius: 999px; font-family: var(--mono); font-size: 11px; color: var(--text); background: var(--bg-sunk); }
.rule-none { color: var(--muted); font-family: var(--sans); }
</style>
