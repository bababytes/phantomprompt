const RECENT_TRAFFIC_MS = 15_000;

export function serviceActive(item, sessions = [], now = Date.now()) {
  const inOpenTerminal = sessions.some((session) => session.active && session.services?.includes(item.service));
  return inOpenTerminal || now - new Date(item.last_seen).getTime() < RECENT_TRAFFIC_MS;
}

export function serviceState(item, sessions = [], now = Date.now()) {
  if (item.protected) {
    if (!item.last_seen) return { text: "Protected · waiting for traffic", cls: "status-quiet" };
    return serviceActive(item, sessions, now) ? { text: "Protected · active", cls: "status-ok" } : { text: "Protected · idle", cls: "status-quiet" };
  }
  return item.blocked ? { text: "Not supported yet · blocked", cls: "status-block" } : { text: "Not supported yet · not protected", cls: "status-warn" };
}
