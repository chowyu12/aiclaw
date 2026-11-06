import createDOMPurify from "dompurify";

/** An inert document for an opaque-origin sandbox, never the application's DOM. */
export function htmlPreviewDocument(source: string): string {
  const purify = createDOMPurify(window);
  purify.addHook("afterSanitizeAttributes", node => {
    for (const name of ["href", "xlink:href"]) {
      const value = node.getAttribute(name);
      if (value !== null && !value.startsWith("#")) node.removeAttribute(name);
    }
    for (const name of ["src", "poster", "background"]) {
      const value = node.getAttribute(name);
      if (value !== null && !value.startsWith("data:")) node.removeAttribute(name);
    }
    node.removeAttribute("srcset");
  });
  const clean = purify.sanitize(source, {
    WHOLE_DOCUMENT: true,
    FORBID_TAGS: ["script", "meta", "base", "link", "iframe", "object", "embed", "form"],
    FORBID_ATTR: ["target"],
  });
  const document = new window.DOMParser().parseFromString(clean, "text/html");
  const policy = document.createElement("meta");
  policy.httpEquiv = "Content-Security-Policy";
  policy.content = "default-src 'none'; script-src 'none'; style-src 'unsafe-inline'; img-src data:; font-src data:; base-uri 'none'; form-action 'none'";
  const viewport = document.createElement("meta");
  viewport.name = "viewport";
  viewport.content = "width=device-width, initial-scale=1";
  document.head.prepend(policy, viewport);
  return "<!doctype html>" + document.documentElement.outerHTML;
}
