import { describe, expect, it } from "vitest";
import { linkTarget, parseInline, parseMarkdown, plainText, type Block, type Inline } from "./markdown";

const inline = (src: string) => parseInline(src, { refs: new Map() });

describe("parseMarkdown blocks", () => {
  it("reads headings, paragraphs, quotes and rules", () => {
    const { blocks } = parseMarkdown("# 海岸旅行计划\n\n第一行\n第二行\n\n> 先看天气\n\n---\n\n标题\n===");
    expect(blocks.map((b) => b.t)).toEqual(["heading", "p", "quote", "hr", "heading"]);
    expect(blocks[0]).toMatchObject({ level: 1, text: "海岸旅行计划" });
    expect(plainText((blocks[1] as Extract<Block, { t: "p" }>).c)).toBe("第一行\n第二行");
    expect(blocks[4]).toMatchObject({ level: 1, text: "标题" });
  });

  it("keeps a fenced block verbatim, markup and all", () => {
    const { blocks } = parseMarkdown("```go\nfunc main() {\n  # not a heading\n  <b>x</b>\n}\n```\nafter");
    expect(blocks[0]).toEqual({ t: "code", lang: "go", v: "func main() {\n  # not a heading\n  <b>x</b>\n}" });
    expect(blocks[1].t).toBe("p");
  });

  it("an unclosed fence runs to the end of the file", () => {
    const { blocks } = parseMarkdown("~~~\na\n\nb");
    expect(blocks).toEqual([{ t: "code", lang: "", v: "a\n\nb" }]);
  });

  it("nests lists by indentation and reads task boxes", () => {
    const { blocks } = parseMarkdown("- 相机\n  - 电池\n- [x] 外套\n- [ ] 地图\n\n1. 一\n2. 二");
    const [bullets, numbers] = blocks as Extract<Block, { t: "list" }>[];
    expect(bullets.ordered).toBe(false);
    expect(bullets.tight).toBe(true);
    expect(bullets.items.map((i) => i.checked)).toEqual([null, true, false]);
    expect(bullets.items[0].c.map((b) => b.t)).toEqual(["p", "list"]);
    expect(numbers).toMatchObject({ ordered: true, start: 1 });
    expect(numbers.items).toHaveLength(2);
  });

  it("a blank line between items makes the list loose", () => {
    const { blocks } = parseMarkdown("- a\n\n- b");
    expect(blocks).toHaveLength(1);
    expect((blocks[0] as Extract<Block, { t: "list" }>).tight).toBe(false);
  });

  it("a year in a sentence does not start a list", () => {
    const { blocks } = parseMarkdown("出发是在\n2019. 年底");
    expect(blocks.map((b) => b.t)).toEqual(["p"]);
  });

  it("reads a table with its alignment and pads short rows", () => {
    const { blocks } = parseMarkdown("| 名称 | 大小 |\n|:---|---:|\n| a.jpg | 4 MB |\n| b \\| c |");
    const table = blocks[0] as Extract<Block, { t: "table" }>;
    expect(table.align).toEqual(["left", "right"]);
    expect(table.rows.map((r) => r.map(plainText))).toEqual([
      ["a.jpg", "4 MB"],
      ["b | c", ""],
    ]);
  });

  it("raw HTML stays text", () => {
    const { blocks } = parseMarkdown('<script>alert(1)</script>\n<img src="http://x/y.png" onerror="z">');
    expect(blocks).toHaveLength(1);
    expect(plainText((blocks[0] as Extract<Block, { t: "p" }>).c)).toBe('<script>alert(1)</script>\n<img src="http://x/y.png" onerror="z">');
  });

  it("gives every heading a distinct anchor for the outline", () => {
    const { headings } = parseMarkdown("# 准备 Notes\n## 准备 Notes\n## Q&A!");
    expect(headings.map((h) => h.anchor)).toEqual(["准备-notes", "准备-notes-1", "qa"]);
  });

  it("resolves reference links defined after their use, and drops the definitions", () => {
    const { blocks } = parseMarkdown("见 [官网][pk] 与 [PK]\n\n[pk]: https://example.com \"PKP\"");
    expect(blocks).toHaveLength(1);
    const links = (blocks[0] as Extract<Block, { t: "p" }>).c.filter((n) => n.t === "link");
    expect(links).toMatchObject([
      { href: "https://example.com", title: "PKP" },
      { href: "https://example.com", title: "PKP" },
    ]);
  });
});

describe("parseInline", () => {
  it("reads emphasis, code and strikethrough", () => {
    expect(inline("**粗** *斜* ~~删~~ `a*b*c`")).toEqual<Inline[]>([
      { t: "strong", c: [{ t: "text", v: "粗" }] },
      { t: "text", v: " " },
      { t: "em", c: [{ t: "text", v: "斜" }] },
      { t: "text", v: " " },
      { t: "del", c: [{ t: "text", v: "删" }] },
      { t: "text", v: " " },
      { t: "code", v: "a*b*c" },
    ]);
  });

  it("leaves snake_case, arithmetic and lone marks alone", () => {
    expect(inline("file_name_v2 和 2 * 3 * 4 以及 1~3 天")).toEqual([{ t: "text", v: "file_name_v2 和 2 * 3 * 4 以及 1~3 天" }]);
    expect(inline("**没有收尾")).toEqual([{ t: "text", v: "**没有收尾" }]);
  });

  it("nests strong inside emphasis", () => {
    expect(inline("*a **b** c*")).toEqual([
      { t: "em", c: [{ t: "text", v: "a " }, { t: "strong", c: [{ t: "text", v: "b" }] }, { t: "text", v: " c" }] },
    ]);
  });

  it("reads escapes and hard breaks", () => {
    expect(inline("\\*不是强调\\*  \n下一行")).toEqual([{ t: "text", v: "*不是强调*" }, { t: "br" }, { t: "text", v: "下一行" }]);
  });

  it("reads links and images, keeping the image a reference only", () => {
    expect(inline('[文档](https://a.b/c "标题") ![海边](photo.jpg)')).toEqual([
      { t: "link", href: "https://a.b/c", title: "标题", c: [{ t: "text", v: "文档" }] },
      { t: "text", v: " " },
      { t: "image", alt: "海边", src: "photo.jpg" },
    ]);
  });

  it("links bare addresses without swallowing the sentence's punctuation", () => {
    expect(inline("见 https://example.com/a_(b)。然后")).toEqual([
      { t: "text", v: "见 " },
      { t: "link", href: "https://example.com/a_(b)", title: "", c: [{ t: "text", v: "https://example.com/a_(b)" }] },
      { t: "text", v: "。然后" },
    ]);
    expect(inline("(https://example.com).")[1]).toMatchObject({ href: "https://example.com" });
  });
});

describe("linkTarget", () => {
  it("lets only web and mail addresses leave, and #anchors move within", () => {
    expect(linkTarget("https://example.com")).toBe("external");
    expect(linkTarget("mailto:a@b.c")).toBe("external");
    expect(linkTarget("#出发前")).toBe("anchor");
    expect(linkTarget("javascript:alert(1)")).toBeNull();
    expect(linkTarget("file:///etc/passwd")).toBeNull();
    expect(linkTarget("notes/other.md")).toBeNull();
    expect(linkTarget("#")).toBeNull();
  });
});
