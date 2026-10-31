import { t } from "./i18n";

/** Count markup and text before DOMParser allocates any attacker-controlled nodes. */
export function checkXmlComplexity(source: string, limit = 100000, maxDepth = 100): number {
  let nodes = 0, depth = 0, index = 0;
  while (index < source.length) {
    if (++nodes > limit) throw new Error(t("文档结构过于复杂，无法安全预览"));
    if (source[index] !== "<") {
      const end = source.indexOf("<", index);
      index = end < 0 ? source.length : end;
      continue;
    }
    if (source.startsWith("<!--", index) || source.startsWith("<![CDATA[", index)) {
      const comment = source.startsWith("<!--", index);
      const closer = comment ? "-->" : "]]>";
      const end = source.indexOf(closer, index + (comment ? 4 : 9));
      if (end < 0) throw new Error(t("文档结构过于复杂，无法安全预览"));
      index = end + closer.length;
      continue;
    }
    const closing = source[index + 1] === "/";
    const declaration = source[index + 1] === "!" || source[index + 1] === "?";
    let quote = "", end = index + 1, attributes = 0;
    for (; end < source.length; end++) {
      const char = source[end];
      if (quote) { if (char === quote) quote = ""; }
      else if (char === '"' || char === "'") quote = char;
      else if (char === "=") { if (++attributes > 256 || ++nodes > limit) throw new Error(t("文档结构过于复杂，无法安全预览")); }
      else if (char === ">") break;
    }
    if (end === source.length) throw new Error(t("文档结构过于复杂，无法安全预览"));
    if (closing) depth--;
    else if (!declaration && source[end - 1] !== "/") depth++;
    if (depth > maxDepth || depth < 0) throw new Error(t("文档结构过于复杂，无法安全预览"));
    index = end + 1;
  }
  return nodes;
}
