export const GHOST_DOME = { x: 16, y: 13.6, radius: 7.55 };
export const GHOST_HEM = { left: 8.12, right: 23.88, top: 23.36, depth: 1.4 };
export const GHOST_LOBE = (GHOST_HEM.right - GHOST_HEM.left) / 3;
const LOBE_RADIUS = ((GHOST_LOBE / 2) ** 2 + GHOST_HEM.depth ** 2) / (2 * GHOST_HEM.depth);

export const GHOST_PATH = [
  `M${GHOST_HEM.left} ${GHOST_HEM.top}`,
  `L${GHOST_DOME.x - GHOST_DOME.radius} ${GHOST_DOME.y}`,
  `A${GHOST_DOME.radius} ${GHOST_DOME.radius} 0 0 1 ${GHOST_DOME.x + GHOST_DOME.radius} ${GHOST_DOME.y}`,
  `L${GHOST_HEM.right} ${GHOST_HEM.top}`,
  ...[1, 2, 3].map((lobe) => `A${LOBE_RADIUS.toFixed(3)} ${LOBE_RADIUS.toFixed(3)} 0 0 1 ${(GHOST_HEM.right - GHOST_LOBE * lobe).toFixed(3)} ${GHOST_HEM.top}`),
  "Z",
].join("");

export const GHOST_EYE_RADIUS = 0.31;
export const GHOST_EYES = [
  { x: 10.38, y: 12.77, width: 4.67, height: 2.1 },
  { x: 16.95, y: 12.77, width: 4.67, height: 2.1 },
];
