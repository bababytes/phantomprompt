<script setup>
import { computed, nextTick, onBeforeUnmount, onMounted, ref, useId, watch } from "vue";
import { labRequest } from "../workspace.js";

const props = defineProps({
  title: { type: String, default: "Choose a folder or file to protect" },
  action: { type: String, default: "Protect this folder" },
  files: { type: Boolean, default: true },
  anyFolder: { type: Boolean, default: false },
  recent: { type: Array, default: () => [] },
  start: { type: String, default: "~/" },
});
const emit = defineEmits(["choose", "close"]);
const uid = useId();

const query = ref(props.start.endsWith("/") ? props.start : `${props.start}/`);
const listing = ref(null);
const error = ref("");
const loading = ref(false);
const showHidden = ref(false);
const highlighted = ref(-1);
const input = ref(null);
const list = ref(null);
let request = 0;
let timer = 0;

function normalize(text) {
  const value = text.trim();
  if (!value) return "~/";
  return value.startsWith("/") || value === "~" || value.startsWith("~/") ? value : `~/${value}`;
}

function join(folder, name) {
  return folder === "/" ? `/${name}` : `${folder}/${name}`;
}

const typed = computed(() => {
  const value = normalize(query.value);
  if (value === "~") return { folder: "~", filter: "" };
  const cut = value.lastIndexOf("/");
  return { folder: cut <= 0 ? "/" : value.slice(0, cut), filter: value.slice(cut + 1) };
});

const target = computed(() => normalize(query.value).replace(/(.)\/+$/, "$1"));
const choosable = computed(() => props.anyFolder || (target.value !== "/" && target.value !== "~"));

const visible = computed(() => {
  if (!listing.value) return [];
  const filter = typed.value.filter.toLowerCase();
  const hidden = showHidden.value || filter.startsWith(".");
  const entries = listing.value.entries.filter((entry) => (props.files || entry.dir) && (hidden || !entry.name.startsWith(".")));
  if (!filter) return entries;
  const name = (entry) => entry.name.toLowerCase();
  return [...entries.filter((entry) => name(entry).startsWith(filter)), ...entries.filter((entry) => !name(entry).startsWith(filter) && name(entry).includes(filter))];
});

const crumbs = computed(() => {
  const path = listing.value?.path ?? "";
  if (!path) return [];
  const home = path.startsWith("~");
  let current = home ? "~" : "";
  const result = [{ label: home ? "~" : "/", path: home ? "~" : "/" }];
  for (const part of path.replace(/^~/, "").split("/").filter(Boolean)) {
    current = `${current}/${part}`;
    result.push({ label: part, path: current });
  }
  return result;
});

const shortcuts = computed(() => [...new Set(["~", ...props.recent.filter(Boolean)])].filter((path) => path !== target.value).slice(0, 6));

async function load(folder) {
  const id = ++request;
  loading.value = true;
  try {
    const result = await labRequest("/api/folders", { path: folder });
    if (id !== request) return;
    listing.value = result;
    error.value = "";
  } catch (failure) {
    if (id !== request) return;
    listing.value = null;
    error.value = failure.message;
  } finally {
    if (id === request) loading.value = false;
  }
}

watch(() => typed.value.folder, (folder) => {
  clearTimeout(timer);
  timer = setTimeout(() => load(folder), 120);
}, { immediate: true });

watch(visible, () => { highlighted.value = -1; });

function go(path) {
  query.value = path === "/" ? "/" : `${path}/`;
  input.value?.focus();
}

function open(entry) {
  const path = join(listing.value.path, entry.name);
  if (entry.dir) go(path);
  else emit("choose", path);
}

function choose() {
  if (choosable.value) emit("choose", target.value);
}

async function move(step) {
  if (!visible.value.length) return;
  highlighted.value = Math.max(-1, Math.min(visible.value.length - 1, highlighted.value + step));
  await nextTick();
  list.value?.querySelector(".highlighted")?.scrollIntoView({ block: "nearest" });
}

function complete(event) {
  const entry = visible.value[Math.max(highlighted.value, 0)];
  if (!entry?.dir || !typed.value.filter && highlighted.value < 0) return;
  event.preventDefault();
  go(join(listing.value.path, entry.name));
}

function submit() {
  if (highlighted.value >= 0) open(visible.value[highlighted.value]);
  else choose();
}

onMounted(() => input.value?.focus());
onBeforeUnmount(() => clearTimeout(timer));
</script>

