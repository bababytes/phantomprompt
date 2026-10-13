import { reactive } from "vue";

export const dialog = reactive({ open: false, title: "", message: "", confirm: "OK", cancel: "", danger: false });
let settle = null;

function show(options) {
  settle?.(false);
  Object.assign(dialog, { title: "", message: "", confirm: "OK", cancel: "", danger: false }, options, { open: true });
  return new Promise((resolve) => {
    settle = resolve;
  });
}

export const ask = (options) => show({ cancel: "Cancel", ...options });

export const tell = (options) => show(options).then(() => undefined);

export function answer(value) {
  dialog.open = false;
  const done = settle;
  settle = null;
  done?.(value);
}
