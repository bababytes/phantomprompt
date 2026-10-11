<script setup>
import { ref, watch } from "vue";
import { labRequest, policy, workspace } from "../workspace.js";

const input = ref("Discuss PROJECT.ORION with project.orion.");
const result = ref("Your masked preview will appear here.");
const preview = ref({ text: "No external API is called.", kind: "" });

watch(() => workspace.generation, () => {
  result.value = "Rules changed. Run a new local test.";
  preview.value = { text: "No external API is called.", kind: "" };
});

async function test() {
  workspace.working = true;
  try {
    if (workspace.rulesDirty) throw new Error("Apply pending field changes before testing.");
    const text = input.value;
    const masked = await labRequest("/api/regex/test", { text, policy: policy() });
    result.value = masked.masked;
    const matches = masked.restored === text;
    preview.value = { text: `${masked.total} value(s) masked, including ${masked.matches.custom} custom match(es). Restored text ${matches ? "matches" : "differs from"} the original.`, kind: matches ? "success" : "error" };
  } catch (error) {
    result.value = "Preview unavailable.";
    preview.value = { text: error.message, kind: "error" };
  } finally {
    workspace.working = false;
  }
}
</script>

<template>
  <article class="workspace-card">
    <p class="eyebrow">LOCAL TESTER</p>
    <h2>Try the active rules</h2>
    <p>This preview uses the Go masking engine directly. Open the live demo to test the full HTTPS proxy path.</p>
    <label for="lab-input">Sample text</label>
    <textarea id="lab-input" v-model="input" maxlength="20000" spellcheck="false"></textarea>
    <button id="test-regex" class="secondary" :disabled="workspace.working" @click="test">Test active rules</button>
    <p id="preview-status" role="status" aria-live="polite" :class="preview.kind">{{ preview.text }}</p>
    <pre id="lab-result" class="preview-result">{{ result }}</pre>
  </article>
</template>
