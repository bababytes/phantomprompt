const KINDS = { email: "e-mail", guid: "GUID", vin: "VIN", pesel: "PESEL", secret: "password / secret", custom: "custom rule" };

export function valueRows(events, services) {
  const groups = new Map();
  const add = (key, label, kind, own, event) => {
    if (own <= 0) return;
    const folder = event.folder ?? "";
    key = `${folder}|${key}`;
    const row = groups.get(key) ?? { key, folder, label, kind, count: 0, services: new Set(), last: 0 };
    row.count += own;
    row.services.add(services[event.adapter] ?? event.adapter ?? "—");
    row.last = Math.max(row.last, Date.parse(event.time));
    groups.set(key, row);
  };
  for (const event of events) {
    for (const [folder, n] of Object.entries(event.matches.blocked_folders ?? {})) add(`folder:blocked:${folder}`, folder, "blocked folder", n, event);
    for (const [folder, n] of Object.entries(event.matches.masked_folders ?? {})) add(`folder:masked:${folder}`, folder, "masked folder", n, event);
    const unique = event.matches.new_kinds;
    if (!unique) continue;
    const context = event.matches.context_kinds ?? {};
    const byRules = { custom: 0, secret: 0 };
    for (const [name, use] of Object.entries(event.rules ?? {})) {
      byRules[use.secret ? "secret" : "custom"] += use.values;
      add(`rule:${name}`, name, use.secret ? "secret rule" : "custom rule", use.values, event);
    }
    for (const [kind, total] of Object.entries(unique)) {
      if (kind === "fields" || kind === "file") continue;
      add(`kind:${kind}`, KINDS[kind] ?? kind, "type", total - (context[kind] ?? 0) - (byRules[kind] ?? 0), event);
    }
    let contextFields = context.fields ?? 0;
    for (const [name, total] of Object.entries(event.matches.new_field_names ?? {})) {
      const fromContext = Math.min(contextFields, total);
      contextFields -= fromContext;
      add(`field:${name}`, name, "field", total - fromContext, event);
    }
  }
  return [...groups.values()].map((row) => ({ ...row, services: [...row.services].join(", ") }));
}
