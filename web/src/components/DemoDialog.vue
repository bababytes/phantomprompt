<script setup>
import { computed, ref, watch } from "vue";
import { refresh } from "../monitor.js";
import { headers, newSession, setStatus, workspace } from "../workspace.js";

const SAMPLE = "Please check vehicle TEST0000000000001.\nReference GUID: 123e4567-e89b-12d3-a456-426614174000.\nContact: test.user@example.test\nfirstName: Test\nlastName: User\nphone: +48 000 000 000\naddress: Example Street 1\npostcode: 00-000\nlicencePlate: TEST123\naccountNumber: TEST-ACCOUNT-001\ncardNumber: 4111 1111 1111 1111";
const TOKENS = /\[PP_(?:VIN|PESEL|GUID|EMAIL|FIELD|CUSTOM)_[a-z0-9]{6}\]/g;
const METRICS = [
  { id: "vin-count", label: "VINs masked" },
  { id: "guid-count", label: "GUIDs masked" },
  { id: "email-count", label: "Emails masked" },
  { id: "fields-count", label: "Fields masked" },
  { id: "custom-count", label: "Custom matches" },
  { id: "mask-time", label: "Masking time (ms)" },
  { id: "round-trip", label: "Round trip (ms)" },
];

const dialog = ref(null);
const input = ref(SAMPLE);
const received = ref({ placeholder: "The actual upstream receipt will appear here.", parts: [] });
const restored = ref({ placeholder: "Original values are restored using this session's memory.", text: "" });
const metrics = ref({});
const canSend = computed(() => !workspace.working && !workspace.rulesDirty && Boolean(workspace.session));

function resetPanels() {
  received.value = { placeholder: "Waiting for a request…", parts: [] };
  restored.value = { placeholder: "Waiting for a request…", text: "" };
  metrics.value = {};
}

watch(() => workspace.generation, resetPanels);

function receipt(text) {
  const parts = [];
  let previous = 0;
  for (const match of text.matchAll(TOKENS)) {
    parts.push({ text: text.slice(previous, match.index), mark: false }, { text: match[0], mark: true });
    previous = match.index + match[0].length;
  }
  parts.push({ text: text.slice(previous), mark: false });
  received.value = { placeholder: "", parts };
}

async function readStream(response) {
  const reader = response.body.getReader();
  const decoder = new TextDecoder();
  let buffer = "";
  let completed = false;
  try {
    while (!completed) {
      const { value, done } = await reader.read();
      buffer += decoder.decode(value, { stream: !done });
      let boundary;
      while ((boundary = buffer.indexOf("\n\n")) !== -1) {
        const frame = buffer.slice(0, boundary);
        buffer = buffer.slice(boundary + 2);
        let event = "message";
        const data = [];
        for (const line of frame.split("\n")) {
          if (line.startsWith("event:")) event = line.slice(6).trim();
          if (line.startsWith("data:")) data.push(line.slice(5).trimStart());
        }
        if (!data.length) continue;
        const payload = JSON.parse(data.join("\n"));
        if (event === "error") throw new Error("The stream was blocked or interrupted. No direct retry was made.");
        if (event === "receipt") receipt(payload.text);
        if (event === "delta") restored.value.text += payload.text;
        if (event === "done") completed = true;
      }
      if (buffer.length > 2 * 1024 * 1024) throw new Error("Stream event too large.");
      if (done) break;
    }
    if (!completed) throw new Error("The stream ended before completion.");
  } finally {
    await reader.cancel();
  }
}

async function send(unsupported = false) {
  if (workspace.rulesDirty || !workspace.session) {
    setStatus("Apply the masking rules before sending a request.", "error");
    return;
  }
  workspace.working = true;
  resetPanels();
  setStatus("Sending through the HTTPS proxy…");
  const started = performance.now();
  const text = input.value;
  const stream = workspace.stream;
  const controller = new AbortController();
  const timeout = setTimeout(() => controller.abort(), 45000);
  try {
    const payload = unsupported ? { image: "unsupported attachment" } : { text, stream };
    const post = () => fetch("/api/chat", { method: "POST", headers: headers(), body: JSON.stringify(payload), signal: controller.signal });
    let response = await post();
    if (response.status === 409) {
      await newSession();
      workspace.working = true;
      if (workspace.rulesDirty) throw new Error("The session expired and a new one could not be started. Reload this page.");
      setStatus("The demo session expired. Started a new one and sending again…");
      response = await post();
    }
    if (!response.ok) {
      if (unsupported && response.status === 422) {
        setStatus("Blocked before upstream: unsupported content (HTTP 422).", "success");
        return;
      }
      if (response.status >= 500) throw new Error(`The local proxy or test server failed (HTTP ${response.status}). Restart PhantomPrompt and reload this page. Clearing the session does not fix connection errors.`);
      throw new Error(`Request blocked (HTTP ${response.status}). Check the format or clear the session.`);
    }
    metrics.value = {
      "vin-count": response.headers.get("X-PhantomPrompt-Vin-Count"),
      "guid-count": response.headers.get("X-PhantomPrompt-guid-Count"),
      "email-count": response.headers.get("X-PhantomPrompt-email-Count"),
      "fields-count": response.headers.get("X-PhantomPrompt-fields-Count"),
      "custom-count": response.headers.get("X-PhantomPrompt-custom-Count"),
      "mask-time": response.headers.get("X-PhantomPrompt-Mask-Ms"),
    };
    restored.value = { placeholder: "", text: "" };
    if (stream) await readStream(response);
    else {
      const data = await response.json();
      receipt(data.received);
      restored.value.text = data.text;
    }
    if (restored.value.text !== text) throw new Error("Response differs from the input. Inspect the result.");
    setStatus("Upstream receipt confirmed. Restored reply matches your input.", "success");
  } catch (error) {
    setStatus(controller.signal.aborted ? "The demo request timed out after 45 seconds. Restart PhantomPrompt and reload this page. No direct retry was made." : error.message, "error");
  } finally {
    clearTimeout(timeout);
    metrics.value = { ...metrics.value, "round-trip": (performance.now() - started).toFixed(1) };
    workspace.working = false;
    refresh();
  }
}

