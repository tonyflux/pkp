import { renderAsync } from "docx-preview";
import DOMPurify from "dompurify";
import { t } from "./i18n";
import { OFFICE_LIMITS, type OfficePackage } from "./reader-office-archive";
import { rasterMime } from "./consumption";

const forbiddenCss = /(?:@import|@font-face|expression\s*\(|(?:javascript|vbscript|https?|file|ftp):|behavior\s*:|-moz-binding|[<>\\])/i;
export function safeOfficeCss(css: string): string {
  let unsafe = false;
  const checked = css.replace(/url\(\s*(["']?)(.*?)\1\s*\)/gi, (_match, _quote: string, value: string) => {
    if (!/^#[\w-]+$/.test(value) && !/^data:image\/(?:png|jpeg|gif|webp|bmp);base64,[a-z\d+/=\s]+$/i.test(value)) unsafe = true;
    return "";
  });
  return unsafe || /url\s*\(/i.test(checked) || forbiddenCss.test(checked) ? "" : css;
}
export function sanitizeOfficeHtml(root: HTMLElement): string {
  if (root.querySelectorAll("*").length > OFFICE_LIMITS.nodes) throw new Error(t("office|文档超过安全预览上限"));
  for (const element of Array.from(root.querySelectorAll("img,image"))) {
    for (const name of ["src", "href", "xlink:href"]) {
      const source = element.getAttribute(name);
      if (!source?.startsWith("data:")) continue;
      const match = /^data:[^,]*;base64,([a-z\d+/=\s]+)$/i.exec(source);
      if (!match) { element.removeAttribute(name); continue; }
      try {
        const header = Uint8Array.from(atob(match[1].slice(0, 32).replace(/\s/g, "")), char => char.charCodeAt(0));
        const mime = rasterMime(header);
        if (mime) element.setAttribute(name, `data:${mime};base64,${match[1]}`);
        else element.removeAttribute(name);
      } catch { element.removeAttribute(name); }
    }
  }
  const styles = Array.from(root.querySelectorAll("style")).map(style => safeOfficeCss(style.textContent ?? "")).filter(Boolean);
  const fragment = DOMPurify.sanitize(root.innerHTML, {
    RETURN_DOM_FRAGMENT: true,
    ADD_TAGS: ["section"],
    FORBID_TAGS: ["style", "script", "iframe", "object", "embed", "link", "meta", "base", "form", "input", "button", "audio", "video", "foreignObject", "animate", "animateMotion", "animateTransform", "set", "use"],
    FORBID_ATTR: ["srcset", "action", "formaction", "target", "download"],
    ALLOW_DATA_ATTR: false,
  });
  for (const element of Array.from(fragment.querySelectorAll("*"))) {
    for (const attr of Array.from(element.attributes)) {
      if (/^on/i.test(attr.name) || /^(?:src|href|xlink:href|poster)$/i.test(attr.name)) {
        if (attr.name !== "src" && attr.name !== "href" && attr.name !== "xlink:href") { element.removeAttribute(attr.name); continue; }
        if (!/^data:image\/(?:png|jpeg|gif|webp|bmp);base64,[a-z\d+/=\s]+$/i.test(attr.value) && !/^#[\w-]+$/.test(attr.value)) element.removeAttribute(attr.name);
      }
      if (attr.name === "style" && !safeOfficeCss(attr.value)) element.removeAttribute("style");
    }
  }
  const holder = document.createElement("div"); holder.append(fragment);
  return styles.map(css => `<style>${css}</style>`).join("") + holder.innerHTML;
}
export function officeFrameDocument(body: string): string {
  return `<!doctype html><html><head><meta charset="utf-8"><meta http-equiv="Content-Security-Policy" content="default-src 'none'; img-src data:; style-src 'unsafe-inline'; font-src 'none'; script-src 'none'; connect-src 'none'; media-src 'none'; frame-src 'none'; base-uri 'none'; form-action 'none'"><style>html,body{margin:0;padding:0;background:white;color:#202124}body{overflow:hidden}*{box-sizing:border-box}a{pointer-events:none}.docx-wrapper{padding:0!important;background:white!important}.docx-wrapper>section.docx{margin:0!important;box-shadow:none!important}mark{background:#ffda76;color:inherit}</style></head><body>${body}</body></html>`;
}
export type OfficePage = { html: string; text: string; width: number; height: number };
export type OfficeRenderer = { count: number; text(index: number): string; page(index: number): OfficePage; close(): void };
export async function createOfficeRenderer(pkg: OfficePackage, signal: AbortSignal): Promise<OfficeRenderer> {
  const inert = document.implementation.createHTMLDocument("");
  const root = inert.createElement("div");
  inert.body.append(root);
  await renderAsync(pkg.bytes, root, root, { className: "docx", inWrapper: true, breakPages: true, ignoreFonts: true, useBase64URL: true, renderAltChunks: false, renderComments: false, renderChanges: false, experimental: false });
  if (signal.aborted) throw new DOMException("Aborted", "AbortError");
  const sections = Array.from(root.querySelectorAll<HTMLElement>("section.docx"));
  if (!sections.length || sections.length > OFFICE_LIMITS.pages || root.querySelectorAll("*").length > OFFICE_LIMITS.nodes) throw new Error(t("office|文档超过安全预览上限"));
  const styles = Array.from(root.querySelectorAll("style"));
  return {
    count: sections.length,
    text(index) { return sections[index]?.textContent ?? ""; },
    page(index) {
      const section = sections[index];
      if (!section) throw new Error(t("office|文档结构不完整"));
      const page = inert.createElement("div");
      styles.forEach(style => page.append(style.cloneNode(true)));
      const wrapper = inert.createElement("div"); wrapper.className = "docx-wrapper";
      wrapper.append(section.cloneNode(true)); page.append(wrapper);
      const width = cssPixels(section.style.width, 816);
      const height = cssPixels(section.style.minHeight || section.style.height, 1056);
      return { html: sanitizeOfficeHtml(page), text: section.textContent ?? "", width, height };
    },
    close() { root.replaceChildren(); },
  };
}
function cssPixels(value: string, fallback: number): number {
  const match = /^([\d.]+)(px|pt|in|cm|mm)$/.exec(value);
  if (!match) return fallback;
  const factor: Record<string, number> = { px: 1, pt: 96 / 72, in: 96, cm: 96 / 2.54, mm: 96 / 25.4 };
  return Math.min(10000, Math.max(100, Number(match[1]) * factor[match[2]]));
}
