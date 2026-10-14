<script setup>
import { computed, nextTick, ref } from "vue";
import { terminalLauncher } from "../config.js";
import { closeTerminal, confirmCloseTerminal, folderName, monitor, refresh, terminalName, terminalState, time, tokenOverhead } from "../monitor.js";
import { labRequest } from "../workspace.js";
import FolderBrowser from "./FolderBrowser.vue";

const sessions = computed(() => monitor.totals.cli_sessions ?? []);
const recentFolders = computed(() => [...new Set(sessions.value.map((item) => item.folder).filter(Boolean))]);
const trusted = computed(() => monitor.totals.trusted_paths ?? []);
const choosing = ref(false);
const opening = ref(false);
const message = ref(null);
const openButton = ref(null);
const trustStart = ref("");
const trusting = ref(false);
const trustMessage = ref(null);

const homePath = (path) => path.replace(/^\/Users\/[^/]+(?=\/|$)/, "~");
const count = (n, word) => `${n} ${word}${n === 1 ? "" : "s"}`;

async function openTerminal(path) {
  opening.value = true;
  message.value = null;
  try {
    const opened = await labRequest("/api/terminal", { path });
    choosing.value = false;
    message.value = { text: `Terminal opened in ${opened.path}. Everything you run there (claude, codex, copilot, …) is protected.`, kind: "success" };
  } catch (failure) {
    message.value = { text: failure.message, kind: "error" };
  } finally {
    opening.value = false;
  }
}

async function closeChooser() {
  choosing.value = false;
  await nextTick();
  openButton.value?.focus();
}

function toggleChooser() {
  choosing.value = !choosing.value;
  trustStart.value = "";
}

function chooseTrusted(folder) {
  trustStart.value = trustStart.value === folder ? "" : folder;
  choosing.value = false;
  trustMessage.value = null;
}

async function setTrusted(path, on) {
  trusting.value = true;
  trustMessage.value = null;
  try {
    await labRequest("/api/trusted", { path, trusted: on });
    trustStart.value = "";
    trustMessage.value = on
      ? { text: `Values from ${homePath(path)} now reach AI models without masking. Keep only fake data there.`, kind: "trust-note" }
      : { text: `Values from ${homePath(path)} are masked again.`, kind: "success" };
    await refresh();
  } catch (failure) {
    trustMessage.value = { text: failure.message, kind: "error" };
  } finally {
    trusting.value = false;
  }
}

async function close(item) {
  if (!(await confirmCloseTerminal(item))) return;
  message.value = null;
  try {
    await closeTerminal(item.id);
  } catch (failure) {
    message.value = { text: failure.message, kind: "error" };
  }
}
</script>

