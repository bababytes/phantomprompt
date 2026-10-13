<script setup>
import { computed } from "vue";
import { SECTIONS, showSection } from "../layout.js";

const props = defineProps({ section: { type: String, required: true } });
const label = computed(() => SECTIONS.find((item) => item.id === props.section)?.label ?? "");
</script>

<template>
  <button type="button" class="section-link" :title="`Open ${label} on the full page`" :aria-label="`Open ${label} on the full page`" @click="showSection(section)">
    <svg viewBox="0 0 16 16" aria-hidden="true"><path d="M5.5 10.5 10.5 5.5M6.5 5.5h4v4" /></svg>
  </button>
</template>
