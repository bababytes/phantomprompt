<script setup>
import { computed, ref } from "vue";
import { applyRules, fieldKey, workspace } from "../workspace.js";

const FIELD_NAME = /^[A-Za-z][A-Za-z0-9_-]{0,63}$/;
const draft = ref("");
const fieldError = ref("");

const disabled = computed(() => new Set(workspace.disabledFields.map(fieldKey)));
const builtIn = computed(() => workspace.defaultFields.map((name) => ({ name, off: disabled.value.has(fieldKey(name)) })));
const disabledCount = computed(() => builtIn.value.filter((field) => field.off).length);

function save(change) {
  change();
  applyRules();
}

function addField() {
  const name = draft.value.trim();
  fieldError.value = "";
  if (!FIELD_NAME.test(name)) {
    fieldError.value = "Use a letter first, then letters, digits, _ or - (max 64).";
    return;
  }
  const key = fieldKey(name);
  if (workspace.defaultFields.some((field) => fieldKey(field) === key)) {
    if (disabled.value.has(key)) save(() => { workspace.disabledFields = workspace.disabledFields.filter((field) => fieldKey(field) !== key); });
    else fieldError.value = `${name} is already a built-in field.`;
    draft.value = "";
    return;
  }
  if (workspace.customFields.some((field) => fieldKey(field) === key)) {
    fieldError.value = `${name} is already protected.`;
    return;
  }
  if (workspace.customFields.length >= 64) {
    fieldError.value = "Up to 64 custom fields.";
    return;
  }
  draft.value = "";
  save(() => { workspace.customFields = [...workspace.customFields, name]; });
}

function toggleBuiltIn(field) {
  save(() => {
    workspace.disabledFields = field.off ? workspace.disabledFields.filter((name) => fieldKey(name) !== fieldKey(field.name)) : [...workspace.disabledFields, field.name];
  });
}

function removeCustom(name) {
  save(() => { workspace.customFields = workspace.customFields.filter((field) => field !== name); });
}
</script>

<template>
  <p class="note">Values after these names are masked in JSON and <code>field: value</code> or <code>field=value</code> text. Names ignore case, <code>_</code> and <code>-</code>. Built-in fields have a solid outline, your own fields a green one.</p>
  <p v-if="disabledCount" class="fields-warning" role="status">⚠ {{ disabledCount }} built-in field{{ disabledCount === 1 ? " is" : "s are" }} off — values after {{ disabledCount === 1 ? "that name" : "those names" }} reach AI providers unmasked.</p>
  <ul class="chips" aria-label="Protected field names">
    <li v-for="field in builtIn" :key="field.name" class="chip" :class="{ off: field.off }">
      <span>{{ field.name }}</span>
      <button v-if="field.off" :aria-label="`Turn ${field.name} back on`" title="Turn back on" :disabled="workspace.working" @click="toggleBuiltIn(field)">+</button>
      <button v-else :aria-label="`Stop protecting ${field.name}`" title="Stop protecting" :disabled="workspace.working" @click="toggleBuiltIn(field)">×</button>
    </li>
    <li v-for="name in workspace.customFields" :key="name" class="chip custom">
      <span>{{ name }}</span>
      <button :aria-label="`Remove ${name}`" title="Remove" :disabled="workspace.working" @click="removeCustom(name)">×</button>
    </li>
  </ul>
  <form class="custom-controls" @submit.prevent="addField">
    <label class="sr-only" for="custom-fields">New field name</label>
    <input id="custom-fields" v-model="draft" type="text" maxlength="64" placeholder="employeeId" autocomplete="off" spellcheck="false" :disabled="workspace.working">
    <button id="add-field" class="secondary" type="submit" :disabled="workspace.working || !draft.trim()">Add field</button>
  </form>
  <p v-if="fieldError" class="error" role="alert">{{ fieldError }}</p>
  <p class="note">Plain prose names, addresses and unlabeled phone or card numbers are not detected.</p>
</template>
