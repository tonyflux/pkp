<script setup lang="ts">
// One exchange of a conversation: the user's words on the
// right, then the assistant's — its text, the read-only steps it took as
// chips (never the raw tool JSON), the cards those steps made, and why the
// turn ended early when it did. The running turn is drawn by the same
// component from assistant.live, with the text still streaming in.
import { computed, onBeforeUpdate, onUpdated, ref } from "vue";
import { Browser } from "@wailsio/runtime";
import AssistantCard from "./AssistantCard.vue";
import AssistantSearchResults from "./AssistantSearchResults.vue";
import AssistantCopyButton from "./AssistantCopyButton.vue";
import MarkdownDoc from "./MarkdownDoc";
import Icon from "./Icon.vue";
import Orbit from "./Orbit.vue";
import { indexBlocked, type AssistantScope, type Card, type SearchResultGroup, type Trace, type Turn } from "../agent";
import { t } from "../i18n";
import { assistantScopeLabel, drive, indexReady, indexText, notify, openAISettings, retryTurn } from "../store";
import { linkTarget, parseMarkdown } from "../markdown";

const props = defineProps<{
  turnId: string;
  user: string;
  scope?: AssistantScope;
  text: string;
  traces: readonly Trace[];
  cards: readonly Card[];
  resultGroups?: readonly SearchResultGroup[];
  /** The finished turn, for how it ended; null while it runs. */
  turn: Turn | null;
  /** The user's avatar letter. */
  initial: string;
  /** Plan card numbers across the conversation (Plan draft / 01). */
  numbers: Record<string, number>;
  /** The latest safe retry, including a turn blocked by an unfinished index. */
  retry?: boolean;
}>();

const live = computed(() => !props.turn);
const indexRetry = computed(() => !!props.retry && !!props.turn && indexBlocked(props.turn));

// Only move a complete top-level closing paragraph after the cards. Parsing
// first keeps fenced code and loose/nested lists intact, including while live.
const blocks = computed(() => parseMarkdown(props.text).blocks);
const hasTrailing = computed(() => props.cards.length > 0 && !live.value && blocks.value.length > 1 && blocks.value.at(-1)?.t === "p");
const leading = computed(() => hasTrailing.value ? blocks.value.slice(0, -1) : blocks.value);
const trailing = computed(() => hasTrailing.value ? blocks.value.slice(-1) : []);
const content = ref<HTMLElement | null>(null);

// Updating a streaming text node resets browser selection offsets, even when
// Vue reuses that node. Keep the selected prefix selected as more text arrives.
let selected: { anchor: Node; anchorOffset: number; focus: Node; focusOffset: number } | null = null;
onBeforeUpdate(() => {
  selected = null;
  const selection = window.getSelection();
  if (!selection || selection.isCollapsed || !selection.anchorNode || !selection.focusNode) return;
  if (!content.value?.contains(selection.anchorNode) && !content.value?.contains(selection.focusNode)) return;
  selected = {
    anchor: selection.anchorNode,
    anchorOffset: selection.anchorOffset,
    focus: selection.focusNode,
    focusOffset: selection.focusOffset,
  };
});
onUpdated(() => {
  const saved = selected;
  selected = null;
  if (!saved || !saved.anchor.isConnected || !saved.focus.isConnected) return;
  const length = (node: Node) => node.nodeType === Node.TEXT_NODE ? node.textContent!.length : node.childNodes.length;
  const selection = window.getSelection();
  if (!selection || (selection.anchorNode === saved.anchor && selection.anchorOffset === saved.anchorOffset &&
    selection.focusNode === saved.focus && selection.focusOffset === saved.focusOffset)) return;
  selection.setBaseAndExtent(
    saved.anchor, Math.min(saved.anchorOffset, length(saved.anchor)),
    saved.focus, Math.min(saved.focusOffset, length(saved.focus)),
  );
});

