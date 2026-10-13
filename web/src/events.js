export const SERVICES = { "copilot-cli": "GitHub Copilot CLI", "claude-code": "Claude Code", "codex-cli": "Codex CLI", demo: "Local demo", "openai-chat": "OpenAI API", "anthropic-messages": "Anthropic API" };

export const RESULTS = {
  accepted: "Masked",
  stream_complete: "Masked · reply restored",
  masked_and_restored: "Masked · reply restored",
  passed_without_restore: "Sent · reply not restored",
  sensitive_value_outside_prompt: "Blocked · protected value outside the prompt",
  attachments_not_supported: "Blocked · attachment",
  unknown_token_in_input: "Blocked · unknown token",
  new_content_type_masked: "Masked · new content type (check for a PhantomPrompt update)",
  secret_kept_masked_in_tool: "Secret kept masked in a shell or web command",
  placeholders_not_restored: "Reply · placeholders not restored",
  unsupported_content: "Blocked · unknown request format",
  masking_limit: "Blocked · masking limit",
  tool_arguments_masked: "Masked · tool call to GitHub",
  model_format_not_supported_yet: "Blocked · model format not supported yet (choose a Claude model)",
  session_expired_restart_cli: "Blocked · session expired, restart the CLI",
  sensitive_value_not_masked_yet: "Blocked · protected value in an unsupported channel",
  upgrade_not_supported: "Blocked · websocket upgrade",
  body_limit: "Blocked · request too large",
  body_limit_or_encoding_error: "Blocked · request too large or unreadable",
  masked_body_limit: "Blocked · masked request too large",
  invalid_compression: "Blocked · broken compression",
  unsupported_encoding: "Blocked · unsupported compression",
  unsupported_content_or_masking_limit: "Blocked · unknown format or masking limit",
  session_expired_or_wrong_origin: "Blocked · session expired",
  session_unavailable: "Blocked · session unavailable",
  concurrency_limit: "Blocked · too many requests at once",
  route_or_method_not_supported: "Blocked · route not supported",
  upstream_TLS_or_connection_failed: "Failed · AI service unreachable",
  upstream_rejected_request: "Failed · AI service rejected the request",
  unsupported_response_content: "Failed · reply format unknown",
  unsupported_upstream_response: "Failed · reply format unknown",
  compressed_upstream_response_not_supported: "Failed · compressed reply",
  response_limit: "Failed · reply too large",
  stream_interrupted: "Failed · stream interrupted",
  protection_enabled: "Protection turned on",
  protection_disabled: "Protection turned OFF",
};

export const COUNT_LABELS = { vin: "VIN", guid: "GUID", email: "email", fields: "field", custom: "custom", pesel: "PESEL", secret: "secret/password" };

export function statusInfo(event) {
  if (event.phase === "control") return { text: RESULTS[event.reason] ?? event.reason, cls: event.reason === "protection_disabled" ? "status-warn" : "status-ok" };
  if (event.reason === "passed_without_restore") return { text: event.status >= 400 ? `AI service answered ${event.status} · passed to the tool` : "Sent · reply not restored", cls: "status-warn" };
  if (quietBlock(event)) {
    const { place } = blockInfo(event, []);
    return { text: `${place[0].toUpperCase()}${place.slice(1)} stopped · protected value kept on this Mac${event.repeats ? ` ×${event.repeats + 1}` : ""}`, cls: "status-quiet" };
  }
  if (event.status >= 400) {
    const label = RESULTS[event.reason] ?? `Blocked · ${event.reason}`;
    const text = label.startsWith("Blocked") || label.startsWith("Failed") ? label : `Blocked · ${label}`;
    return { text: event.repeats ? `${text} ×${event.repeats + 1}` : text, cls: "status-block" };
  }
  if (event.unrestored > 0) return { text: `⚠ ${event.unrestored} placeholder(s) not restored in the reply`, cls: "status-warn" };
  if (event.matches.attachments > 0) return { text: `⚠ ${event.matches.attachments} image(s) or file(s) sent without checking`, cls: "status-warn" };
  const blockedFolders = Object.keys(event.matches.blocked_folders ?? {});
  if (blockedFolders.length) return { text: `🚫 Folder access blocked (${blockedFolders.join(", ")})`, cls: "status-secret" };
  if ((event.matches.secret ?? 0) > 0) {
    const unique = event.matches.new_kinds?.secret ?? 0;
    return { text: unique ? `🔒 Password/secret masked (${unique})` : "🔒 Password/secret masked", cls: "status-secret" };
  }
  return { text: "Protected", cls: "status-ok" };
}

function kindList(kinds) {
  return Object.entries(kinds).filter(([kind, count]) => COUNT_LABELS[kind] && count > 0).map(([kind, count]) => `${COUNT_LABELS[kind]} ${count}`).join(", ");
}

