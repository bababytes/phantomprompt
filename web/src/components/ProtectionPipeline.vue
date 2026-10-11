<script setup>
import { computed, onBeforeUnmount, onMounted, ref, watch } from "vue";
import { BRAND_ICONS } from "../brandIcons.js";
import { GHOST_DOME, GHOST_EYE_RADIUS, GHOST_EYES, GHOST_HEM, GHOST_LOBE, GHOST_PATH } from "../ghost.js";
import { monitor } from "../monitor.js";

const SAMPLES = {
  EMAIL: ["anna.k@corp", "ops@example.io"],
  GUID: ["3f2a91c4-77e", "b81e0c2d-4a1"],
  SECRET: ["sk-live-9Qx", "AKIA4QX7"],
  FIELD: ["lastName: N", "\"phone\": \"+48"],
  FILE: ["DB_PASS=hu", "host: 10.4."],
  PESEL: ["850712034"],
  CUSTOM: ["PRJ-ORION"],
};
const COUNTERS = { vin: "VIN", email: "EMAIL", guid: "GUID", secret: "SECRET", fields: "FIELD", file: "FILE", pesel: "PESEL", custom: "CUSTOM" };
const AMBIENT_KINDS = ["VIN", "EMAIL", "GUID", "SECRET", "FIELD", "FILE", "EMAIL"];
const TOKEN_GLYPHS = "abcdefghijkmnpqrstuvwxyz23456789";
const VIN_GLYPHS = "ABCDEFGHJKLMNPRSTUVWXYZ0123456789";
const SCRAMBLE_GLYPHS = "ABCDEFGHJKLMNPQRSTUVWXYZ0123456789";
const PROVIDERS = {
  "Claude Code": { name: "Anthropic", icon: "anthropic" },
  "GitHub Copilot CLI": { name: "Copilot", icon: "githubcopilot" },
  "Codex CLI": { name: "OpenAI", icon: "openai" },
};
const LOGO_PITCH = 38;
const LOGO_MIN = 22;
const LOGO_MAX = 28;
const FRAME_PAD = 10;
const LABEL_ROOM = 32;
const LANDED_SECONDS = 1.8;
const TERMINAL_LINES = [
  { text: "ask anna.k@corp", kind: "EMAIL", raw: "anna.k@corp" },
  { prefix: "vin ", kind: "VIN" },
  { text: "key sk-live-9Qx", kind: "SECRET", raw: "sk-live-9Qx" },
  { text: "lastName: Nowak", kind: "FIELD", raw: "lastName: Nowak" },
  { text: "id b81e0c2d-4a1", kind: "GUID", raw: "b81e0c2d-4a1" },
  { text: "cat .env", kind: "FILE", raw: "DB_PASS=hu" },
];
const SCRAMBLE_SECONDS = 0.9;
const REPLY_EVERY = 3;
const REPLY_DELAY_SECONDS = 0.5;
const GATE_APPROACH_SECONDS = 0.2;
const LABEL_FONT_SIZE = 9;
const SCRAMBLE_LEAD = 24;
const GHOST_RISE = 5;
const TRAIL_LENGTH = 200;
const LABEL_END_FADE = 48;
const DOT_SCALE = 0.15;
const ARRIVAL_ZONE = 60;
const ARRIVAL_SLOWEST = 0.45;
const TRAIL_RETRACT = 4;
const GHOST = new Path2D(GHOST_PATH);
const GHOST_SCALE = 2.9;
const LOUPE = { x: 0, y: -54, radius: 9, handle: 9 };
const SPEED_LINES = [{ offset: 0, length: 90 }, { offset: -3.5, length: 56 }, { offset: 3.5, length: 64 }];
const INK_WIDTH = 1.4;
const RAY_SECONDS = 1.6;
const RAY_DRIFT = 4;
const RAYS = [
  { angle: -155, start: 46, length: 9, phase: 0.05 },
  { angle: -128, start: 43, length: 12, phase: 0.4 },
  { angle: -52, start: 44, length: 11, phase: 0.7 },
  { angle: -26, start: 47, length: 8, phase: 0.2 },
  { angle: 28, start: 46, length: 9, phase: 0.55 },
  { angle: 58, start: 43, length: 12, phase: 0.85 },
  { angle: 122, start: 44, length: 11, phase: 0.1 },
  { angle: 152, start: 47, length: 8, phase: 0.65 },
];
const DIAGRAM_WIDTH = 860;
const ROW_PITCH = 52;

const frame = ref(null);
const canvas = ref(null);
const layout = ref(null);
const flash = ref(false);
const focus = ref(0);
const landed = ref(-1);

