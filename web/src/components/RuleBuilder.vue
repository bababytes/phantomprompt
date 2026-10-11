<script setup>
import { computed, ref } from "vue";
import { applyRules, labRequest, policy, setStatus, workspace } from "../workspace.js";

const literal = ref("");
const mode = ref("exact");
const caseSensitive = ref(false);
const pattern = ref("");
const name = ref("");
const generated = ref({ pattern: "", description: "" });
const meaning = computed(() => (pattern.value && pattern.value === generated.value.pattern ? generated.value.description : ""));

async function generatePattern() {
  generated.value = await labRequest("/api/regex/generate", { text: literal.value, case_sensitive: caseSensitive.value, shape: mode.value === "shape" });
  pattern.value = generated.value.pattern;
}

async function generate() {
  workspace.working = true;
  try {
    await generatePattern();
    setStatus(`Pattern generated: catches ${meaning.value}. Click Add to active rules to enable it.`, "success");
  } catch (error) {
    setStatus(error.message, "error");
  } finally {
    workspace.working = false;
  }
}

async function add() {
  workspace.working = true;
  try {
    if (!pattern.value.trim()) {
      if (!literal.value.trim()) throw new Error("Enter an example or a regex pattern.");
      await generatePattern();
    }
    if (workspace.customRules.some((rule) => rule.pattern === pattern.value)) throw new Error("This rule is already active.");
    const rule = { pattern: pattern.value };
    if (name.value.trim()) rule.name = name.value.trim();
    if (workspace.draftSecret) rule.secret = true;
    const candidate = [...workspace.customRules, rule];
    await labRequest("/api/regex/test", { text: "", policy: policy(candidate) });
    workspace.customRules = candidate;
    if (await applyRules()) setStatus(workspace.customRulesOn ? "Rule added. It is listed under “Your custom rules” in What gets masked." : "Rule added, but “Your custom rules” is switched off in What gets masked, so it masks nothing yet.", "success");
    name.value = "";
    workspace.draftSecret = false;
  } catch (error) {
    setStatus(error.message, "error");
  } finally {
    workspace.working = false;
  }
}

</script>

<template>
  <article id="rule-builder" class="workspace-card">
    <p class="eyebrow">RULE BUILDER</p>
    <h2>Create a rule</h2>
    <p>Type an example of what should be masked, then catch exactly that text or anything with the same layout.</p>
    <label for="literal">Example</label>
    <input id="literal" v-model="literal" type="text" maxlength="128" placeholder="PROJECT.ORION or abc-x7k2-91" autocomplete="off" spellcheck="false" :disabled="workspace.working">
    <fieldset class="mode-options" :disabled="workspace.working">
      <legend class="sr-only">What to catch</legend>
      <label class="case-option"><input id="mode-exact" v-model="mode" type="radio" name="rule-mode" value="exact"> Exactly this text</label>
      <label class="case-option"><input id="mode-shape" v-model="mode" type="radio" name="rule-mode" value="shape"> Anything with the same layout <small>letters stay letters, digits stay digits, separators stay the same</small></label>
    </fieldset>
    <label class="case-option"><input id="case-sensitive" v-model="caseSensitive" type="checkbox" :disabled="workspace.working"> Match case</label>
    <button id="generate-regex" class="secondary" :disabled="workspace.working || !literal.trim()" @click="generate">Generate regex</button>
    <label for="regex-pattern">Pattern · editable Go regex</label>
    <input id="regex-pattern" v-model="pattern" type="text" maxlength="512" placeholder="(?i:PROJECT\.ORION)" autocomplete="off" spellcheck="false" :disabled="workspace.working">
    <p v-if="meaning" id="pattern-meaning" class="note">Catches {{ meaning }}.</p>
    <label for="rule-name">Name <small>optional</small></label>
    <input id="rule-name" v-model="name" type="text" maxlength="64" placeholder="Order number" autocomplete="off" :disabled="workspace.working">
    <label class="case-option"><input id="secret-rule" v-model="workspace.draftSecret" type="checkbox" :disabled="workspace.working"> Password or secret <small>never sent back in shell, web or MCP commands</small></label>
    <button id="add-regex" class="primary" :disabled="workspace.working || (!pattern.trim() && !literal.trim())" @click="add">Add to active rules</button>
    <p id="rule-status" role="status" aria-live="polite" :class="workspace.status.kind">{{ workspace.status.text }}</p>
  </article>
</template>

<style scoped>
.mode-options { display: flex; flex-wrap: wrap; gap: 0 18px; margin: 0; padding: 0; border: 0; }
.mode-options input[type=radio] { accent-color: var(--accent); margin: 0; }
</style>