export function maskedValues(event) {
  const unique = event.matches.new_kinds;
  if (!unique) return [...folderAccess(event), kindList(event.matches)].filter(Boolean).join(" · ") || "—";
  const typed = event.matches.typed_kinds ?? {};
  const context = event.matches.context_kinds ?? {};
  const found = Object.fromEntries(Object.entries(unique).map(([kind, n]) => [kind, n - (typed[kind] ?? 0) - (context[kind] ?? 0)]));
  const sources = [["you", typed], ["found by AI", found], [SERVICES[event.adapter] ?? "tool", context]].map(([source, kinds]) => [source, kindList(kinds)]).filter(([, list]) => list);
  return [...folderAccess(event), ...sources.map(([source, list]) => `${source}: ${list}`)].join(" · ") || "—";
}

export function folderAccess(event) {
  const access = (folders, action) => Object.entries(folders ?? {}).map(([folder, n]) => (n > 1 ? `${folder} ${action} ×${n}` : `${folder} ${action}`));
  return [...access(event.matches.blocked_folders, "blocked"), ...access(event.matches.masked_folders, "masked")];
}

const KINDS = {
  secret: { label: "Passwords & secrets", short: "Secrets", one: "a password or secret", color: "#b896f2" },
  email: { label: "E-mails", short: "E-mails", one: "an e-mail address", color: "#62b6f0" },
  guid: { label: "GUIDs", short: "GUIDs", one: "a GUID", color: "#9aa7ff" },
  vin: { label: "VINs", short: "VINs", one: "a VIN", color: "#f07560" },
  pesel: { label: "PESEL numbers", short: "PESEL", one: "a PESEL number", color: "#e88fc7" },
  custom: { label: "Custom words", short: "Custom", one: "one of your custom words", color: "#4cc995" },
  fields: { label: "Named fields", short: "Fields", one: "a protected field value", color: "#6fd6c8" },
  file: { label: "Protected file lines", short: "File lines", one: "a line from a protected file", color: "#e3b25a" },
};

export const KIND_KEYS = Object.keys(KINDS);

export function kindStyle(kind) {
  return KINDS[kind] ?? { label: kind, short: kind, color: "#8a9690" };
}

export function maskedCount(event) {
  return Object.values(event.matches?.new_kinds ?? {}).reduce((sum, n) => sum + n, 0);
}

export const quietBlock = (event) => event.phase !== "control" && event.status >= 400 && event.reason === "sensitive_value_not_masked_yet" && !conversationRoute(event.route ?? "");

const conversationRoute = (route) => route.startsWith("/v1/messages") || route.endsWith("/chat/completions") || route.endsWith("/responses");

const NOTE_KINDS = { email: "email", secret: "secret", field: "fields", fields: "fields", file: "file", custom: "custom", vin: "vin", guid: "guid", pesel: "pesel" };

function sidePlace(route, where) {
  if (/telemetry|metrics|analytics|events?$|logs?$/.test(route)) return "telemetry";
  return where.startsWith("query parameter") ? "web address" : "side request";
}

function noteParts(note = "") {
  const body = /^protected (\w+) value at \$\.?(.*)$/.exec(note);
  if (body) return { kind: body[1], where: body[2] || "request body" };
  const query = /^protected (\w+) value in (query parameter .+)$/.exec(note);
  if (query) return { kind: query[1], where: query[2] };
  const stream = /^line\[\]\.(.*) (\w+)$/.exec(note);
  if (stream) return { kind: stream[2], where: stream[1] };
  return { kind: "", where: note };
}

export function blockInfo(event, discovery) {
  if (event.phase === "control" || event.status < 400) return null;
  const route = event.route ?? "";
  if (event.reason === "passed_without_restore") return { place: "AI service error", kind: "", where: "", affects: true, effect: "The AI service answered with an error and the tool got it unchanged" };
  const moment = Date.parse(event.time);
  const shape = discovery.filter((item) => item.note && item.path === route && Date.parse(item.time) <= moment + 5000).at(-1);
  const { kind, where } = noteParts(shape?.note);
  const found = { kind: NOTE_KINDS[kind] ?? "", where };
  if (conversationRoute(route)) return { ...found, place: "AI request", affects: true, effect: "This AI request was not sent, so the tool got an error for it; nothing protected left your Mac" };
  const place = sidePlace(route, where);
  const why = found.kind ? `A value in this ${place} has the same format as ${kindStyle(found.kind).one}. It may only look like one, for example an ID or a number, and not be your data; PhantomPrompt kept it on this Mac to be safe.` : "";
  return { ...found, place, why, affects: false, effect: "Side traffic, not part of the conversation: AI answers are not affected" };
}