const state = computed(() => {
  if (monitor.stopped) return "stopped";
  return monitor.protection ? "on" : "off";
});
const maskedTotal = computed(() => monitor.totals.new_values ?? 0);
const caption = computed(() => ({ on: "AI sees tokens, you see words, secrets stay home", off: "Protection off · values leave unmasked", stopped: "Stopped · PhantomPrompt is not running" })[state.value]);

const sources = computed(() => {
  const sessions = (monitor.totals.cli_sessions ?? []).filter((session) => session.active);
  const seen = (monitor.totals.integrations ?? []).filter((item) => item.last_seen);
  const services = [...new Set([...sessions.flatMap((session) => session.services ?? []), ...seen.map((item) => item.service)])];
  return [{
    key: "terminal",
    name: sessions.length ? sessions[0].label + (sessions.length > 1 ? ` +${sessions.length - 1}` : "") : "terminal",
    status: sessions.length ? "active" : seen.length ? "seen" : "idle",
    stamp: [...sessions.map((session) => session.last_activity ?? ""), ...seen.map((item) => item.last_seen)].join("|"),
    services,
  }];
});
const typedLine = ref("");
const previousLine = ref("");

const providers = computed(() => {
  const services = new Set([
    ...(monitor.totals.integrations ?? []).filter((item) => item.last_seen).map((item) => item.service),
    ...(monitor.totals.cli_sessions ?? []).filter((session) => session.active).flatMap((session) => session.services ?? []),
  ]);
  return Object.entries(PROVIDERS).map(([service, provider]) => ({ service, ...provider, seen: services.has(service) }));
});

let context = null;
let width = 0;
let height = 0;
let frameId = 0;
let previous = 0;
let clock = 0;
let lineIndex = 0;
let ambientProvider = 0;
let typedChars = 0;
let typeIn = 0.8;
let currentLine = null;
let visible = true;
let reduced = false;
let blink = 0;
let blinkIn = 3;
let palette = {};
let geometry = null;
let particles = [];
let pulses = [];
let bursts = [];
let ghostReact = 0;
let arrivals = 0;
let gateCapture = null;
let stamps = new Map();
let flashTimer = 0;
let landedTimer = 0;
let observer = null;
let intersection = null;
let motionQuery = null;

const pick = (list) => list[Math.floor(Math.random() * list.length)];
const glyphs = (set, count) => Array.from({ length: count }, () => pick(set)).join("");
const token = (kind) => `[PP_${kind}_${glyphs(TOKEN_GLYPHS, 6)}]`;

function scrambled(target, raw, progress) {
  const shown = Math.floor(target.length * progress);
  const length = Math.round(raw.length + (target.length - raw.length) * progress);
  return target.slice(0, shown) + glyphs(SCRAMBLE_GLYPHS, Math.max(0, length - shown));
}

function tint(color, alpha) {
  const match = /^#([0-9a-f]{6})$/i.exec(color ?? "");
  if (!match) return color;
  const value = parseInt(match[1], 16);
  return `rgba(${value >> 16}, ${(value >> 8) & 255}, ${value & 255}, ${alpha})`;
}

function readPalette() {
  const style = getComputedStyle(frame.value);
  const read = (name) => style.getPropertyValue(name).trim();
  palette = { surface: read("--bg"), text: read("--text"), muted: read("--muted"), faint: read("--faint"), accent: read("--accent"), warn: read("--warn"), danger: read("--danger"), ink: read("--ink-soft"), mono: read("--mono") };
}

function tone() {
  return { on: palette.accent, off: palette.warn, stopped: palette.faint }[state.value];
}

function curve(x0, y0, x1, y1) {
  const bend = (x1 - x0) * 0.55;
  const points = [];
  let length = 0;
  for (let i = 0; i <= 48; i++) {
    const t = i / 48;
    const u = 1 - t;
    const x = u * u * u * x0 + 3 * u * u * t * (x0 + bend) + 3 * u * t * t * (x1 - bend) + t * t * t * x1;
    const y = (u * u * u + 3 * u * u * t) * y0 + (3 * u * t * t + t * t * t) * y1;
    if (points.length) length += Math.hypot(x - points.at(-1).x, y - points.at(-1).y);
    points.push({ x, y, d: length });
  }
  return { x0, y0, x1, y1, bend, points, length };
}

function rows(count, top, bottom, maxPitch) {
  const middle = (top + bottom) / 2;
  const pitch = Math.min(maxPitch, (bottom - top) / Math.max(count - 1, 1));
  return Array.from({ length: count }, (_, index) => Math.round(middle + (index - (count - 1) / 2) * pitch));
}

function column(count, center, left, right, top, bottom) {
  const ys = rows(count, top, bottom, LOGO_PITCH);
  const pitch = count > 1 ? ys[1] - ys[0] : LOGO_PITCH;
  const logo = Math.min(LOGO_MAX, Math.max(LOGO_MIN, pitch - 10));
  const half = logo / 2 + FRAME_PAD;
  const x = Math.round(Math.max(left + half, Math.min(center, right - half - LABEL_ROOM)));
  const frame = { left: x - half, top: ys[0] - half, width: 2 * half, height: ys.at(-1) - ys[0] + 2 * half };
  return { logo, frame, providers: ys.map((y) => ({ x, y })) };
}

