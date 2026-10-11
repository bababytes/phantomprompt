const LITERAL = /^(?:\\.|[^\\.^$|?*+()[\]{}])+$/;

function parts(pattern) {
  const insensitive = pattern.startsWith("(?i:") && pattern.endsWith(")");
  return { insensitive, body: insensitive ? pattern.slice(4, -1) : pattern };
}

export function literalText(pattern) {
  const { body } = parts(pattern);
  return LITERAL.test(body) ? body.replace(/\\(.)/g, "$1") : null;
}

export function describe(pattern) {
  const text = literalText(pattern);
  if (text === null) return "Any text that matches this pattern";
  return `The exact text “${text}”${parts(pattern).insensitive ? ", any letter case" : ", same letter case"}`;
}
