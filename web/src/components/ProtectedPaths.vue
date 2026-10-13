<script setup>
import { computed, nextTick, ref } from "vue";
import { folderTotals } from "../activity.js";
import { monitor } from "../monitor.js";
import { applyRules, workspace } from "../workspace.js";
import FolderBrowser from "./FolderBrowser.vue";

const draft = ref("");
const pathError = ref("");
const browsing = ref(false);
const browseButton = ref(null);

const statuses = computed(() => new Map((monitor.totals.protected_paths ?? []).map((status) => [status.path, status])));
const reached = computed(() => folderTotals(monitor.events));

function describe(path) {
  const status = statuses.value.get(path);
  if (!status) return { text: "indexing…", cls: "" };
  if (status.error) return { text: status.error, cls: "status-warn" };
  const skipped = status.skipped ? ` · ${status.skipped} skipped` : "";
  return { text: `${status.files} file${status.files === 1 ? "" : "s"} · ${status.lines} line${status.lines === 1 ? "" : "s"}${skipped}`, cls: "status-ok" };
}

function normalize(path) {
  return path.length > 1 ? path.replace(/\/+$/, "") : path;
}

function formatProblem(path) {
  if (path === "/" || path === "~") return "Choose a specific folder, not the whole disk or home folder.";
  if (!path.startsWith("/") && !path.startsWith("~/")) return "Use a path starting with ~/ or /, for example ~/.ssh";
  return "";
}

const draftProblem = computed(() => {
  const path = normalize(draft.value.trim());
  return path ? formatProblem(path) : "";
});

function addPath(chosen) {
  const path = normalize((typeof chosen === "string" ? chosen : draft.value).trim());
  pathError.value = formatProblem(path);
  if (pathError.value) return;
  if (workspace.protectedPaths.includes(path)) {
    pathError.value = `${path} is already protected.`;
    return;
  }
  if (workspace.protectedPaths.length >= 16) {
    pathError.value = "Up to 16 protected paths.";
    return;
  }
  if (typeof chosen !== "string") draft.value = "";
  workspace.protectedPaths = [...workspace.protectedPaths, path];
  applyRules();
}

async function closeBrowser() {
  browsing.value = false;
  await nextTick();
  browseButton.value?.focus();
}

function protectChosen(path) {
  addPath(path);
  if (!pathError.value) closeBrowser();
}

function removePath(path) {
  workspace.protectedPaths = workspace.protectedPaths.filter((entry) => entry !== path);
  workspace.blockedPaths = workspace.blockedPaths.filter((entry) => entry !== path);
  applyRules();
}

const blocked = (path) => workspace.blockedPaths.includes(path);

function setBlocked(path, on) {
  if (blocked(path) === on) return;
  workspace.blockedPaths = on ? [...workspace.blockedPaths, path] : workspace.blockedPaths.filter((entry) => entry !== path);
  applyRules();
}
</script>

<template>
  <section class="workspace-card fields-card" aria-labelledby="paths-title">
    <div class="section-heading"><div><p class="eyebrow">PROTECTED FILES</p><h2 id="paths-title">Protected folders and files</h2></div></div>
    <p>Applies to every AI that goes through PhantomPrompt (coding agents in protected terminals). Lines of these files (12 characters or longer) are recognised however the AI got them; only fingerprints are kept in memory and files are re-checked every minute.</p>
    <ul class="note">
      <li><strong>Masked</strong>: each line is replaced with a token and restored in answers, so the AI can still work with the file.</li>
      <li><strong>Blocked</strong>: where a coding tool can be told not to read the folder, PhantomPrompt does that when it starts the tool. If the AI still gets the content, any message, file read or tool result with a line from the folder is removed before it leaves this Mac. This is stronger than masking, so Blocked already covers it.</li>
    </ul>
    <ul v-if="workspace.protectedPaths.length" class="chips path-chips" aria-label="Protected paths">
      <li v-for="path in workspace.protectedPaths" :key="path" class="chip custom">
        <span>{{ path }}</span>
        <small :class="describe(path).cls">{{ describe(path).text }}</small>
        <small v-if="reached.get(path)?.masked" class="reach-masked" title="Times the AI got lines of this folder, masked, in the prompt history">AI masked ×{{ reached.get(path).masked }}</small>
        <small v-if="reached.get(path)?.blocked" class="reach-blocked" title="Times the AI reached for this folder and was blocked, in the prompt history">AI blocked ×{{ reached.get(path).blocked }}</small>
        <span class="modes" role="group" :aria-label="`Protection for ${path}`">
          <button class="mode" :aria-pressed="!blocked(path)" title="Masked: lines become tokens and are restored in answers." :disabled="workspace.working" @click="setBlocked(path, false)">Masked</button>
          <button class="mode blocked" :aria-pressed="blocked(path)" title="Blocked: the AI is told not to read it, and any content that still leaks is removed (stronger than masking)." :disabled="workspace.working" @click="setBlocked(path, true)">Blocked</button>
        </span>
        <button :aria-label="`Stop protecting ${path}`" title="Stop protecting" :disabled="workspace.working" @click="removePath(path)">×</button>
      </li>
    </ul>
    <form class="custom-controls" @submit.prevent="addPath">
      <label class="sr-only" for="protected-path">Folder or file to protect</label>
      <input id="protected-path" v-model="draft" type="text" maxlength="512" placeholder="~/.ssh" autocomplete="off" spellcheck="false" :class="{ invalid: draftProblem }" :aria-invalid="draftProblem ? 'true' : 'false'" :aria-describedby="draftProblem ? 'protected-path-hint' : undefined" :disabled="workspace.working">
      <button id="add-path" class="secondary" type="submit" :disabled="workspace.working || !draft.trim() || Boolean(draftProblem)">Protect</button>
      <button ref="browseButton" id="browse-path" class="secondary" type="button" :aria-expanded="browsing" :disabled="workspace.working" @click="browsing = !browsing">Browse…</button>
    </form>
    <p v-if="draftProblem" id="protected-path-hint" class="error">{{ draftProblem }}</p>
    <p v-if="pathError && !draftProblem" class="error" role="alert">{{ pathError }}</p>
    <FolderBrowser v-if="browsing" @choose="protectChosen" @close="closeBrowser" />
  </section>
</template>

<style scoped>
.chip .reach-masked { color: var(--accent); }
.chip .reach-blocked { color: var(--danger); }
input.invalid { border-color: var(--danger); outline-color: var(--danger); }
</style>