function followLink(href: string) {
  if (!linkTarget(href)) return;
  if (href.startsWith("#")) {
    let anchor = href.slice(1);
    try {
      anchor = decodeURIComponent(anchor);
    } catch {
      // A malformed escape is looked up as written.
    }
    content.value?.querySelector<HTMLElement>(`[data-anchor="${CSS.escape(anchor.toLowerCase())}"]`)?.scrollIntoView({ block: "nearest", behavior: "auto" });
    return;
  }
  void Browser.OpenURL(href).catch((err: unknown) =>
    notify(t("无法打开链接：{error}", { error: err instanceof Error ? err.message : String(err) }), "error"),
  );
}

const ending = computed(() => {
  const done = props.turn;
  if (!done) return null;
  if (done.error) return { tone: "error", text: done.error };
  if (done.denied || done.stop === "denied") return { tone: "info", text: done.notice || t("未发送：保留本地结果") };
  if (done.notice) return { tone: done.stop === "cancel" ? "info" : "warning", text: done.notice };
  return null;
});

// Successful searches live in the collapsed process, not repeated status chips.
const shownTraces = computed(() => props.traces.filter((step) => step.tool !== "search_files" || step.state !== "done"));
const shown = computed(() => {
  const hasPlan = props.cards.some((card) => card.kind === "plan");
  return props.cards.filter((card) => card.kind !== "files" || (!hasPlan && card.query == null));
});
</script>

<template>
  <div class="ai-turn" :class="{ live }" :data-turn="turnId">
    <div v-if="user" class="user-turn">
      <span class="bubble"><span>{{ user }}</span><small v-if="scope" class="request-scope">{{ t("范围：{name}", { name: assistantScopeLabel(scope) }) }}</small></span>
      <b class="avatar" aria-hidden="true">{{ initial }}</b>
    </div>
    <article class="assistant-turn" :aria-busy="live">
      <span class="mark" aria-hidden="true"><Icon name="spark" :size="19" /></span>
      <div ref="content" class="content">
        <MarkdownDoc v-if="leading.length" class="answer" :blocks="leading" @link="followLink">
          <template #code="{ code, language }">
            <div class="answer-code">
              <div class="code-toolbar"><span>{{ language }}</span><AssistantCopyButton :text="code" :label="t('复制代码')" /></div>
              <pre class="md-code"><code>{{ code }}</code></pre>
            </div>
          </template>
        </MarkdownDoc>
        <span v-if="live && leading.length" class="caret" aria-hidden="true" />
        <p v-if="live && !leading.length" class="say thinking"><Orbit :size="14" /> {{ t("正在处理…") }}</p>

        <div v-if="shownTraces.length" class="trace" :aria-label="t('已执行的步骤')">
          <span v-for="(step, i) in shownTraces" :key="i" :class="step.state" :title="step.detail || undefined">
            <Orbit v-if="step.state === 'running'" :size="11" />
            <Icon v-else :name="step.state === 'failed' ? 'alert' : 'check'" :size="12" />
            {{ step.label }}
          </span>
          <span class="untouched">{{ t("未修改云盘") }}</span>
        </div>

        <AssistantSearchResults :cards="cards" :result-groups="resultGroups" :live="live" :turn-id="turnId" />
        <AssistantCard v-for="c in shown" :key="c.id" :card="c" :turn-id="turnId" :number="numbers[c.id] ?? 1" :live="live" />

        <MarkdownDoc v-if="trailing.length" class="answer after" :blocks="trailing" @link="followLink" />
        <div v-if="indexRetry" class="index-retry" role="status" aria-live="polite">
          <Icon v-if="indexReady" name="check" :size="17" />
          <Orbit v-else :size="17" />
          <div class="index-copy">
            <strong>{{ indexReady ? t("索引已就绪") : t("正在建立索引") }}</strong>
            <span>{{ indexReady ? t("现在可以使用原来的问题和范围重新查询。") : t("当前暂时无法搜索文件。请等待索引完成后点击“重试原问题”，无需重新输入。") }}</span>
            <small v-if="drive.indexProgress">{{ indexText(drive.indexProgress) }}</small>
          </div>
          <button class="primary" type="button" :disabled="!indexReady" @click="retryTurn(turnId)">{{ t("重试原问题") }}</button>
        </div>
        <p v-if="ending" class="ending" :class="ending.tone">
          {{ ending.text }}
          <button v-if="retry && !indexRetry" type="button" class="quiet retry" @click="retryTurn(turnId)"><Icon name="restore" :size="13" />{{ t("重试") }}</button>
        </p>
        <p v-if="turn?.stop === 'budget'" class="ending info">
          {{ t("可前往 AI 设置，调高“单个对话上限”或开启“不限制”，下一轮生效。模型费用仍按实际用量计费。") }}
          <button type="button" class="quiet retry" @click="openAISettings()">{{ t("前往 AI 设置") }}<Icon name="arrowRight" :size="13" /></button>
        </p>
        <p v-if="turn?.modeNote" class="ending info">{{ turn.modeNote }}</p>
        <footer v-if="!live && text.trim()" class="answer-footer"><AssistantCopyButton :text="text" :label="t('复制回答')" /></footer>
      </div>
    </article>
  </div>
