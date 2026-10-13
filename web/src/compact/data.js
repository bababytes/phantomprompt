import { maskedCount } from "../events.js";

const SHELLS = new Set(["zsh", "bash", "fish", "sh", "ksh", "tcsh", "csh", "dash", "nu", "pwsh", "xonsh", "elvish"]);

export const terminalTitle = (label) => (SHELLS.has(label) ? label : "Terminal");

function activityPoints(events, limit) {
  return events.slice(0, limit).reverse().map((event) => ({ id: event.id, values: maskedCount(event), blocked: event.status >= 400 }));
}

export function terminalActivity(events, session, limit) {
  if (!session.folder) return [];
  const started = Date.parse(session.started);
  return activityPoints(events.filter((event) => event.folder === session.folder && event.phase !== "control" && Date.parse(event.time) >= started), limit);
}
