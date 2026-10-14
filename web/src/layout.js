import { nextTick, reactive, watch, watchEffect } from "vue";

const STORAGE = "phantomprompt.compactView";
const THEME = "phantomprompt.theme";
const BAR = "phantomprompt.barSeconds";
const PANEL = "phantomprompt.openPanel";
export const BAR_SECONDS = [1, 4, 10, 60];
export const PANELS = ["section-rules", "section-test", "section-history"];

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

function loadPanel() {
  try {
    const panel = localStorage.getItem(PANEL);
    return PANELS.includes(panel) ? panel : null;
  } catch {
    return null;
  }
}

export const layout = reactive({ compact: load(), light: loadTheme(), barSeconds: loadBar(), panel: loadPanel() });

watch(() => layout.panel, (panel) => {
  try {
    if (panel) localStorage.setItem(PANEL, panel);
    else localStorage.removeItem(PANEL);
  } catch {}
});

watch(() => layout.barSeconds, (seconds) => {
  try {
    localStorage.setItem(BAR, String(seconds));
  } catch {}
});

watchEffect(() => {
  document.documentElement.dataset.theme = layout.light ? "light" : "dark";
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
  if (PANELS.includes(id)) layout.panel = id;
  await nextTick();
  document.getElementById(PANELS.includes(id) ? "section-more" : id)?.scrollIntoView({ block: "start" });
}