</template>

<style scoped>
.ai-turn {
  margin-bottom: 30px;
}

.user-turn {
  display: flex;
  align-items: flex-end;
  justify-content: flex-end;
  gap: 10px;
  margin: 0 0 29px;
}

.bubble {
  max-width: 80%;
  padding: 12px 16px;
  border: 1px solid color-mix(in srgb, var(--accent) 22%, transparent);
  border-radius: 16px 16px 5px 16px;
  background: color-mix(in srgb, var(--accent) 16%, transparent);
  font-size: 13px;
  line-height: 1.65;
  white-space: pre-wrap;
  overflow-wrap: anywhere;
  -webkit-user-select: text;
  user-select: text;
  --default-contextmenu: show;
}

.request-scope { display: block; margin-top: 6px; font-size: 11px; color: var(--fg-dim); }

.avatar {
  display: grid;
  place-items: center;
  flex: none;
  width: 30px;
  height: 30px;
  border-radius: 50%;
  background: color-mix(in srgb, var(--accent) 22%, var(--canvas-flat));
  color: var(--accent);
  font-size: 12px;
}

.assistant-turn {
  display: flex;
  align-items: flex-start;
  gap: 13px;
}

.mark {
  display: grid;
  place-items: center;
  flex: none;
  width: 30px;
  height: 30px;
  color: var(--accent);
}

.content {
  flex: 1;
  min-width: 0;
}

.say {
  margin: 0 0 10px;
  font-size: 14px;
  line-height: 1.8;
  white-space: pre-wrap;
  overflow-wrap: anywhere;
}

.answer.after {
  margin-top: 18px;
}

.answer {
  font-size: 14px;
  line-height: 1.8;
  overflow-wrap: anywhere;
  -webkit-user-select: text;
  user-select: text;
  --default-contextmenu: show;
}

