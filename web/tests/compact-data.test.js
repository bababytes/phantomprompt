import assert from "node:assert/strict";
import { test } from "node:test";
import { terminalActivity, terminalTitle } from "../src/compact/data.js";

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

test("a protected shell is titled by its type, an agent started on its own stays a terminal", () => {
  assert.deepEqual(["zsh", "bash", "fish", "claude", "copilot"].map(terminalTitle), ["zsh", "bash", "fish", "Terminal", "Terminal"]);
});