function build() {
  if (!width || !height) return;
  const diagram = Math.min(width, DIAGRAM_WIDTH);
  const offset = Math.round((width - diagram) / 2);
  const inset = offset + Math.round(Math.min(Math.max(diagram * 0.3, 142), 240));
  const top = 56;
  const bottom = height - 65;
  const mid = Math.round((top + bottom) / 2);
  const gate = Math.round(width / 2);
  const sourceRows = rows(sources.value.length, top, bottom, ROW_PITCH);
  const targets = column(providers.value.length, width - 18 - (inset - 30) / 2, width - inset + 12, width - 18, top, bottom);
  const edge = (targets.providers[0]?.x ?? width - inset) - targets.logo / 2 - 3;
  geometry = {
    top, bottom, mid, gate, left: inset, right: width - inset, providerEdge: edge, sourceRows,
    inbound: sourceRows.map((y) => curve(inset, y, gate, mid)),
    outbound: targets.providers.map(({ y }) => curve(gate, mid, edge, y)),
  };
  for (const particle of particles) {
    particle.source %= geometry.inbound.length;
    particle.provider %= geometry.outbound.length;
  }
  layout.value = { width, left: inset, right: width - inset, sources: sourceRows, logo: targets.logo, frame: targets.frame, providers: targets.providers };
}

function sample(path, distance) {
  const { points } = path;
  let index = 1;
  while (index < points.length - 1 && points[index].d < distance) index++;
  const a = points[index - 1];
  const b = points[index];
  const t = b.d > a.d ? Math.min(1, Math.max(0, (distance - a.d) / (b.d - a.d))) : 0;
  return { x: a.x + (b.x - a.x) * t, y: a.y + (b.y - a.y) * t };
}

function locate(particle, distance) {
  if (particle.reply) distance = span(particle) - distance;
  const inbound = geometry.inbound[particle.source];
  return distance < inbound.length ? sample(inbound, distance) : sample(geometry.outbound[particle.provider], distance - inbound.length);
}

function span(particle) {
  return geometry.inbound[particle.source].length + geometry.outbound[particle.provider].length;
}

function route(sourceIndex, live) {
  const services = sources.value[sourceIndex]?.services ?? [];
  const matched = providers.value.map((provider, index) => (services.includes(provider.service) ? index : -1)).filter((index) => index >= 0);
  if (live && matched.length) return pick(matched);
  ambientProvider = (ambientProvider + 1) % providers.value.length;
  return ambientProvider;
}

function hover() {
  if (reduced) return { x: geometry.gate, y: geometry.mid - GHOST_RISE, lean: 0 };
  return {
    x: geometry.gate + Math.sin(clock * 1.3) * 2.5 + Math.sin(clock * 2.9 + 1) * 1.2,
    y: geometry.mid - GHOST_RISE + Math.sin(clock * 1.7 + 0.5) * 1.8 + Math.sin(clock * 3.7) * 0.8,
    lean: Math.sin(clock * 1.1) * 0.05 + Math.sin(clock * 2.3 + 2) * 0.02,
  };
}

function sampleValue(kind) {
  return kind === "VIN" ? glyphs(VIN_GLYPHS, 11) : pick(SAMPLES[kind] ?? SAMPLES.EMAIL);
}

function particle(sourceIndex, live, kind = pick(AMBIENT_KINDS), raw = sampleValue(kind)) {
  return { source: sourceIndex, provider: route(sourceIndex, live), d: 0, speed: (geometry.right - geometry.left) / (live ? 4.2 : 8), raw, token: token(kind), label: raw, mode: "raw", live, scramble: 0, tick: 0, flash: 0, gateSync: false };
}

function emitters() {
  const busy = sources.value.map((source, index) => (source.status === "idle" ? -1 : index)).filter((index) => index >= 0);
  return busy.length ? busy : [sources.value.length - 1];
}

function spawn(live, delay = 0, origin, kind, raw) {
  if (!geometry) return;
  const created = particle(origin ?? pick(emitters()), live, kind, raw);
  created.d = -delay * created.speed;
  particles.push(created);
}

