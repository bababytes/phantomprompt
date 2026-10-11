<script setup>
import { ref } from "vue";
import { claudeSetup } from "../config.js";

const copied = ref(false);

async function copy() {
  await navigator.clipboard.writeText(claudeSetup);
  copied.value = true;
  setTimeout(() => (copied.value = false), 2000);
}
</script>

<template>
  <section v-if="claudeSetup" id="claude-setup" class="workspace-card" aria-labelledby="claude-setup-title">
    <div class="section-heading">
      <div><p class="eyebrow">ONE-TIME SETUP</p><h2 id="claude-setup-title">Connect your coding agents</h2></div>
      <button class="text-button" @click="copy">{{ copied ? "Copied" : "Copy" }}</button>
    </div>
    <p>Add these lines to <code>~/.zshrc</code>, open a new terminal and run <code>claude</code> or <code>codex</code>. Model requests then go through PhantomPrompt while this app runs; when it is closed, the agents cannot reach the model, so nothing is sent unmasked.</p>
    <pre><code>{{ claudeSetup }}</code></pre>
  </section>
</template>
