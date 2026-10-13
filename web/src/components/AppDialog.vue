<script setup>
import { nextTick, ref, watch } from "vue";
import { answer, dialog } from "../dialog.js";

const element = ref(null);
const cancelButton = ref(null);
const confirmButton = ref(null);

watch(() => dialog.open, async (open) => {
  if (!open) {
    if (element.value?.open) element.value.close();
    return;
  }
  if (!element.value.open) element.value.showModal();
  await nextTick();
  (dialog.danger && cancelButton.value ? cancelButton.value : confirmButton.value)?.focus();
});

function backdrop(event) {
  const box = element.value.getBoundingClientRect();
  const outside = event.clientX < box.left || event.clientX > box.right || event.clientY < box.top || event.clientY > box.bottom;
  if (event.target === element.value && outside) answer(false);
}
</script>

<template>
  <dialog ref="element" id="app-dialog" class="app-dialog" :class="{ danger: dialog.danger }" aria-labelledby="app-dialog-title" aria-describedby="app-dialog-message" @cancel.prevent="answer(false)" @click="backdrop">
    <h2 id="app-dialog-title">{{ dialog.title }}</h2>
    <p id="app-dialog-message">{{ dialog.message }}</p>
    <div class="app-dialog-actions">
      <button v-if="dialog.cancel" ref="cancelButton" type="button" class="secondary compact" @click="answer(false)">{{ dialog.cancel }}</button>
      <button ref="confirmButton" type="button" class="compact" :class="dialog.danger ? 'danger-button' : 'primary'" @click="answer(true)">{{ dialog.confirm }}</button>
    </div>
  </dialog>
</template>

<style scoped>
.app-dialog { width: min(420px, calc(100% - 32px)); max-width: none; padding: 22px 24px 20px; border-radius: 16px; box-shadow: 0 30px 70px -30px rgb(0 0 0 / .9); }
.app-dialog h2 { margin: 0 0 8px; font-size: 16px; font-weight: 600; color: var(--text); }
.app-dialog p { margin: 0; font-size: 13px; line-height: 1.6; color: var(--muted); white-space: pre-line; }
.app-dialog.danger { border-color: rgb(240 117 96 / .35); }
.app-dialog-actions { display: flex; justify-content: flex-end; gap: 8px; margin-top: 20px; }
.app-dialog-actions button { margin: 0; }
.danger-button { border: 1px solid rgb(240 117 96 / .5); border-radius: 999px; background: var(--danger-soft); color: var(--danger); font-weight: 600; }
.danger-button:hover { background: rgb(240 117 96 / .2); border-color: var(--danger); }
</style>