function seed() {
  particles = [];
  pulses = [];
  bursts = [];
  gateCapture = null;
  if (!geometry || state.value === "stopped") return;
  const origins = emitters();
  const crossed = state.value === "on" ? "masked" : "leaked";
  const lastProvider = geometry.outbound.length - 1;
  const placements = [[origins[0], 0.62, null], [origins.at(-1), origins.length > 1 ? 0.38 : 0.2, null], [origins[0], 0.34, 0], [origins.at(-1), 0.7, lastProvider]];
  for (const [sourceIndex, position, provider] of placements) {
    const created = particle(sourceIndex, false);
    const inbound = geometry.inbound[sourceIndex].length;
    if (provider === null) {
      created.d = inbound * position;
    } else {
      created.provider = provider;
      created.d = inbound + geometry.outbound[provider].length * position;
      created.mode = crossed;
      created.label = crossed === "masked" ? created.token : created.raw;
    }
    particles.push(created);
  }
}

function typeCommand(dt) {
  if (particles.some((item) => item.reply && item.d < span(item))) return;
  typeIn -= dt;
  if (typeIn > 0) return;
  if (!currentLine) {
    const line = TERMINAL_LINES[lineIndex];
    const raw = line.raw ?? sampleValue(line.kind);
    currentLine = { kind: line.kind, raw, text: line.text ?? line.prefix + raw };
  }
  if (typedChars < currentLine.text.length) {
    typedChars++;
    typedLine.value = currentLine.text.slice(0, typedChars);
    typeIn = 0.05 + Math.random() * 0.06;
    return;
  }
  spawn(false, 0, 0, currentLine.kind, currentLine.raw);
  previousLine.value = currentLine.text;
  typedLine.value = "";
  typedChars = 0;
  currentLine = null;
  lineIndex = (lineIndex + 1) % TERMINAL_LINES.length;
  typeIn = 2.2 + Math.random() * 1.6;
}

function showStaticCommand() {
  if (reduced) typedLine.value = TERMINAL_LINES[0].text;
}

function meetGate(item) {
  if (gateCapture) gateCapture.pending.add(item);
  else gateCapture = { pending: new Set([item]), progress: 0 };
}

function leaveGate(item) {
  if (gateCapture?.pending.delete(item) && gateCapture.pending.size === 0) gateCapture = null;
}

function land(provider) {
  landed.value = provider;
  clearTimeout(landedTimer);
  landedTimer = setTimeout(() => { landed.value = -1; }, LANDED_SECONDS * 1000);
}

function touch(item) {
  bursts.push({ x: geometry.gate, y: geometry.mid, age: 0, sparks: Array.from({ length: item.live ? 8 : 6 }, () => ({ angle: Math.random() * Math.PI * 2, reach: 10 + Math.random() * 14, size: 0.7 + Math.random() * 0.7 })) });
}

function update(dt) {
  clock += dt;
  blink = Math.max(0, blink - dt);
  blinkIn -= dt;
  if (blinkIn <= 0) {
    blink = 0.14;
    blinkIn = 3 + Math.random() * 4;
  }
  if (gateCapture) gateCapture.progress = Math.min(1, gateCapture.progress + dt / GATE_APPROACH_SECONDS);
  typeCommand(dt);
  const replies = [];
  for (const item of particles) {
    item.d += item.speed * dt * arrivalPace(item);
    if (!item.reply && !item.focused && item.mode !== "raw" && span(item) - item.d <= ARRIVAL_ZONE) {
      item.focused = true;
      focus.value = item.provider;
    }
    const inbound = geometry.inbound[item.source].length;
    const toGate = item.reply ? geometry.outbound[item.provider].length : inbound;
    const approaching = item.reply ? item.mode === "masked" : item.mode === "raw";
    if (approaching && !item.gateSync && (toGate - item.d) / item.speed <= GATE_APPROACH_SECONDS) {
      item.gateSync = true;
      meetGate(item);
    }
    if (item.mode === "raw" && item.d >= inbound - SCRAMBLE_LEAD) {
      item.mode = state.value === "on" ? "masked" : "leaked";
      if (item.mode === "masked") {
        item.scramble = SCRAMBLE_SECONDS;
        item.flash = 1;
        ghostReact = 1;
        touch(item);
      }
      leaveGate(item);
      pulses.push({ x: geometry.gate, y: geometry.mid, age: 0, color: item.mode === "masked" ? palette.accent : palette.warn, strength: item.live ? 1 : 0.4 });
    }
    if (item.reply && item.mode === "masked" && item.d >= geometry.outbound[item.provider].length - SCRAMBLE_LEAD) {
      item.mode = "restored";
      item.scramble = SCRAMBLE_SECONDS;
      item.flash = 1;
      ghostReact = 1;
      touch(item);
      leaveGate(item);
      pulses.push({ x: geometry.gate, y: geometry.mid, age: 0, color: palette.accent, strength: item.live ? 0.8 : 0.3 });
    }
    if (item.scramble > 0) {
      item.scramble = Math.max(0, item.scramble - dt);
      item.tick -= dt;
      if (item.tick <= 0 || item.scramble === 0) {
        item.tick = 0.06;
        const [target, from] = item.reply ? [item.raw, item.token] : [item.token, item.raw];
        item.label = item.scramble > 0 ? scrambled(target, from, 1 - item.scramble / SCRAMBLE_SECONDS) : target;
      }
    }
    if (!item.reply && !item.landed && item.d >= span(item)) {
      item.landed = true;
      land(item.provider);
    }
    if (item.mode === "masked" && !item.reply && !item.answered && item.d >= span(item)) {
      item.answered = true;
      if (item.live || ++arrivals % REPLY_EVERY === 0) {
        focus.value = item.provider;
        replies.push({ ...item, reply: true, gateSync: false, d: -REPLY_DELAY_SECONDS * item.speed, label: item.token, scramble: 0, flash: 0 });
      }
    }
  }
  particles.push(...replies);
  particles = particles.filter((item) => item.d < span(item) || trailRange(item).visible);
  for (const item of particles) item.flash = Math.max(0, item.flash - dt / 0.5);
  for (const pulse of pulses) pulse.age += dt / 0.9;
  pulses = pulses.filter((pulse) => pulse.age < 1);
  for (const burst of bursts) burst.age += dt / 0.7;
  bursts = bursts.filter((burst) => burst.age < 1);
  ghostReact = Math.max(0, ghostReact - dt / 0.45);
}

