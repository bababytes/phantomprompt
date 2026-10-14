import assert from "node:assert/strict";
import { test } from "node:test";
import { serviceState } from "../src/services.js";

const now = Date.parse("2026-10-14T11:50:00Z");
const claude = { service: "Claude Code", protected: true, last_seen: "2026-10-14T11:40:55Z" };
const terminal = (active, services) => ({ active, services });
const active = { text: "Protected · active", cls: "status-ok" };
const idle = { text: "Protected · idle", cls: "status-quiet" };

test("a service used only by a finished one-shot run is idle and grey, not active or a warning", () => {
  assert.deepEqual(serviceState(claude, [], now), idle);
  assert.deepEqual(serviceState(claude, [terminal(false, ["Claude Code"])], now), idle);
});

test("a service stays active while an open terminal uses it or its traffic is under 15 seconds old", () => {
  assert.deepEqual(serviceState(claude, [terminal(true, ["Claude Code"])], now), active);
  assert.deepEqual(serviceState(claude, [terminal(true, ["Codex"])], now), idle);
  assert.deepEqual(serviceState({ ...claude, last_seen: "2026-10-14T11:49:50Z" }, [], now), active);
  assert.deepEqual(serviceState({ ...claude, last_seen: "2026-10-14T11:49:40Z" }, [], now), idle);
});

test("only unprotected traffic is a warning; a blocked service is red", () => {
  assert.deepEqual(serviceState({ service: "Gemini", protected: false, blocked: 2 }, [], now), { text: "Not supported yet · blocked", cls: "status-block" });
  assert.deepEqual(serviceState({ service: "Gemini", protected: false, blocked: 0 }, [], now), { text: "Not supported yet · not protected", cls: "status-warn" });
});
