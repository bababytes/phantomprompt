import { KIND_KEYS, kindStyle, quietBlock, statusInfo } from "./events.js";

export function eventKinds(event) {
  return Object.entries(event.matches?.new_kinds ?? {}).filter(([, n]) => n > 0).map(([kind, n]) => ({ kind, n, ...kindStyle(kind) })).sort((a, b) => b.n - a.n);
}

export function eventFolders(event) {
  const list = (folders, action) => Object.entries(folders ?? {}).map(([folder, n]) => ({ folder, action, n }));
  return [...list(event.matches?.blocked_folders, "blocked"), ...list(event.matches?.masked_folders, "masked")];
}

export function kindTotals(events) {
  const totals = new Map(KIND_KEYS.map((kind) => [kind, 0]));
  for (const event of events) {
    for (const [kind, n] of Object.entries(event.matches?.new_kinds ?? {})) totals.set(kind, (totals.get(kind) ?? 0) + n);
  }
  return [...totals].filter(([, count]) => count > 0).map(([kind, count]) => ({ kind, count })).sort((a, b) => b.count - a.count);
}

export function folderTotals(events) {
  const folders = new Map();
  for (const event of events) {
    for (const [action, counts] of [["masked", event.matches?.masked_folders], ["blocked", event.matches?.blocked_folders]]) {
      for (const [folder, n] of Object.entries(counts ?? {})) {
        const row = folders.get(folder) ?? { masked: 0, blocked: 0 };
        row[action] += n;
        folders.set(folder, row);
      }
    }
  }
  return folders;
}

export function blockedSince(events, since) {
  return events.filter((event) => event.status >= 400 && event.phase !== "control" && !quietBlock(event) && Date.parse(event.time) >= since).reduce((sum, event) => sum + 1 + (event.repeats ?? 0), 0);
}

const GROUPS = [
  { id: "secret", label: "Passwords & secrets", color: kindStyle("secret").color, kinds: ["secret"] },
  { id: "custom", label: "Custom rules", color: kindStyle("custom").color, kinds: ["custom"] },
  { id: "fields", label: "Fields", color: kindStyle("fields").color, kinds: ["fields"] },
  { id: "personal", label: "Personal data & IDs", color: kindStyle("email").color, kinds: ["email", "vin", "pesel", "guid"] },
  { id: "file", label: "Protected files", color: kindStyle("file").color, kinds: ["file"] },
];

const MAX_CHIPS = 6;

function limited(chips) {
  if (chips.length <= MAX_CHIPS) return chips;
  const rest = chips.slice(MAX_CHIPS - 1);
  return [...chips.slice(0, MAX_CHIPS - 1), { kind: "more", short: `+${rest.length} more`, n: "", color: "#8a9690", title: rest.map((chip) => `${chip.short} ${chip.n}`).join(", ") }];
}

function namedChips(group, event) {
  if (group.id === "custom") {
    return Object.entries(event.rules ?? {}).filter(([, use]) => !use.secret && use.values > 0).sort((a, b) => b[1].values - a[1].values).map(([name, use]) => ({ kind: `rule:${name}`, short: name, n: use.values, color: group.color }));
  }
  if (group.id === "fields") {
    return Object.entries(event.matches?.new_field_names ?? {}).filter(([, n]) => n > 0).sort((a, b) => b[1] - a[1]).map(([name, n]) => ({ kind: `field:${name}`, short: name, n, color: group.color }));
  }
  return [];
}

export function eventRows(event) {
  const status = statusInfo(event);
  const row = (id, label, cls, color, kinds = [], folders = []) => ({ key: `${event.id}:${id}`, label, cls, color, kinds, folders });
  if (event.phase === "control") return [row("control", status.text, status.cls, "")];
  const rows = [];
  if (event.status >= 400 || status.cls === "status-warn") rows.push(row("status", status.text, status.cls, ""));
  const kinds = eventKinds(event);
  const folders = eventFolders(event);
  for (const group of GROUPS) {
    const generic = kinds.filter((kind) => group.kinds.includes(kind.kind));
    const named = namedChips(group, event);
    const chips = limited(generic.length && named.length ? named : generic);
    const masked = group.id === "file" ? folders.filter((entry) => entry.action === "masked") : [];
    if (chips.length || masked.length) rows.push(row(group.id, group.label, "", group.color, chips, masked));
  }
  const blocked = folders.filter((entry) => entry.action === "blocked");
  if (blocked.length) rows.push(row("folder", "Folder access blocked", "status-block", "", [], blocked));
  return rows.length ? rows : [row("status", status.text, status.cls, "")];
}

export function newDetections(seen, events, now, baselined) {
  const fresh = [];
  const present = new Set();
  for (const event of events) {
    if (event.phase === "control") continue;
    present.add(event.id);
    const current = { kinds: { ...(event.matches?.new_kinds ?? {}) }, blocked: event.status >= 400 && !quietBlock(event) ? 1 + (event.repeats ?? 0) : 0 };
    const previous = seen.get(event.id);
    seen.set(event.id, current);
    const kinds = {};
    for (const [kind, n] of Object.entries(current.kinds)) {
      const added = n - (previous?.kinds[kind] ?? 0);
      if (added > 0) kinds[kind] = added;
    }
    const blocked = Math.max(current.blocked - (previous?.blocked ?? 0), 0);
    if (!Object.keys(kinds).length && !blocked) continue;
    fresh.push({ time: previous && baselined ? now : Math.min(now, Date.parse(event.time)), kinds, blocked, adapter: event.adapter });
  }
  for (const id of seen.keys()) {
    if (!present.has(id)) seen.delete(id);
  }
  return fresh;
}