function flutteringGhost(time, gust) {
  const sway = (Math.sin(time * 1.9) * 0.45 - 0.32) * gust;
  const hem = [0, 1, 2, 3].map((i) => [GHOST_HEM.right - GHOST_LOBE * i + sway + Math.sin(time * 7.2 + i * 0.9) * 0.24 * gust, GHOST_HEM.top + Math.sin(time * 6.4 + i * 1.25) * 0.3 * gust]);
  const middle = (GHOST_DOME.y + GHOST_HEM.top) / 2;
  const path = new Path2D();
  path.moveTo(GHOST_DOME.x - GHOST_DOME.radius, GHOST_DOME.y);
  path.arc(GHOST_DOME.x, GHOST_DOME.y, GHOST_DOME.radius, Math.PI, 2 * Math.PI);
  path.quadraticCurveTo(GHOST_HEM.right + sway * 0.3, middle, ...hem[0]);
  hem.slice(1).forEach(([x, y], i) => {
    const [fromX, fromY] = hem[i];
    const half = Math.hypot(x - fromX, y - fromY) / 2;
    const sag = GHOST_HEM.depth + Math.sin(time * 5.6 + i * 1.7) * 0.3 * gust;
    const radius = (half * half + sag * sag) / (2 * sag);
    const lift = (radius - sag) / (2 * half);
    const centerX = (x + fromX) / 2 - (y - fromY) * lift;
    const centerY = (y + fromY) / 2 + (x - fromX) * lift;
    path.arc(centerX, centerY, radius, Math.atan2(fromY - centerY, fromX - centerX), Math.atan2(y - centerY, x - centerX));
  });
  path.quadraticCurveTo(GHOST_HEM.left + sway * 0.3, middle, GHOST_DOME.x - GHOST_DOME.radius, GHOST_DOME.y);
  path.closePath();
  return path;
}

function drawGhost(x, y, alpha, lean) {
  const squash = ghostReact > 0 ? Math.sin((1 - ghostReact) * Math.PI) * 0.06 : 0;
  context.save();
  context.translate(x, y);
  context.rotate(lean);
  context.scale(GHOST_SCALE * (1 + squash), GHOST_SCALE * (1 - squash * 0.8));
  context.translate(-16, -15.4);
  const body = reduced ? GHOST : flutteringGhost(clock, 1 + ghostReact * 1.5);
  context.lineJoin = "round";
  context.lineCap = "round";
  context.fillStyle = palette.surface;
  context.fill(body);
  context.lineWidth = INK_WIDTH / GHOST_SCALE;
  context.strokeStyle = palette.ink;
  context.globalAlpha = alpha;
  context.stroke(body);
  context.globalAlpha = 1;
  const open = blink > 0 ? 0.3 : 1;
  context.beginPath();
  for (const eye of GHOST_EYES) context.roundRect(eye.x, eye.y + (eye.height * (1 - open)) / 2, eye.width, eye.height * open, GHOST_EYE_RADIUS);
  context.fillStyle = tone();
  context.fill();
  context.restore();
}

function drawRays(x, y, alpha) {
  context.lineCap = "round";
  context.lineWidth = 1;
  context.strokeStyle = palette.ink;
  for (const ray of RAYS) {
    const t = reduced ? 0.5 : (clock / RAY_SECONDS + ray.phase) % 1;
    const from = ray.start - t * RAY_DRIFT;
    const dx = Math.cos((ray.angle * Math.PI) / 180);
    const dy = Math.sin((ray.angle * Math.PI) / 180);
    context.globalAlpha = alpha * Math.sin(t * Math.PI);
    context.beginPath();
    context.moveTo(x + dx * (from + ray.length), y + dy * (from + ray.length));
    context.lineTo(x + dx * from, y + dy * from);
    context.stroke();
  }
  context.globalAlpha = 1;
}

