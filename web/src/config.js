const root = document.getElementById("app");
const given = document.querySelector('meta[name="phantomprompt-access"]').content;
const STORAGE = "phantomprompt.panel";

function signIn() {
  if (given) {
    const signedIn = { access: given, claudeSetup: root.dataset.claudeSetup ?? "" };
    try {
      sessionStorage.setItem(STORAGE, JSON.stringify(signedIn));
    } catch {}
    history.replaceState(null, "", "/");
    return signedIn;
  }
  try {
    return JSON.parse(sessionStorage.getItem(STORAGE) ?? "{}") ?? {};
  } catch {
    return {};
  }
}

const signedIn = signIn();

export const access = signedIn.access ?? "";
export const terminalLauncher = root.dataset.terminal === "true";
export const logsInFinder = root.dataset.logs === "true";
export const author = root.dataset.author ?? "";
export const claudeSetup = signedIn.claudeSetup ?? "";

export let locked = false;

export function showLocked() {
  locked = true;
  try {
    sessionStorage.removeItem(STORAGE);
  } catch {}
  const page = document.createElement("main");
  page.className = "locked-page";
  const title = document.createElement("h1");
  title.textContent = "PhantomPrompt";
  const open = document.createElement("p");
  const command = document.createElement("code");
  command.textContent = "phantom open";
  open.append("Open the panel from the PhantomPrompt app icon, or run ", command, " in a terminal.");
  const once = document.createElement("p");
  once.textContent = "Panel links work once, so this page cannot be bookmarked or opened in a new tab.";
  page.append(title, open, once);
  document.body.replaceChildren(page);
}
