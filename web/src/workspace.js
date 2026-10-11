import { reactive } from "vue";
import { access, showLocked } from "./config.js";
import { refresh } from "./monitor.js";

export const workspace = reactive({
  session: "",
  rules: { vin: true, guid: true, email: true, fields: true, secrets: true },
  pesel: false,
  stream: true,
  customFields: [],
  disabledFields: [],
  disabledSecrets: [],
  protectedPaths: [],
  blockedPaths: [],
  customRules: [],
  customRulesOn: true,
  defaultFields: [],
  secretDetectors: [],
  detectorPatterns: {},
  defaultRules: null,
  draftSecret: false,
  rulesDirty: true,
  working: false,
  generation: 0,
  status: { text: "Starting a private session…", kind: "" },
  policyNote: "Loading active rules…",
});

export function headers() {
  return { "Content-Type": "application/json", "X-PhantomPrompt-Access": access, "X-PhantomPrompt-Session": workspace.session };
}

export function setStatus(text, kind = "") {
  workspace.status = { text, kind };
}

export function policy(customRules = workspace.customRules) {
  return { ...workspace.rules, pesel: workspace.pesel, custom_fields: workspace.customFields, disabled_fields: workspace.disabledFields, disabled_secrets: workspace.disabledSecrets, custom_rules: customRules, custom_off: !workspace.customRulesOn, protected_paths: workspace.protectedPaths, blocked_paths: workspace.blockedPaths.filter((path) => workspace.protectedPaths.includes(path)) };
}

export const fieldKey = (name) => name.toLowerCase().replace(/[_-]/g, "");

function adopt(saved) {
  for (const id of Object.keys(workspace.rules)) workspace.rules[id] = Boolean(saved[id]);
  workspace.pesel = Boolean(saved.pesel);
  workspace.customFields = saved.custom_fields ?? [];
  workspace.disabledFields = saved.disabled_fields ?? [];
  workspace.disabledSecrets = saved.disabled_secrets ?? [];
  workspace.customRules = saved.custom_rules ?? [];
  workspace.customRulesOn = !saved.custom_off;
  workspace.protectedPaths = saved.protected_paths ?? [];
  workspace.blockedPaths = saved.blocked_paths ?? [];
}

async function loadRules() {
  const response = await fetch("/rules", { headers: headers() });
  if (response.status === 401) showLocked();
  if (!response.ok) throw new Error("Cannot load masking rules.");
  const rules = await response.json();
  workspace.defaultFields = rules.defaults;
  workspace.secretDetectors = rules.secrets ?? [];
  workspace.detectorPatterns = rules.patterns ?? {};
  workspace.defaultRules = rules.startup;
  adopt(rules.policy);
}

export function restoreDefaults() {
  const defaults = workspace.defaultRules ?? {};
  for (const id of Object.keys(workspace.rules)) workspace.rules[id] = Boolean(defaults[id]);
  workspace.pesel = Boolean(defaults.pesel);
  workspace.customRulesOn = true;
  workspace.disabledFields = [];
  workspace.disabledSecrets = [];
  return applyRules();
}

export async function applyRules() {
  workspace.working = true;
  try {
    const { rules } = workspace;
    if (!rules.vin && !rules.guid && !rules.email && !rules.fields && !rules.secrets && !workspace.pesel && !(workspace.customRulesOn && workspace.customRules.length) && !workspace.protectedPaths.length) rules.vin = true;
    const response = await fetch("/api/rules", { method: "PUT", headers: headers(), body: JSON.stringify(policy()), signal: AbortSignal.timeout(10000) });
    if (!response.ok) throw new Error((await response.text()).trim() || `Rules rejected (HTTP ${response.status}).`);
    adopt(await response.json());
    await newSession();
    refresh();
    return true;
  } catch (error) {
    try { await loadRules(); } catch {}
    setStatus(error.message, "error");
    return false;
  } finally {
    workspace.working = false;
  }
}

export async function newSession() {
  workspace.rulesDirty = true;
  workspace.working = true;
  try {
    if (workspace.session) {
      const closed = await fetch("/sessions", { method: "DELETE", headers: headers() });
      if (!closed.ok) throw new Error("Cannot clear the previous session.");
      workspace.session = "";
    }
    const { rules } = workspace;
    if (!rules.vin && !rules.guid && !rules.email && !rules.fields && !rules.secrets && !workspace.pesel && !(workspace.customRulesOn && workspace.customRules.length) && !workspace.protectedPaths.length) rules.vin = true;
    const response = await fetch("/sessions", { method: "POST", headers: headers(), body: JSON.stringify(policy()) });
    if (!response.ok) throw new Error("Cannot start a session. Restart the demo if needed.");
    workspace.session = (await response.json()).session;
    workspace.rulesDirty = false;
    workspace.generation++;
    const active = Object.keys(rules).filter((id) => rules[id]).map((id) => id.toUpperCase());
    if (workspace.pesel) active.push("PESEL");
    if (workspace.customRulesOn && workspace.customRules.length) active.push(`${workspace.customRules.length} CUSTOM REGEX`);
    workspace.policyNote = `Active: ${active.join(" + ")}. Rules apply to all protected traffic (protected terminals and the demo) and are saved on this Mac.`;
    setStatus("Ready. Session mappings live only in RAM.");
  } catch (error) {
    setStatus(error.message, "error");
  } finally {
    workspace.working = false;
  }
}

export async function initialize() {
  workspace.working = true;
  try {
    await loadRules();
    await newSession();
  } catch (error) {
    setStatus(error.message, "error");
  }
}

export async function labRequest(path, body) {
  const response = await fetch(path, { method: "POST", headers: headers(), body: JSON.stringify(body), signal: AbortSignal.timeout(10000) });
  if (!response.ok) throw new Error((await response.text()).trim() || `Rule request rejected (HTTP ${response.status}).`);
  return response.json();
}