function drawLoupe() {
  const rim = Math.SQRT1_2 * LOUPE.radius;
  const reach = Math.SQRT1_2 * (LOUPE.radius + LOUPE.handle);
  context.save();
  context.translate(geometry.gate, geometry.mid);
  context.lineCap = "round";
  context.strokeStyle = palette.ink;
  context.lineWidth = 2.4;
  context.beginPath();
  context.moveTo(LOUPE.x + rim, LOUPE.y + rim);
  context.lineTo(LOUPE.x + reach, LOUPE.y + reach);
  context.stroke();
  context.beginPath();
  context.arc(LOUPE.x, LOUPE.y, LOUPE.radius, 0, Math.PI * 2);
  context.fillStyle = tint(palette.text, 0.06);
  context.fill();
  context.lineWidth = INK_WIDTH;
  context.stroke();
  context.beginPath();
  context.arc(LOUPE.x, LOUPE.y, LOUPE.radius - 2.2, Math.PI * 1.1, Math.PI * 1.4);
  context.lineWidth = 1;
  context.stroke();
  context.restore();
}

function drawScanner() {
  const on = state.value === "on";
  const intensity = gateCapture ? Math.sin(gateCapture.progress * Math.PI) : 0;
  const { x, y, lean } = hover();
  drawRays(x, y, on ? 0.9 : 0.6);
  drawGhost(x, y, (on ? 0.85 : 0.65) + intensity * 0.15, lean);
}

function drawBursts() {
  context.lineCap = "round";
  context.lineWidth = 1.2;
  for (const burst of bursts) {
    const ease = 1 - (1 - burst.age) ** 3;
    context.strokeStyle = tint(palette.accent, (1 - burst.age) * 0.8);
    context.beginPath();
    for (const spark of burst.sparks) {
      const from = 22 + spark.reach * ease * 0.7;
      const to = from + 2 + 6 * spark.size * (1 - burst.age);
      context.moveTo(burst.x + Math.cos(spark.angle) * from, burst.y + Math.sin(spark.angle) * from);
      context.lineTo(burst.x + Math.cos(spark.angle) * to, burst.y + Math.sin(spark.angle) * to);
    }
    context.stroke();
  }
}


function drawPulses() {
  for (const pulse of pulses) {
    const ease = 1 - (1 - pulse.age) ** 3;
    context.beginPath();
    context.arc(pulse.x + 0.5, pulse.y + 0.5, 4 + ease * 14 * (0.6 + pulse.strength * 0.4), 0, Math.PI * 2);
    context.strokeStyle = tint(pulse.color, (1 - pulse.age) * 0.55 * pulse.strength);
    context.lineWidth = 1;
    context.stroke();
  }
}

function trailRange(item) {
  const total = span(item);
  const retract = Math.max(0, item.d - (total - LABEL_END_FADE)) * TRAIL_RETRACT;
  const from = Math.max(0, item.d - TRAIL_LENGTH + retract);
  const to = Math.min(item.d, total);
  return { from, to, visible: to - from >= 2 };
}

function drawTrail(item, color, base) {
  const { from, to, visible } = trailRange(item);
  if (!visible) return;
  context.lineCap = "round";
  context.lineWidth = 1;
  for (const line of SPEED_LINES) {
    const start = Math.max(from, to - line.length);
    if (to - start < 2) continue;
    context.beginPath();
    for (let distance = start; distance <= to; distance += 4) {
      const point = locate(item, distance);
      const ahead = locate(item, distance + 2);
      const run = Math.hypot(ahead.x - point.x, ahead.y - point.y) || 1;
      const x = point.x - ((ahead.y - point.y) / run) * line.offset;
      const y = point.y + ((ahead.x - point.x) / run) * line.offset;
      if (distance === start) context.moveTo(x, y);
      else context.lineTo(x, y);
    }
    const tail = locate(item, start);
    const end = locate(item, to);
    const fade = context.createLinearGradient(tail.x, tail.y, end.x, end.y);
    fade.addColorStop(0, tint(color, 0));
    fade.addColorStop(1, tint(color, 0.6 * base));
    context.strokeStyle = fade;
    context.stroke();
  }
}


function arrivalPace(item) {
  const left = span(item) - item.d;
  if (left >= ARRIVAL_ZONE) return 1;
  return ARRIVAL_SLOWEST + (1 - ARRIVAL_SLOWEST) * Math.max(0, left) / ARRIVAL_ZONE;
}

