<script setup>
import { workspace } from "../workspace.js";

defineProps({
  id: { type: String, required: true },
  title: { type: String, required: true },
  description: { type: String, default: "" },
  status: { type: String, default: "On" },
});
const enabled = defineModel("enabled", { type: Boolean, required: true });
const open = defineModel("open", { type: Boolean, default: false });
const emit = defineEmits(["change"]);

function toggle(event) {
  enabled.value = event.target.checked;
  emit("change");
}
</script>

<template>
  <section class="rule-group" :class="{ open, off: !enabled }" :aria-labelledby="`${id}-title`">
    <div class="group-head">
      <button :id="`${id}-title`" type="button" class="group-toggle" :aria-expanded="open" :aria-controls="`${id}-body`" @click="open = !open">
        <span class="chevron" aria-hidden="true"></span>
        <span class="group-text"><strong>{{ title }}</strong><small>{{ description }}</small></span>
      </button>
      <span class="group-status">{{ enabled ? status : "Off" }}</span>
      <label class="switch group-switch"><input :id="id" type="checkbox" :checked="enabled" :disabled="workspace.working" @change="toggle"><span class="sr-only">Mask {{ title }}</span></label>
    </div>
    <div v-show="open" :id="`${id}-body`" class="group-body"><slot /></div>
  </section>
</template>

<style scoped>
.rule-group { border: 1px solid var(--line); border-radius: 10px; background: var(--bg-sunk); transition: border-color .15s; }
.rule-group + .rule-group { margin-top: 8px; }
.rule-group.open { border-color: var(--line-strong); }
.group-head { display: flex; align-items: center; gap: 14px; padding: 4px 16px 4px 4px; }
.group-toggle { display: flex; flex: 1; align-items: center; gap: 14px; min-width: 0; margin: 0; padding: 12px; border: 0; border-radius: 8px; background: transparent; color: inherit; text-align: left; cursor: pointer; }
.group-toggle:hover { background: rgb(127 127 127 / .08); }
.chevron { flex-shrink: 0; width: 7px; height: 7px; border-right: 2px solid var(--muted); border-bottom: 2px solid var(--muted); transform: rotate(-45deg); transition: transform .15s; }
.open .chevron { transform: rotate(45deg); }
.group-text { display: flex; flex-direction: column; gap: 3px; min-width: 0; }
.group-text strong { color: var(--text); font-size: 14px; font-weight: 500; }
.group-text small { color: var(--muted); font-size: 12px; line-height: 1.5; }
.group-status { color: var(--muted); font-family: var(--mono); font-size: 11px; white-space: nowrap; }
.off .group-status { color: var(--faint); }
.off .group-text strong { color: var(--muted); }
.group-switch { margin: 0; }
.group-switch input[type=checkbox]:not(:checked) { background: transparent; border-color: var(--line-strong); }
.group-switch input[type=checkbox]:not(:checked)::after { background: var(--faint); }
.group-body { padding: 14px 20px 18px 42px; border-top: 1px solid var(--line); }
@media (max-width: 550px) {
  .group-status { display: none; }
  .group-body { padding-left: 16px; }
}
</style>
