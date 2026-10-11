<script setup>
import { computed, ref } from "vue";
import { applyRules, labRequest, policy, workspace } from "../workspace.js";

const draft = ref("");
const draftName = ref("");
const patternError = ref("");
const showRegex = ref(false);

const off = computed(() => new Set(workspace.disabledSecrets));
const builtIn = computed(() => workspace.secretDetectors.map((detector) => ({ ...detector, off: off.value.has(detector.id) })));
const offCount = computed(() => builtIn.value.filter((detector) => detector.off).length);
const ownSecrets = computed(() => workspace.customRules.filter((rule) => rule.secret));

function toggle(detector) {
  workspace.disabledSecrets = detector.off ? workspace.disabledSecrets.filter((id) => id !== detector.id) : [...workspace.disabledSecrets, detector.id];
  applyRules();
}

function removeOwn(rule) {
  workspace.customRules = workspace.customRules.filter((candidate) => candidate !== rule);
  applyRules();
}

async function addPattern() {
  const pattern = draft.value.trim();
  patternError.value = "";
  if (workspace.customRules.some((rule) => rule.pattern === pattern)) {
    patternError.value = "This pattern is already active.";
    return;
  }
  if (workspace.customRules.length >= 16) {
    patternError.value = "Up to 16 own patterns, shared with the Rule Builder.";
    return;
  }
  const rule = { pattern, secret: true };
  if (draftName.value.trim()) rule.name = draftName.value.trim();
  const candidate = [...workspace.customRules, rule];
  workspace.working = true;
  try {
    await labRequest("/api/regex/test", { text: "", policy: policy(candidate) });
  } catch (error) {
    patternError.value = error.message;
    return;
  } finally {
    workspace.working = false;
  }
  draft.value = "";
  draftName.value = "";
  workspace.customRules = candidate;
  applyRules();
}

function buildFromExample() {
  workspace.draftSecret = true;
  document.getElementById("rule-builder")?.scrollIntoView({ behavior: "smooth", block: "start" });
  document.getElementById("literal")?.focus({ preventScroll: true });
}
</script>

<template>
  <p class="note">Keys and tokens in known formats become <code>[PP_SECRET_…]</code> and stay masked in shell, web and MCP tool calls; only file edits get the real value back. Built-in patterns have a solid outline, your own patterns a green one.</p>
  <p class="note">Passwords after a label such as <code>password:</code> or <code>DB_PASSWORD=</code> are not built in: add <code>password</code> as a named field, or add your own pattern here.</p>
  <p v-if="offCount" class="fields-warning" role="status">⚠ {{ offCount }} built-in pattern{{ offCount === 1 ? " is" : "s are" }} off — matching values reach AI providers unmasked.</p>
  <ul class="chips" aria-label="Secret patterns">
    <li v-for="detector in builtIn" :key="detector.id" class="chip" :class="{ off: detector.off }">
      <span :title="detector.pattern">{{ detector.name }}</span>
      <button v-if="detector.off" :aria-label="`Turn ${detector.name} back on`" title="Turn back on" :disabled="workspace.working" @click="toggle(detector)">+</button>
      <button v-else :aria-label="`Stop masking ${detector.name}`" title="Stop masking" :disabled="workspace.working" @click="toggle(detector)">×</button>
    </li>
    <li v-for="rule in ownSecrets" :key="rule.pattern" class="chip custom">
      <span :title="rule.pattern">{{ rule.name || rule.pattern }}</span>
      <button :aria-label="`Remove ${rule.name || rule.pattern}`" title="Remove" :disabled="workspace.working" @click="removeOwn(rule)">×</button>
    </li>
  </ul>
  <form class="custom-controls" @submit.prevent="addPattern">
    <label class="sr-only" for="secret-pattern">New secret pattern (Go regex)</label>
    <input id="secret-pattern" v-model="draft" type="text" maxlength="512" placeholder="(?i:\bACME-[A-Z0-9]{24}\b)" autocomplete="off" spellcheck="false" :disabled="workspace.working">
    <label class="sr-only" for="secret-pattern-name">Name of the new pattern (optional)</label>
    <input id="secret-pattern-name" v-model="draftName" class="name-input" type="text" maxlength="64" placeholder="Name (optional)" autocomplete="off" :disabled="workspace.working">
    <button id="add-secret-pattern" class="secondary" type="submit" :disabled="workspace.working || !draft.trim()">Add pattern</button>
  </form>
  <p v-if="patternError" class="error" role="alert">{{ patternError }}</p>
  <p class="note">
    <button type="button" class="text-button" :aria-expanded="showRegex" @click="showRegex = !showRegex">{{ showRegex ? "Hide regex" : "Show regex" }}</button>
    <button type="button" class="text-button" :disabled="workspace.working" @click="buildFromExample">Build a pattern from an example</button>
  </p>
  <ul v-if="showRegex" class="regex-list" aria-label="Secret pattern regex">
    <li v-for="detector in builtIn" :key="detector.id" :class="{ off: detector.off }">
      <span>{{ detector.name }}</span>
      <code>{{ detector.pattern }}</code>
    </li>
    <li v-for="rule in ownSecrets" :key="rule.pattern" class="own">
      <span>{{ rule.name || "Your pattern" }}</span>
      <code>{{ rule.pattern }}</code>
    </li>
  </ul>
</template>

<style scoped>
.name-input { flex: 0 1 180px; }
.chip span { max-width: 360px; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.note .text-button { margin: 0 8px 0 0; }
.regex-list { display: grid; grid-template-columns: max-content minmax(0, 1fr); gap: 8px 16px; margin: 0; padding: 0; list-style: none; }
.regex-list li { display: grid; grid-column: 1 / -1; grid-template-columns: subgrid; align-items: baseline; min-width: 0; }
.regex-list li > span { font-size: 13px; }
.regex-list li.own > span { color: var(--accent); }
.regex-list li.off code { opacity: .45; text-decoration: line-through; }
.regex-list code { justify-self: start; max-width: 100%; overflow-wrap: anywhere; }
@media (max-width: 550px) {
  .regex-list { grid-template-columns: minmax(0, 1fr); gap: 12px; }
  .regex-list li { display: flex; flex-direction: column; gap: 5px; }
}
</style>