function shrinkScale(item, total) {
  const gate = item.reply ? geometry.outbound[item.provider].length : geometry.inbound[item.source].length;
  return item.d > gate ? Math.max(0, 1 - Math.min(1, (item.d - gate) / (total - gate)) ** 4) : 1;
}

function drawParticles() {
  const labels = [];
  for (const item of particles) {
    if (item.d <= 0) continue;
    const total = span(item);
    const color = item.mode === "masked" ? palette.accent : item.mode === "leaked" ? palette.danger : item.live || item.mode === "restored" ? palette.text : palette.muted;
    const base = (item.live ? 1 : 0.7) * Math.min(1, item.d / 36);
    drawTrail(item, color, base);
    const head = locate(item, Math.min(item.d, total));
    const scale = shrinkScale(item, total);
    if (item.d >= total) continue;
    labels.push({ item, head, color, scale, strength: base * Math.min(1, scale / DOT_SCALE) });
  }
  labels.sort((a, b) => Number(b.item.live) - Number(a.item.live) || b.item.d - a.item.d);
  context.font = `500 ${LABEL_FONT_SIZE}px ${palette.mono}`;
  context.textBaseline = "middle";
  context.textAlign = "center";
  const placed = [];
  for (const { item, head, color, scale, strength } of labels) {
    if (strength <= 0.02) continue;
    const boxWidth = Math.ceil(context.measureText(item.label).width) + 10;
    const x = Math.round(head.x);
    const y = Math.round(head.y);
    const half = (boxWidth / 2) * scale;
    const box = { left: x - half, right: x + half, top: y - 7 * scale, bottom: y + 7 * scale };
    const outside = box.left < geometry.left - 12 || box.right > geometry.providerEdge + 8;
    if (outside || placed.some((other) => box.left < other.right + 4 && box.right > other.left - 4 && box.top < other.bottom && box.bottom > other.top)) continue;
    placed.push(box);
    context.save();
    context.translate(x, y);
    context.scale(scale, scale);
    context.beginPath();
    context.roundRect(-boxWidth / 2 + 0.5, -6.5, boxWidth - 1, 13, 6.5);
    context.fillStyle = tint(palette.surface, Math.min(1, strength * 1.6));
    context.fill();
    const ink = item.flash > 0 ? palette.accent : color;
    context.lineWidth = 1.2;
    context.strokeStyle = tint(ink, Math.min(1, (item.mode === "raw" ? 0.55 : 0.8) * strength + 0.4 * item.flash));
    context.stroke();
    context.beginPath();
    context.roundRect(-boxWidth / 2 + 1.2, -6, boxWidth - 1.8, 12.6, 6.5);
    context.lineWidth = 0.8;
    context.strokeStyle = tint(ink, 0.3 * strength);
    context.stroke();
    context.fillStyle = tint(color, Math.min(1, strength * 1.15));
    context.fillText(item.label, 0, 0.5);
    context.restore();
  }
}

function draw() {
  if (!context || !geometry) return;
  context.clearRect(0, 0, width, height);
  drawPulses();
  drawScanner();
  drawParticles();
  drawLoupe();
  drawBursts();
}

function running() {
  return visible && !reduced && !document.hidden && state.value !== "stopped";
}

function loop(now) {
  frameId = 0;
  const dt = previous ? Math.min((now - previous) / 1000, 0.05) : 0;
  previous = now;
  update(dt);
  draw();
  schedule();
}

function schedule() {
  if (running() && !frameId) {
    frameId = requestAnimationFrame(loop);
  } else if (!running()) {
    cancelAnimationFrame(frameId);
    frameId = 0;
    previous = 0;
  }
}

function resize() {
  const ratio = window.devicePixelRatio || 1;
  width = canvas.value.clientWidth;
  height = canvas.value.clientHeight;
  canvas.value.width = Math.round(width * ratio);
  canvas.value.height = Math.round(height * ratio);
  context = canvas.value.getContext("2d");
  context.setTransform(ratio, 0, 0, ratio, 0, 0);
  build();
  if (reduced || !particles.length) seed();
  draw();
}

function redraw() {
  build();
  if (reduced || state.value === "stopped") seed();
  draw();
  schedule();
}

function onMotion() {
  reduced = motionQuery.matches;
  showStaticCommand();
  build();
  seed();
  draw();
  schedule();
}

function emitter() {
  const changed = sources.value.findIndex((source) => source.stamp && stamps.has(source.key) && stamps.get(source.key) !== source.stamp);
  return changed >= 0 ? changed : sources.value.length - 1;
}

watch(state, redraw);
watch(() => sources.value.map((source) => `${source.key}:${source.status}`).join("|") + providers.value.map((provider) => `${provider.service}:${provider.seen}`).join("|"), redraw);

