import assert from "node:assert/strict";
import { test } from "node:test";
import { blockInfo, maskedValues, quietBlock, statusInfo } from "../src/events.js";

const at = "2026-10-13T11:09:47.000Z";
const blocked = (route, reason = "sensitive_value_not_masked_yet") => ({ id: 1, time: at, phase: "request", adapter: "copilot-cli", status: 403, route, reason, matches: {} });

test("the secret status counts unique values, not every resend of the history", () => {
  const event = { status: 200, phase: "request", matches: { secret: 435, new_kinds: { secret: 6 } } };
  assert.equal(statusInfo(event).text, "🔒 Password/secret masked (6)");
  assert.equal(statusInfo({ ...event, matches: { secret: 12, new_kinds: {} } }).text, "🔒 Password/secret masked");
});

test("masked values are split by where they came from", () => {
  const event = { adapter: "claude-code", matches: { new_kinds: { email: 3 }, typed_kinds: { email: 1 }, context_kinds: { email: 1 } } };
  assert.equal(maskedValues(event), "you: email 1 · found by AI: email 1 · Claude Code: email 1");
});

test("a blocked telemetry upload is side traffic named by value type, not by its JSON path", () => {
  const info = blockInfo(blocked("/telemetry"), [{ path: "/telemetry", time: at, note: "line[].data.baseData.properties.payload(json).text email" }]);
  assert.deepEqual(info && { place: info.place, kind: info.kind, where: info.where, affects: info.affects }, { place: "telemetry", kind: "email", where: "data.baseData.properties.payload(json).text", affects: false });
});

test("a blocked query parameter is reported as a web address", () => {
  const info = blockInfo({ ...blocked("/search"), status: 403 }, [{ path: "/search", time: at, note: "protected pesel value in query parameter q" }]);
  assert.deepEqual([info.place, info.kind, info.where, info.affects], ["web address", "pesel", "query parameter q", false]);
});

test("blocks on model routes affect the AI conversation", () => {
  for (const route of ["/v1/messages", "/chat/completions", "/responses"]) {
    assert.equal(blockInfo({ ...blocked(route), status: 422, reason: "attachments_not_supported" }, []).affects, true, route);
  }
  assert.equal(blockInfo({ ...blocked("/v1/messages"), status: 429, reason: "passed_without_restore" }, []).place, "AI service error");
});

test("allowed requests carry no block details", () => {
  assert.equal(blockInfo({ ...blocked("/telemetry"), status: 200 }, []), null);
});

test("stopped telemetry is shown calmly and explains that the value may only look protected", () => {
  const event = { ...blocked("/telemetry"), repeats: 1 };
  assert.equal(quietBlock(event), true);
  assert.deepEqual(statusInfo(event), { text: "Telemetry stopped · protected value kept on this Mac ×2", cls: "status-quiet" });
  const info = blockInfo(event, [{ path: "/telemetry", time: at, note: "line[].data.measurements.verdict_confidence vin" }]);
  assert.match(info.why, /^A value in this telemetry has the same format as a VIN\. It may only look like one/);
  assert.equal(quietBlock({ ...blocked("/v1/messages"), reason: "sensitive_value_not_masked_yet" }), false);
});
