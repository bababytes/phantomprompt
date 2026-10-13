import { nextTick, reactive, watch, watchEffect } from "vue";

const STORAGE = "phantomprompt.compactView";
const THEME = "phantomprompt.theme";
const BAR = "phantomprompt.barSeconds";
export const BAR_SECONDS = [1, 4, 10, 60];

export const SECTIONS = [
  { id: "section-status", label: "Status" },
  { id: "section-connect", label: "Connect" },
  { id: "section-rules", label: "Rules" },
  { id: "section-test", label: "Test" },
  { id: "section-history", label: "History" },
];

function load() {
  try {
    return localStorage.getItem(STORAGE) === "on";
  } catch {
    return false;
  }
}

function loadTheme() {
  try {
    return localStorage.getItem(THEME) === "light";
  } catch {
    return false;
  }
}

function loadBar() {
  try {
    const seconds = Number(localStorage.getItem(BAR));
    return BAR_SECONDS.includes(seconds) ? seconds : 4;
  } catch {
    return 4;
  }
}

export const layout = reactive({ compact: load(), light: loadTheme(), barSeconds: loadBar() });

watch(() => layout.barSeconds, (seconds) => {
  try {
    localStorage.setItem(BAR, String(seconds));
  } catch {}
});

watchEffect(() => {
  document.documentElement.dataset.theme = layout.compact && layout.light ? "light" : "dark";
});

watch(() => layout.light, (on) => {
  try {
    localStorage.setItem(THEME, on ? "light" : "dark");
  } catch {}
});

watch(() => layout.compact, (on) => {
  try {
    localStorage.setItem(STORAGE, on ? "on" : "off");
  } catch {}
  window.scrollTo(0, 0);
});

export async function showSection(id) {
  layout.compact = false;
  await nextTick();
  document.getElementById(id)?.scrollIntoView({ block: "start" });
}