watch(() => monitor.totals, (totals, before) => {
  const added = (totals?.new_values ?? 0) - (before?.new_values ?? 0);
  if (before?.new_values !== undefined && added > 0) {
    flash.value = true;
    clearTimeout(flashTimer);
    flashTimer = setTimeout(() => { flash.value = false; }, 1800);
    if (!reduced && state.value !== "stopped") {
      const origin = emitter();
      const kinds = Object.entries(COUNTERS).flatMap(([counter, kind]) => Array(Math.max(0, Math.min(5, (totals[counter] ?? 0) - (before[counter] ?? 0)))).fill(kind));
      for (let i = 0; i < Math.min(added, 5); i++) spawn(true, i * 0.6, origin, kinds[i]);
      schedule();
    }
  }
  stamps = new Map(sources.value.map((source) => [source.key, source.stamp]));
});

onMounted(() => {
  readPalette();
  motionQuery = window.matchMedia("(prefers-reduced-motion: reduce)");
  reduced = motionQuery.matches;
  showStaticCommand();
  motionQuery.addEventListener("change", onMotion);
  observer = new ResizeObserver(resize);
  observer.observe(canvas.value);
  intersection = new IntersectionObserver(([entry]) => {
    visible = entry.isIntersecting;
    schedule();
  });
  intersection.observe(frame.value);
  document.addEventListener("visibilitychange", schedule);
  resize();
  schedule();
});

onBeforeUnmount(() => {
  cancelAnimationFrame(frameId);
  clearTimeout(flashTimer);
  clearTimeout(landedTimer);
  observer?.disconnect();
  intersection?.disconnect();
  motionQuery?.removeEventListener("change", onMotion);
  document.removeEventListener("visibilitychange", schedule);
});
</script>

<template>
  <div class="pipeline-frame">
  <figure ref="frame" class="pipeline" :class="`pipeline-${state}`" aria-label="Protection pipeline">
    <div class="pipeline-head" aria-hidden="true">
      <span class="pipeline-column" :style="layout && { width: `${layout.left - 30}px` }"><span>THIS MAC</span></span>
      <span class="pipeline-gate-label"><img class="pipeline-gate-logo" src="/assets/icon.svg" alt="" aria-hidden="true"><span class="pipeline-gate-name"><span>PHANTOM</span><span>PROMPT</span></span></span>
      <span class="pipeline-column" :style="layout && { width: `${layout.width - layout.right - 30}px` }"><span>AI PROVIDERS</span></span>
    </div>
    <svg class="pipeline-defs" aria-hidden="true"><filter id="pipeline-ink"><feTurbulence type="fractalNoise" baseFrequency="0.035" numOctaves="2" seed="7" /><feDisplacementMap in="SourceGraphic" scale="2.6" xChannelSelector="R" yChannelSelector="G" /></filter></svg>
    <canvas ref="canvas" aria-hidden="true"></canvas>
    <div v-if="layout" class="pipeline-nodes" aria-hidden="true">
      <div class="pipeline-terminal" :class="`pipeline-terminal-${sources[0].status}`" :style="{ top: `${layout.sources[0]}px`, width: `${layout.left - 30}px` }">
        <div class="pipeline-terminal-bar"><i></i><i></i><i></i><span>{{ sources[0].name }}</span></div>
        <div class="pipeline-terminal-body">
          <div class="pipeline-terminal-previous">{{ previousLine }}</div>
          <div><span class="pipeline-terminal-prompt">❯</span> {{ typedLine }}<span class="pipeline-terminal-cursor"></span></div>
        </div>
      </div>
      <div class="pipeline-providers" :style="{ left: `${layout.frame.left}px`, top: `${layout.frame.top}px`, width: `${layout.frame.width}px`, height: `${layout.frame.height}px` }"></div>
      <div v-for="(provider, index) in providers" :key="provider.service" class="pipeline-node pipeline-provider" :class="[provider.seen ? 'pipeline-node-seen' : 'pipeline-node-idle', index === focus ? 'pipeline-provider-near' : 'pipeline-provider-far', { 'pipeline-provider-landed': index === landed }]" :title="provider.name" :style="{ top: `${layout.providers[index].y}px`, left: `${layout.providers[index].x}px`, '--logo': `${layout.logo}px` }">
        <span class="pipeline-chip">
          <svg class="pipeline-icon" viewBox="0 0 24 24"><path :d="BRAND_ICONS[provider.icon]" /></svg>
        </span>
        <span class="pipeline-provider-name">{{ provider.name }}</span>
      </div>
    </div>
    <figcaption class="pipeline-caption">
      <span>{{ caption }}</span>
      <span class="pipeline-count" :class="{ 'pipeline-count-live': flash }"><b>{{ maskedTotal }}</b> unique values protected since start</span>
    </figcaption>
  </figure>
  </div>
</template>