defineExpose({ open: () => dialog.value.showModal() });
</script>

<template>
  <dialog id="demo-window" ref="dialog" aria-labelledby="demo-title">
    <div class="section-heading">
      <div><p class="eyebrow">LIVE HTTPS PROXY DEMO</p><h2 id="demo-title">See what leaves your machine</h2></div>
      <button id="close-demo" class="secondary" @click="dialog.close()">Close demo</button>
    </div>
    <p class="note">Uses the rules from your main workspace. The test server is local; no AI account is needed.</p>
    <div class="flow" aria-label="Request flow"><span>01 · Your input</span><b>→</b><span>02 · HTTPS proxy</span><b>→</b><span>03 · Test server</span><b>→</b><span>04 · Restored reply</span></div>
    <section class="panels">
      <article class="panel">
        <div class="panel-title"><span class="step">01</span><h2>Your original text</h2><span class="badge">LOCAL</span></div>
        <label class="sr-only" for="input">Text to mask</label>
        <textarea id="input" v-model="input" spellcheck="false" maxlength="20000"></textarea>
        <div class="panel-bottom"><span>Synthetic sample · edit freely</span><button id="sample" class="text-button" :disabled="workspace.working" @click="input = SAMPLE">Reset sample</button></div>
      </article>
      <article class="panel">
        <div class="panel-title"><span class="step">02</span><h2>What the server received</h2><span class="badge protected">MASKED</span></div>
        <pre v-if="received.placeholder" id="received" class="placeholder">{{ received.placeholder }}</pre>
        <pre v-else id="received"><template v-for="(part, index) in received.parts" :key="index"><mark v-if="part.mark">{{ part.text }}</mark><template v-else>{{ part.text }}</template></template></pre>
        <div class="panel-bottom"><span>Read from the HTTPS test server response</span></div>
      </article>
      <article class="panel">
        <div class="panel-title"><span class="step">03</span><h2>What you get back</h2><span class="badge">RESTORED</span></div>
        <pre id="restored" :class="{ placeholder: restored.placeholder }">{{ restored.placeholder || restored.text }}</pre>
        <div class="panel-bottom"><span>Mapping stays in the proxy process</span></div>
      </article>
    </section>
    <section class="actions">
      <button id="send" class="primary" :disabled="!canSend" @click="send()">Run protected request <span>→</span></button>
      <button id="clear" class="secondary" :disabled="workspace.working" @click="newSession">Clear session</button>
      <label class="stream-option"><input id="stream" v-model="workspace.stream" type="checkbox" :disabled="workspace.working"> Stream the reply in small chunks</label>
      <button id="blocked" class="text-button" :disabled="workspace.working" @click="send(true)">Test unsupported content</button>
      <span id="status" role="status" aria-live="polite" :class="workspace.status.kind">{{ workspace.status.text }}</span>
    </section>
    <section class="metrics" aria-label="Last request metrics">
      <div v-for="metric in METRICS" :key="metric.id"><span :id="metric.id">{{ metrics[metric.id] ?? "—" }}</span><small>{{ metric.label }}</small></div>
    </section>
    <aside>
      <strong>A real proxy. A deliberately simple test server.</strong>
      <p>This demo uses HTTP CONNECT, two verified TLS connections and a local echo server. It does not call an AI provider. The server returns the exact text it received; it never receives the token map. Streaming deliberately splits tokens into small chunks.</p>
      <p>Only requests sent through this proxy are protected. This demo does not enable protection for other browser tabs, Claude Code, ChatGPT or your whole system. Restarting the proxy loses all mappings. The time above is a measurement of this request, not a latency guarantee.</p>
    </aside>
  </dialog>
</template>