<template>
  <div class="folder-browser" role="dialog" :aria-label="title" @keydown.esc.stop.prevent="emit('close')">
    <p class="browser-title">{{ title }}</p>
    <form class="path-row" @submit.prevent="submit">
      <label class="sr-only" :for="`${uid}-path`">Folder path</label>
      <input :id="`${uid}-path`" ref="input" v-model="query" class="folder-path" type="text" autocomplete="off" spellcheck="false" placeholder="~/Projects/my-app" :aria-describedby="`${uid}-hint`" @keydown.down.prevent="move(1)" @keydown.up.prevent="move(-1)" @keydown.tab.exact="complete">
      <button type="button" class="primary" :disabled="!choosable" :title="target" @click="choose">{{ action }}</button>
      <button type="button" class="secondary" @click="emit('close')">Close</button>
    </form>
    <p :id="`${uid}-hint`" class="hint">Type a path or pick below · <kbd>Tab</kbd> completes · <kbd>↑</kbd><kbd>↓</kbd> select · <kbd>Enter</kbd> {{ highlighted >= 0 ? "opens the selected folder" : `uses ${target}` }}</p>
    <div v-if="shortcuts.length" class="shortcuts" aria-label="Shortcuts">
      <button v-for="path in shortcuts" :key="path" type="button" class="shortcut" @click="go(path)">{{ path === "~" ? "Home" : path }}</button>
    </div>
    <div class="browser-bar">
      <nav class="crumbs" aria-label="Current folder">
        <template v-for="(crumb, index) in crumbs" :key="crumb.path">
          <span v-if="index > 0 && crumbs[index - 1].path !== '/'" aria-hidden="true">/</span>
          <button type="button" class="crumb" :aria-current="index === crumbs.length - 1 ? 'location' : undefined" @click="go(crumb.path)">{{ crumb.label }}</button>
        </template>
      </nav>
      <label class="hidden-toggle"><input v-model="showHidden" type="checkbox"> Show hidden</label>
    </div>
    <p v-if="error" class="error" role="alert">{{ error }}</p>
    <ul v-else-if="listing" ref="list" class="browser-list" aria-label="Folder contents" :aria-busy="loading">
      <li v-for="(entry, index) in visible" :key="entry.name" :class="{ highlighted: index === highlighted }">
        <button type="button" class="entry" :class="entry.dir ? 'folder' : 'file'" :tabindex="-1" @click="open(entry)">
          <svg v-if="entry.dir" class="icon" viewBox="0 0 16 16" aria-hidden="true"><path d="M1.5 3.5h4.2l1.5 1.6h7.3v7.4h-13z" /></svg>
          <svg v-else class="icon" viewBox="0 0 16 16" aria-hidden="true"><path d="M3.5 1.5h6l3 3v10h-9z" /></svg>
          <span class="name">{{ entry.name }}</span>
          <span class="entry-action">{{ entry.dir ? "Open" : "Protect" }}</span>
        </button>
      </li>
      <li v-if="!visible.length" class="empty">{{ typed.filter ? `No ${files ? "folders or files" : "folders"} starting with “${typed.filter}”.` : `No ${files ? "" : "sub"}folders here.` }}</li>
    </ul>
    <p v-if="listing?.truncated" class="note">Showing the first 1000 entries; type to narrow the list.</p>
  </div>
</template>

<style scoped>
.folder-browser { margin-top: 14px; padding: 16px; border: 1px solid var(--line-strong); border-radius: 12px; background: var(--bg-sunk); }
.folder-browser button { margin: 0; }
.browser-title { margin: 0 0 10px; color: var(--muted); font-family: var(--mono); font-size: 11px; letter-spacing: .12em; text-transform: uppercase; }
.path-row { display: flex; align-items: center; gap: 10px; }
.path-row input { flex: 1; min-width: 0; font-family: var(--mono); font-size: 14px; }
.hint { margin: 8px 0 0; color: var(--faint); font-size: 11px; }
kbd { padding: 0 4px; border: 1px solid var(--line-strong); border-radius: 4px; font-family: var(--mono); font-size: 10px; color: var(--muted); }
.shortcuts { display: flex; flex-wrap: wrap; gap: 6px; margin-top: 12px; }
.shortcut { padding: 3px 10px; border: 1px solid var(--line-strong); border-radius: 999px; background: transparent; color: var(--muted); font-family: var(--mono); font-size: 12px; cursor: pointer; }
.shortcut:hover, .shortcut:focus-visible { border-color: var(--accent); color: var(--accent); }
.browser-bar { display: flex; align-items: center; justify-content: space-between; gap: 12px; margin-top: 14px; padding-bottom: 8px; border-bottom: 1px solid var(--line); }
.crumbs { display: flex; flex-wrap: wrap; align-items: center; gap: 2px; min-width: 0; color: var(--faint); font-family: var(--mono); font-size: 12px; }
.crumb { padding: 2px 6px; border: 0; border-radius: 6px; background: transparent; color: var(--muted); cursor: pointer; }
.crumb[aria-current="location"] { color: var(--text); font-weight: 600; }
.crumb:hover, .crumb:focus-visible { background: rgb(127 127 127 / .15); color: var(--text); }
.hidden-toggle { display: flex; flex-shrink: 0; align-items: center; gap: 6px; margin: 0; color: var(--muted); font-size: 12px; font-weight: 400; cursor: pointer; }
.browser-list { max-height: 300px; margin: 6px 0 0; padding: 0; overflow-y: auto; list-style: none; }
.entry { display: flex; align-items: center; gap: 10px; width: 100%; padding: 6px 8px; border: 0; border-radius: 6px; background: transparent; color: var(--text); font-family: var(--mono); font-size: 13px; text-align: left; cursor: pointer; }
.entry.file { color: var(--muted); }
.entry:hover { background: rgb(127 127 127 / .12); }
.highlighted .entry { background: var(--accent-soft); }
.icon { flex-shrink: 0; width: 15px; height: 15px; fill: none; stroke: var(--muted); stroke-width: 1.2; stroke-linejoin: round; }
.folder .icon { stroke: var(--accent); }
.name { flex: 1; min-width: 0; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.entry-action { color: var(--accent); font-family: var(--sans); font-size: 12px; opacity: 0; }
.highlighted .entry-action, .entry:hover .entry-action { opacity: 1; }
.empty { padding: 10px 8px; color: var(--muted); font-size: 12px; }
@media (max-width: 550px) {
  .path-row { flex-wrap: wrap; }
  .path-row input { flex-basis: 100%; }
}
</style>
