interface BrowserCandidate {
  id: string;
  name: string;
  bundle: string;
  extensionsPage: string;
  /** 上架后填：这个浏览器的扩展商店里「AIClaw 浏览器助手」的页面。 */
  storeUrl: string;
}

export const BROWSER_CANDIDATES: BrowserCandidate[] = [
  { id: "edge", name: "Microsoft Edge", bundle: "Microsoft Edge.app", extensionsPage: "edge://extensions", storeUrl: "" },
  { id: "chrome", name: "Chrome", bundle: "Google Chrome.app", extensionsPage: "chrome://extensions", storeUrl: "" },
  { id: "brave", name: "Brave", bundle: "Brave Browser.app", extensionsPage: "brave://extensions", storeUrl: "" },
  { id: "arc", name: "Arc", bundle: "Arc.app", extensionsPage: "chrome://extensions", storeUrl: "" },
  { id: "chromium", name: "Chromium", bundle: "Chromium.app", extensionsPage: "chrome://extensions", storeUrl: "" },
];

/** Electron may return a display name or a macOS app bundle name. */
export function browserIdForApplication(application: string): string {
  const name = application.trim().toLowerCase().replace(/\.app$/, "");
  return BROWSER_CANDIDATES.find(browser =>
    [browser.name, browser.bundle.replace(/\.app$/, "")].some(alias => alias.toLowerCase() === name),
  )?.id ?? "";
}
