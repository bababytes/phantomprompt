import assert from "node:assert/strict";
import { test } from "node:test";
import { blockedSince, eventRows, kindTotals, newDetections, terminalActivity, terminalTitle } from "../src/compact/data.js";

const chips = (row) => row.kinds.map((chip) => `${chip.short} ${chip.n}`.trim());

test("a prompt is split into one activity row per category, with rule and field names", () => {
  const event = {
    id: 7,
    status: 200,
    phase: "request",
    rules: { "Project codes": { values: 2, hits: 5, secret: false }, "AWS key": { values: 1, hits: 1, secret: true } },
    matches: { new_kinds: { secret: 1, custom: 2, fields: 2, email: 3, guid: 1, file: 1 }, new_field_names: { firstName: 1, accountNumber: 1 }, masked_folders: { "~/work": 2 }, blocked_folders: { "~/.ssh": 1 } },
  };
  const rows = eventRows(event).map((row) => [row.label, chips(row), row.folders.map((entry) => `${entry.folder} ${entry.action}`)]);
  assert.deepEqual(rows, [
    ["Passwords & secrets", ["Secrets 1"], []],
    ["Custom rules", ["Project codes 2"], []],
    ["Fields", ["firstName 1", "accountNumber 1"], []],
    ["Personal data & IDs", ["E-mails 3", "GUIDs 1"], []],
    ["Protected files", ["File lines 1"], ["~/work masked"]],
    ["Folder access blocked", [], ["~/.ssh blocked"]],
  ]);
});

test("long field lists end with a +N more chip that names the rest", () => {
  const names = Object.fromEntries(["a1", "a2", "a3", "a4", "a5", "a6", "a7", "a8"].map((name) => [name, 1]));
  const [row] = eventRows({ id: 1, status: 200, phase: "request", matches: { new_kinds: { fields: 8 }, new_field_names: names } });
  assert.deepEqual(chips(row), ["a1 1", "a2 1", "a3 1", "a4 1", "a5 1", "+3 more"]);
  assert.equal(row.kinds.at(-1).title, "a6 1, a7 1, a8 1");
});

test("a blocked request becomes a single status row", () => {
  const rows = eventRows({ id: 2, status: 422, phase: "request", reason: "attachments_not_supported", matches: {} });
  assert.deepEqual(rows.map((row) => [row.key, row.label, row.cls]), [["2:status", "Blocked · attachment", "status-block"]]);
});

test("the ring lists only detected types, largest first", () => {
  const events = [{ matches: { new_kinds: { email: 2, vin: 0 } } }, { matches: { new_kinds: { email: 1, secret: 5 } } }];
  assert.deepEqual(kindTotals(events), [{ kind: "secret", count: 5 }, { kind: "email", count: 3 }]);
});

test("a terminal sparkline counts only prompts from its folder since it started", () => {
  const session = { folder: "~/work", started: "2026-10-13T10:00:00Z" };
  const events = [
    { id: 3, time: "2026-10-13T10:05:00Z", folder: "~/work", status: 403, phase: "request", matches: {} },
    { id: 2, time: "2026-10-13T10:01:00Z", folder: "~/work", status: 200, phase: "request", matches: { new_kinds: { email: 2 } } },
    { id: 1, time: "2026-10-13T09:00:00Z", folder: "~/work", status: 200, phase: "request", matches: { new_kinds: { email: 9 } } },
    { id: 4, time: "2026-10-13T10:02:00Z", folder: "~/other", status: 200, phase: "request", matches: { new_kinds: { email: 1 } } },
  ];
  assert.deepEqual(terminalActivity(events, session, 16), [{ id: 2, values: 2, blocked: false }, { id: 3, values: 0, blocked: true }]);
});

test("values added to a running turn become new detections at the time they are seen", () => {
  const seen = new Map();
  const turn = (emails, fields) => ({ id: 9, time: "2026-10-13T10:00:00Z", phase: "request", adapter: "claude-code", status: 200, matches: { new_kinds: { email: emails, fields } } });
  const first = newDetections(seen, [turn(2, 0)], Date.parse("2026-10-13T10:00:30Z"), false);
  assert.deepEqual(first, [{ time: Date.parse("2026-10-13T10:00:00Z"), kinds: { email: 2 }, blocked: 0, adapter: "claude-code" }]);
  const later = Date.parse("2026-10-13T10:00:32Z");
  assert.deepEqual(newDetections(seen, [turn(5, 1)], later, true), [{ time: later, kinds: { email: 3, fields: 1 }, blocked: 0, adapter: "claude-code" }]);
  assert.deepEqual(newDetections(seen, [turn(5, 1)], later + 2000, true), []);
  newDetections(seen, [], later + 4000, true);
  assert.equal(seen.size, 0);
});

test("a repeated block is detected again by its repeat count", () => {
  const seen = new Map();
  const block = (repeats) => ({ id: 5, time: "2026-10-13T10:00:00Z", phase: "request", adapter: "copilot-cli", status: 403, repeats, matches: {} });
  newDetections(seen, [block(0)], Date.parse("2026-10-13T10:00:01Z"), false);
  assert.deepEqual(newDetections(seen, [block(2)], Date.parse("2026-10-13T10:00:05Z"), true).map((sample) => sample.blocked), [2]);
});

test("blocks in the last hour include repeats and skip older ones", () => {
  const now = Date.parse("2026-10-13T12:00:00Z");
  const events = [
    { time: "2026-10-13T11:30:00Z", status: 403, phase: "request", repeats: 2 },
    { time: "2026-10-13T10:30:00Z", status: 403, phase: "request" },
    { time: "2026-10-13T11:45:00Z", status: 200, phase: "request" },
  ];
  assert.equal(blockedSince(events, now - 60 * 60 * 1000), 3);
});

test("a protected shell is titled by its type, an agent started on its own stays a terminal", () => {
  assert.deepEqual(["zsh", "bash", "fish", "claude", "copilot"].map(terminalTitle), ["zsh", "bash", "fish", "Terminal", "Terminal"]);
});

test("stopped telemetry does not raise the blocked alarm or a red bar", () => {
  const telemetry = { id: 8, time: "2026-10-13T11:50:00Z", phase: "request", adapter: "copilot-cli", status: 403, reason: "sensitive_value_not_masked_yet", route: "/telemetry", repeats: 1, matches: {} };
  assert.equal(blockedSince([telemetry], Date.parse("2026-10-13T11:00:00Z")), 0);
  assert.deepEqual(newDetections(new Map(), [telemetry], Date.parse("2026-10-13T11:50:01Z"), false), []);
  assert.deepEqual(eventRows(telemetry).map((row) => [row.label, row.cls]), [["Telemetry stopped · protected value kept on this Mac ×2", "status-quiet"]]);
});