.answer :deep(p) { margin: 0 0 10px; white-space: pre-wrap; }
.answer :deep(h1), .answer :deep(h2), .answer :deep(h3),
.answer :deep(h4), .answer :deep(h5), .answer :deep(h6) {
  margin: 18px 0 8px;
  font-size: 1.05em;
  line-height: 1.5;
}
.answer :deep(h1) { font-size: 1.3em; }
.answer :deep(h2) { font-size: 1.15em; }
.answer :deep(> :first-child) { margin-top: 0; }
.answer :deep(ul), .answer :deep(ol) { margin: 8px 0 12px; padding-left: 24px; }
.answer :deep(li) { margin: 3px 0; }
.answer :deep(blockquote) {
  margin: 10px 0;
  padding: 2px 12px;
  border-left: 3px solid var(--line);
  color: var(--fg-dim);
}
.answer :deep(a) { color: var(--accent); text-decoration: underline; text-underline-offset: 3px; }
.answer :deep(code) {
  padding: 2px 4px;
  border-radius: 4px;
  background: color-mix(in srgb, var(--fg) 7%, transparent);
  font-family: var(--font-mono, monospace);
  font-size: 0.9em;
}
.answer-code {
  margin: 12px 0;
  border: 1px solid var(--line);
  border-radius: 9px;
  background: color-mix(in srgb, var(--fg) 4%, transparent);
  overflow: hidden;
}
.code-toolbar {
  display: flex;
  align-items: center;
  justify-content: space-between;
  flex-wrap: wrap;
  gap: 4px 10px;
  padding: 5px 9px;
  border-bottom: 1px solid var(--line);
  color: var(--fg-dim);
  font-size: 11px;
  -webkit-user-select: none;
  user-select: none;
}
.answer :deep(.md-code) {
  margin: 0;
  padding: 12px;
  overflow-x: auto;
  white-space: pre;
  overflow-wrap: normal;
  line-height: 1.6;
}
.answer :deep(.md-code code) { padding: 0; background: none; }
.answer :deep(.md-table) { margin: 12px 0; overflow-x: auto; }
.answer :deep(table) { border-collapse: collapse; font-size: 13px; }
.answer :deep(th), .answer :deep(td) { padding: 6px 10px; border: 1px solid var(--line); }
.answer :deep(th) { background: color-mix(in srgb, var(--fg) 5%, transparent); }
.answer :deep(hr) { margin: 16px 0; border: 0; border-top: 1px solid var(--line); }
.answer :deep(.box) {
  display: inline-block;
  width: 12px;
  height: 12px;
  margin-right: 6px;
  border: 1px solid var(--fg-dim);
  border-radius: 3px;
  vertical-align: -1px;
}
.answer :deep(.box.on) { background: var(--accent); border-color: var(--accent); }
.answer-footer { margin-top: 12px; }

.thinking {
  display: flex;
  align-items: center;
  gap: 8px;
  color: var(--fg-dim);
}

/* The streaming caret; a steady bar where motion is reduced. */
.caret {
  display: inline-block;
  width: 2px;
  height: 1em;
  margin-left: 2px;
  vertical-align: -0.15em;
  background: var(--accent);
  animation: blink 1s steps(1) infinite;
}

@keyframes blink {
  50% {
    opacity: 0;
  }
}

.trace {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 8px;
  margin: 15px 0 18px;
}

.trace > span {
  display: inline-flex;
  align-items: center;
  gap: 5px;
  max-width: 100%;
  padding: 5px 9px;
  border-radius: 7px;
  background: color-mix(in srgb, var(--fg) 6%, transparent);
  font-size: 10px;
  color: var(--fg-dim);
  overflow-wrap: anywhere;
}

.trace > span > svg {
  flex: none;
  color: var(--success);
}

.trace > span.failed > svg {
  color: var(--danger);
}

.ending {
  margin: 8px 0 0;
  font-size: 12px;
  line-height: 1.6;
  color: var(--fg-dim);
  overflow-wrap: anywhere;
}

.ending.warning {
  color: var(--warning);
}

.ending.error {
  color: var(--danger);
}

.index-retry {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 12px;
  margin: 14px 0;
  padding: 14px;
  border: 1px solid var(--line-strong);
  border-radius: 12px;
  background: var(--accent-soft);
  font-size: 12px;
  line-height: 1.6;
}

.index-copy { flex: 1 1 230px; min-width: 0; }
.index-copy strong, .index-copy span, .index-copy small { display: block; }
.index-copy span, .index-copy small { color: var(--fg-dim); }
.index-copy small { margin-top: 4px; font-size: 11px; }
.index-retry button { flex: none; min-height: 36px; padding: 7px 12px; border-radius: 9px; font-size: 12px; }

.retry {
  display: inline-flex;
  align-items: center;
  gap: 4px;
  min-height: 0;
  margin-left: 6px;
  padding: 2px 8px;
  border-radius: 7px;
  font-size: 12px;
  vertical-align: baseline;
}

@media (max-width: 550px) {
  .bubble {
    max-width: 85%;
  }

  .assistant-turn {
    gap: 5px;
  }

  .mark {
    width: 25px;
  }
}

@media (prefers-reduced-motion: reduce) {
  .caret {
    animation: none;
  }
}
</style>
