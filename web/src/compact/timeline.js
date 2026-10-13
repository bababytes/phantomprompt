import { reactive, watch } from "vue";
import { monitor } from "../monitor.js";
import { newDetections } from "./data.js";

const KEEP = 10 * 60 * 1000;
const seen = new Map();
let baselined = false;

export const timeline = reactive({ samples: [] });

export function clearTimeline() {
  timeline.samples = [];
  seen.clear();
}

watch(() => monitor.events, (events) => {
  const now = Date.now();
  const fresh = newDetections(seen, events, now, baselined);
  baselined = true;
  timeline.samples = [...timeline.samples.filter((sample) => now - sample.time < KEEP), ...fresh];
});
