// Markdown is rendered from text nodes. Images use only the supplied sibling
// resolver; unsupported links remain text and styles stay with each consumer.
import { defineComponent, h, type PropType, type VNodeArrayChildren } from "vue";
import { t } from "../i18n";
import { linkTarget, type Block, type Inline } from "../markdown";
import MarkdownImage from "./MarkdownImage.vue";

export default defineComponent({
  name: "MarkdownDoc",
  props: {
    blocks: { type: Array as PropType<Block[]>, required: true },
    resolveImage: { type: Function as PropType<(relative: string) => Promise<string | null>>, required: false },
    releaseImage: { type: Function as PropType<(url: string) => void>, required: false },
  },
  emits: {
    /** A link was clicked: an http(s)/mailto address or a #heading. */
    link: (_href: string) => true,
  },
  setup(props, { emit, slots }) {
    function inline(nodes: Inline[]): VNodeArrayChildren {
      return nodes.map((n) => {
        switch (n.t) {
          case "text":
            return n.v;
          case "br":
            return h("br");
          case "code":
            return h("code", n.v);
          case "strong":
          case "em":
          case "del":
            return h(n.t, inline(n.c));
          case "image":
            return props.resolveImage && props.releaseImage
              ? h(MarkdownImage, { src: n.src, alt: n.alt, resolveImage: props.resolveImage, releaseImage: props.releaseImage })
              : h("span", { class: "md-image", title: n.src || undefined }, t("图片：{alt}", { alt: n.alt || t("未命名") }));
          case "link": {
            if (!linkTarget(n.href)) return h("span", { class: "md-link-off", title: n.href || undefined }, inline(n.c));
            return h(
              "a",
              {
                href: n.href,
                title: n.title || n.href,
                onClick: (event: MouseEvent) => {
                  event.preventDefault();
                  emit("link", n.href);
                },
              },
              inline(n.c),
            );
          }
        }
      });
    }

    function block(b: Block, tight = false): VNodeArrayChildren {
      switch (b.t) {
        case "heading":
          return [h(`h${b.level}`, { "data-anchor": b.anchor }, inline(b.c))];
        case "p":
          // A tight list's items are their text, not paragraphs.
          return tight ? inline(b.c) : [h("p", inline(b.c))];
        case "quote":
          return [h("blockquote", b.c.flatMap((c) => block(c)))];
        case "list":
          return [
            h(
              b.ordered ? "ol" : "ul",
              { start: b.ordered && b.start !== 1 ? b.start : undefined },
              b.items.map((item) =>
                h("li", { class: item.checked === null ? undefined : "task" }, [
                  item.checked === null
                    ? null
                    : h("span", { class: ["box", { on: item.checked }], role: "img", "aria-label": item.checked ? t("已完成") : t("未完成") }),
                  ...item.c.flatMap((c) => block(c, b.tight)),
                ]),
              ),
            ),
          ];
        case "code":
          return slots.code?.({ code: b.v, language: b.lang }) ?? [h("pre", { class: "md-code", "data-lang": b.lang || undefined }, h("code", b.v))];
        case "hr":
          return [h("hr")];
        case "table":
          return [
            h("div", { class: "md-table" }, [
              h("table", [
                h("thead", [h("tr", b.head.map((cell, k) => h("th", { style: { textAlign: b.align[k] || undefined } }, inline(cell))))]),
                h(
                  "tbody",
                  b.rows.map((row) => h("tr", row.map((cell, k) => h("td", { style: { textAlign: b.align[k] || undefined } }, inline(cell))))),
                ),
              ]),
            ]),
          ];
      }
    }

    return () => h("div", { class: "md" }, props.blocks.flatMap((b) => block(b)));
  },
});
