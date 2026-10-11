<script setup>
import { computed, reactive } from "vue";
import { applyRules, restoreDefaults, workspace } from "../workspace.js";
import CustomRules from "./CustomRules.vue";
import NamedFields from "./NamedFields.vue";
import RuleGroup from "./RuleGroup.vue";
import SecretPatterns from "./SecretPatterns.vue";

const open = reactive({ secrets: false, fields: false, email: false, guid: false, vin: false, pesel: false, custom: false });

const secretsStatus = computed(() => {
  const total = workspace.secretDetectors.length;
  const own = workspace.customRules.filter((rule) => rule.secret).length;
  return `${total - workspace.disabledSecrets.length}/${total} patterns${own ? ` · ${own} yours` : ""}`;
});
const fieldsStatus = computed(() => {
  const total = workspace.defaultFields.length;
  const own = workspace.customFields.length;
  return `${total - workspace.disabledFields.length}/${total} fields${own ? ` · ${own} yours` : ""}`;
});
const usesDefaults = computed(() => {
  const defaults = workspace.defaultRules;
  if (!defaults) return true;
  return Object.keys(workspace.rules).every((id) => workspace.rules[id] === Boolean(defaults[id])) && workspace.pesel === Boolean(defaults.pesel) && workspace.customRulesOn && !workspace.disabledFields.length && !workspace.disabledSecrets.length;
});
const detectors = [
  { id: "email", title: "Email addresses", description: "Anything shaped like name@domain.tld.", note: "" },
  { id: "guid", title: "GUIDs", description: "IDs such as 550e8400-e29b-41d4-a716-446655440000.", note: "Whole words only." },
  { id: "vin", title: "Vehicle numbers (VIN)", description: "17 letters and digits without I, O or Q.", note: "Whole words only." },
];
</script>

<template>
  <section class="workspace-card rules-card" aria-labelledby="rules-card-title">
    <div class="section-heading">
      <div><p class="eyebrow">RULES</p><h2 id="rules-card-title">What gets masked</h2></div>
      <div><button id="restore-defaults" class="secondary" :disabled="workspace.working || usesDefaults" @click="restoreDefaults">Restore defaults</button></div>
    </div>
    <p id="policy-note" class="note">{{ workspace.policyNote }}</p>
    <div class="rule-groups">
      <RuleGroup id="secrets" v-model:enabled="workspace.rules.secrets" v-model:open="open.secrets" title="Passwords & secrets" description="API keys, tokens and private keys in known formats, plus your own patterns." :status="secretsStatus" @change="applyRules">
        <SecretPatterns />
      </RuleGroup>
      <RuleGroup id="fields" v-model:enabled="workspace.rules.fields" v-model:open="open.fields" title="Named fields" description="Values after names such as email, phone or firstName." :status="fieldsStatus" @change="applyRules">
        <NamedFields />
      </RuleGroup>
      <RuleGroup v-for="detector in detectors" :id="detector.id" :key="detector.id" v-model:enabled="workspace.rules[detector.id]" v-model:open="open[detector.id]" :title="detector.title" :description="detector.description" @change="applyRules">
        <p class="pattern-label">Pattern</p>
        <code class="pattern">{{ workspace.detectorPatterns[detector.id] }}</code>
        <p v-if="detector.note" class="note">{{ detector.note }}</p>
      </RuleGroup>
      <RuleGroup id="pesel" v-model:enabled="workspace.pesel" v-model:open="open.pesel" title="PESEL numbers" description="Any 11-digit number on its own. Off by default." @change="applyRules">
        <p class="pattern-label">Pattern</p>
        <code class="pattern">{{ workspace.detectorPatterns.pesel }}</code>
        <p class="note">Whole numbers only. Numbers with a wrong checksum are masked too, because a typo does not make them less sensitive.</p>
      </RuleGroup>
      <RuleGroup id="rules" v-model:enabled="workspace.customRulesOn" v-model:open="open.custom" title="Your custom rules" description="Patterns from the Rule Builder, with their match counts." :status="`${workspace.customRules.length}/16 rules`" @change="applyRules">
        <CustomRules />
      </RuleGroup>
    </div>
    <p class="note">Restore defaults switches every group, built-in field and pattern back to its default. Your own fields, Rule Builder rules and protected folders stay as they are.</p>
  </section>
</template>

<style scoped>
.rules-card { margin: 0 0 24px; }
.rule-groups { margin-top: 16px; }
.pattern-label { margin: 0 0 6px; color: var(--muted); font-family: var(--mono); font-size: 11px; letter-spacing: .12em; text-transform: uppercase; }
.pattern { display: inline-block; max-width: 100%; overflow-wrap: anywhere; }
</style>
