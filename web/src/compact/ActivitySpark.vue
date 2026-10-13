<script setup>
import { computed } from "vue";

const props = defineProps({ points: { type: Array, required: true }, slots: { type: Number, required: true } });
const peak = computed(() => Math.max(1, ...props.points.map((point) => point.values)));
const height = (point) => (point.blocked ? "45%" : `${Math.max(14, (point.values / peak.value) * 100)}%`);
</script>

<template>
  <span class="spark" :style="{ '--slots': slots }" aria-hidden="true">
    <span v-for="n in slots - points.length" :key="`empty-${n}`" class="spark-slot empty"></span>
    <span v-for="point in points" :key="point.id" class="spark-slot" :class="{ blocked: point.blocked, quiet: !point.blocked && !point.values }"><i :style="{ height: height(point) }"></i></span>
  </span>
</template>

<style scoped>
.spark { display: inline-grid; grid-template-columns: repeat(var(--slots), 3px); align-items: end; gap: 2px; height: 18px; vertical-align: middle; }
.spark-slot { display: flex; align-items: flex-end; height: 100%; }
.spark-slot i { display: block; width: 100%; border-radius: 1px; background: var(--accent); box-shadow: 0 0 5px rgb(76 201 149 / .7); }
.spark-slot.blocked i { background: var(--danger); box-shadow: 0 0 5px rgb(240 117 96 / .7); }
.spark-slot.quiet i { background: var(--faint); box-shadow: none; }
.spark-slot.empty::after { content: ""; display: block; width: 100%; height: 1px; background: rgb(var(--ink-rgb) / .12); }
</style>