<template>
  <section class="workspace-card connections-card" aria-labelledby="protected-title">
    <div class="section-heading">
      <div><p class="eyebrow">UNDER PROTECTION NOW</p><h2 id="protected-title">Protected terminals</h2></div>
      <div>
        <button id="trust-folder" class="secondary" :aria-expanded="trustStart === '~'" :disabled="trusting" @click="chooseTrusted('~')">Trust a folder…</button>
        <button v-if="terminalLauncher" id="open-terminal" ref="openButton" class="primary" :aria-expanded="choosing" :disabled="opening" @click="toggleChooser">Open protected terminal</button>
      </div>
    </div>
    <FolderBrowser v-if="choosing" title="Folder for the protected terminal" action="Open terminal here" :files="false" :recent="recentFolders" any-folder @choose="openTerminal" @close="closeChooser" />
    <p v-if="message" id="terminal-status" role="status" :class="message.kind">{{ message.text }}</p>
    <p v-if="!sessions.length" id="cli-empty">Nothing is protected yet. <template v-if="terminalLauncher">Click <strong>Open protected terminal</strong> and choose a folder, or in a terminal run</template><template v-else>In a terminal run</template> <code>phantom</code>: everything you start there (claude, codex, copilot, …) is protected.</p>
    <div v-else id="cli-table" class="table-scroll">
      <table>
        <thead><tr><th>Command</th><th>Folder</th><th>Exceptions</th><th>Status</th><th>Services</th><th>Prompts</th><th>Blocked</th><th>Tokens from masking (est.)</th><th>Started</th><th>Last activity</th></tr></thead>
        <tbody id="cli-rows">
          <tr v-for="item in sessions" :key="item.started + item.label + item.pid">
            <td class="command">{{ terminalName(item) }}<button v-if="item.pid || item.disconnected" type="button" class="close-terminal" :aria-label="`Close ${terminalName(item)}`" title="Close this terminal" @click="close(item)">×</button></td>
            <td class="folder" :title="item.folder">{{ item.folder ? folderName(item.folder) : "—" }}</td>
            <td class="exceptions"><button v-if="item.folder" type="button" class="text-button" :aria-expanded="trustStart === item.folder" :disabled="trusting" :aria-label="`Trust a folder inside ${folderName(item.folder)}`" :title="`Pick a folder inside ${homePath(item.folder)} whose content is sent without masking, such as test data`" @click="chooseTrusted(item.folder)">Trust…</button></td>
            <td :class="terminalState(item).cls">{{ terminalState(item).text }}</td>
            <td>{{ item.services.length ? item.services.join(", ") : "—" }}</td>
            <td :title="`${item.requests ?? 0} AI requests`">{{ item.prompts }}</td>
            <td>{{ item.blocked }}</td>
            <td>{{ tokenOverhead(item.token_overhead, item.input_tokens, item.usage_overhead) }}</td>
            <td>{{ time(item.started) }}</td>
            <td>{{ time(item.last_activity) }}</td>
          </tr>
        </tbody>
      </table>
    </div>
    <template v-if="trustStart">
      <p class="trust-warning">Every value found in the folder you pick (e-mails, VINs, passwords, your patterns) reaches AI models <strong>without masking</strong>, wherever it appears. Pick a folder with fake data only, such as test fixtures, not the whole project.</p>
      <FolderBrowser :key="trustStart" title="Folder sent to AI without masking" action="Trust this folder" :files="false" :recent="recentFolders" :start="trustStart" @choose="(path) => setTrusted(path, true)" @close="trustStart = ''" />
    </template>
    <p v-if="trustMessage" id="trust-status" role="status" :class="trustMessage.kind">{{ trustMessage.text }}</p>
    <div v-if="trusted.length" id="trusted-folders">
      <p class="eyebrow trusted-title">Trusted folders · sent without masking</p>
      <ul class="chips path-chips" aria-label="Trusted folders">
        <li v-for="entry in trusted" :key="entry.path" class="chip trusted">
          <span :title="entry.path">{{ homePath(entry.path) }}</span>
          <small :class="{ 'status-block': entry.error }">{{ entry.error ? `${entry.error} · stays masked` : `${count(entry.files, "file")} · ${count(entry.values ?? 0, "value")}` }}</small>
          <button :aria-label="`Mask ${homePath(entry.path)} again`" title="Mask again" :disabled="trusting" @click="setTrusted(entry.path, false)">×</button>
        </li>
      </ul>
      <p class="note">Every value found in these folders is sent as is, however the AI gets it. Changed files are scanned again within a minute; files over 8 MB and folders over 50,000 files or 256 MB stay masked.</p>
    </div>
  </section>
</template>

<style scoped>
.folder { font-family: var(--mono); max-width: 320px; overflow: hidden; text-overflow: ellipsis; }
.command { white-space: nowrap; }
.close-terminal { margin: 0 0 0 6px; width: 22px; height: 22px; display: inline-grid; place-items: center; vertical-align: middle; padding: 0; border: 0; border-radius: 50%; background: transparent; color: var(--muted); font-size: 14px; line-height: 1; }
.close-terminal:hover { background: rgb(var(--ink-rgb) / .08); color: var(--text); }
.exceptions { white-space: nowrap; }
.exceptions button { margin: 0; padding: 2px 6px; color: var(--muted); text-decoration: underline 1px var(--line-strong); text-underline-offset: 3px; }
.exceptions button:hover:enabled, .exceptions button[aria-expanded=true] { color: var(--text); }
.trusted-title { margin: 20px 0 0; }
.trusted-title + .chips { margin-top: 10px; }
.chip.trusted { border-color: var(--warn); color: var(--warn); }
.trust-warning { margin: 12px 0 0; font-size: 12px; line-height: 1.6; }
.trust-warning, .trust-note { color: var(--warn); }
</style>
