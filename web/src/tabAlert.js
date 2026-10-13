import { watch } from "vue";
import { maskedCount, quietBlock } from "./events.js";
import { monitor } from "./monitor.js";

const ICONS = { normal: "/assets/icon.svg", masked: "/assets/icon-masked.svg", blocked: "/assets/icon-blocked.svg" };
const BLINK_MS = 600;

let seen = null;
let unseen = { count: 0, kind: "" };
let blinkTimer = null;
const title = document.title;

const attended = () => !document.hidden && document.hasFocus();

function setIcon(kind) {
  document.querySelector('link[rel="icon"]')?.setAttribute("href", ICONS[kind]);
}

function notable(event) {
  if (event.phase === "control" || quietBlock(event)) return false;
  const folders = Object.keys({ ...event.matches?.masked_folders, ...event.matches?.blocked_folders }).length;
  return event.status >= 400 || maskedCount(event) > 0 || folders > 0;
}

function blink(kind, flashes, settle) {
  clearInterval(blinkTimer);
  let step = 0;
  setIcon(kind);
  blinkTimer = setInterval(() => {
    step += 1;
    if (step < flashes * 2) {
      setIcon(step % 2 ? "normal" : kind);
      return;
    }
    clearInterval(blinkTimer);
    setIcon(settle);
  }, BLINK_MS);
}

function acknowledge() {
  if (!attended() || !unseen.count) return;
  unseen = { count: 0, kind: "" };
  document.title = title;
  clearInterval(blinkTimer);
  setIcon("normal");
}

export function watchTabAlerts() {
  watch(() => monitor.events, (events) => {
    const latest = Math.max(0, ...events.map((event) => event.id));
    if (seen === null || latest < seen) {
      seen = latest;
      return;
    }
    const fresh = events.filter((event) => event.id > seen && notable(event));
    seen = latest;
    if (!fresh.length) return;
    const kind = unseen.kind === "blocked" || fresh.some((event) => event.status >= 400) ? "blocked" : "masked";
    if (attended()) {
      blink(kind, 2, "normal");
      return;
    }
    unseen = { count: unseen.count + fresh.length, kind };
    document.title = `(${unseen.count}) ${title}`;
    blink(kind, 6, kind);
  });
  document.addEventListener("visibilitychange", acknowledge);
  window.addEventListener("focus", acknowledge);
}
