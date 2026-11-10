<script setup lang="ts">
// A conversation: a reading column about 880 wide with the
// finished turns, then the turn in progress from assistant.live — its text
// streaming in, its steps as they run. The composer below adds the next
// turn; Stop there cancels this one. When the account's metadata is
// Always Allow no sheet asks, so the column says what the conversation has used.
import { computed, nextTick, onBeforeUnmount, onMounted, ref, watch } from "vue";
import AssistantTurn from "./AssistantTurn.vue";
import Icon from "./Icon.vue";
import type { Card } from "../agent";
import { formatTokens } from "../aiprompt";
import { t } from "../i18n";
import { accountById, accountName, assistant, assistantHome, retryTurnId } from "../store";

const conversation = computed(() => assistant.conversation);
const turns = computed(() => conversation.value?.turns ?? []);
// The running turn, until the finished one replaces it in the list.
const live = computed(() => {
  const l = assistant.live;
  return l && !turns.value.some((turn) => turn.id === l.turnId) ? { ...l, id: l.turnId } : null;
});
// One keyed list keeps the live message's DOM when it becomes a saved turn.
const exchanges = computed(() => (live.value ? [...turns.value, live.value] : turns.value));

const account = computed(() => accountName(assistant.account));
const initial = computed(() => Array.from(account.value)[0] ?? t("我"));

const title = computed(() => conversation.value?.title || turns.value[0]?.user || live.value?.user || t("新的对话"));

const planCards = computed(() => {
  const all: Card[] = [];
  for (const turn of turns.value) all.push(...turn.cards);
  if (live.value) all.push(...live.value.cards);
  return all.filter((c) => c.kind === "plan");
});

const numbers = computed(() => Object.fromEntries(planCards.value.map((c, i) => [c.id, i + 1])));

const standing = computed(() => {
  const cards = planCards.value;
  // The fallback Original Account is a generic word, not a name: no 「」.
  const who = account.value === t("原账号") ? account.value : t("「{name}」", { name: account.value });
  if (!cards.length) return t("这段对话属于{who}；只有你确认计划后才会修改云盘。", { who });
  if (cards.some((c) => c.state === "draft")) return t("这段对话属于{who}；计划还没有执行。", { who });
  if (cards.some((c) => c.state === "done")) return t("这段对话属于{who}；计划已执行。", { who });
  return t("这段对话属于{who}；没有待执行的计划。", { who });
});

const alwaysAllow = computed(() => !!accountById(assistant.account)?.aiConsent);
const usage = computed(() => conversation.value?.usage ?? null);

/* ---------- following the newest words ---------- */

const scroller = ref<HTMLElement | null>(null);
const pinned = ref(true);
let selecting = false;
let outer: HTMLElement | null = null;
let observer: ResizeObserver | null = null;

function scrollRegion() {
  const el = scroller.value;
  return el && getComputedStyle(el).overflowY === "visible" ? outer : el;
}

function hasSelection() {
  const selection = window.getSelection();
  const el = scroller.value;
  if (!selection || selection.isCollapsed || !el) return false;
  for (let i = 0; i < selection.rangeCount; i++) {
    if (selection.getRangeAt(i).intersectsNode(el)) return true;
  }
  return false;
}

function onSelection() {
  if (hasSelection()) pinned.value = false;
}

function onScroll() {
  const el = scrollRegion();
  if (el && !selecting) pinned.value = !hasSelection() && el.scrollHeight - el.scrollTop - el.clientHeight < 80;
}

function onPointerDown(event: PointerEvent) {
  if (event.button !== 0 || !(event.target instanceof Element) || event.target.closest("button, a, input, textarea, select")) return;
  selecting = true;
}

function onPointerUp() {
  selecting = false;
  onSelection();
}

async function follow() {
  await nextTick();
  const el = scrollRegion();
  if (hasSelection()) pinned.value = false;
  if (el && pinned.value && !selecting) el.scrollTop = el.scrollHeight;
}

function latest() {
  if (hasSelection()) window.getSelection()?.removeAllRanges();
  pinned.value = true;
  void follow();
}

watch(
  () => [turns.value.length, live.value?.turnId, live.value?.text.length, live.value?.traces.length, live.value?.cards.length],
  () => void follow(),
);
watch(
  () => conversation.value?.id,
  () => {
    pinned.value = true;
    void follow();
  },
);

onMounted(() => {
  outer = scroller.value?.closest<HTMLElement>(".assistant") ?? null;
  outer?.addEventListener("scroll", onScroll, { passive: true });
  document.addEventListener("selectionchange", onSelection);
  document.addEventListener("pointerup", onPointerUp);
  document.addEventListener("pointercancel", onPointerUp);
  observer = new ResizeObserver(() => void follow());
  if (scroller.value) observer.observe(scroller.value);
  if (scroller.value?.firstElementChild) observer.observe(scroller.value.firstElementChild);
  if (outer) observer.observe(outer);
  void follow();
});

onBeforeUnmount(() => {
  outer?.removeEventListener("scroll", onScroll);
  document.removeEventListener("selectionchange", onSelection);
  document.removeEventListener("pointerup", onPointerUp);
  document.removeEventListener("pointercancel", onPointerUp);
  observer?.disconnect();
});
</script>

<template>
  <div class="ai-chat">
    <div class="chat-nav">
      <button class="quiet back" type="button" :disabled="!!assistant.busy || !!assistant.live || assistant.running || !!assistant.consent || !!assistant.connect" @click="assistantHome()">
        <Icon name="chevronLeft" :size="16" />{{ t("返回助手首页") }}
      </button>
    </div>
    <div ref="scroller" class="chat-scroll" @scroll.passive="onScroll" @pointerdown="onPointerDown">
      <div class="chat-inner">
        <header class="chat-heading">
          <span class="eyebrow">{{ t("一段对话 · 一份计划") }}</span>
          <h1>{{ title }}</h1>
          <p>{{ standing }}</p>
        </header>

        <AssistantTurn
          v-for="turn in exchanges"
          :key="turn.id"
          :turn-id="turn.id"
          :user="turn.user"
          :scope="turn.scope"
          :text="turn.text"
          :traces="turn.traces"
          :cards="turn.cards"
          :result-groups="turn.resultGroups"
          :turn="'stop' in turn ? turn : null"
          :initial="initial"
          :numbers="numbers"
          :retry="turn.id === retryTurnId"
        />

        <p v-if="!turns.length && !live" class="empty">{{ t("说一句想找什么、想怎么整理。本地能完成的先在本地完成，需要模型时会先问你。") }}</p>

        <p v-if="alwaysAllow && usage && usage.used > 0" class="usage tnum">
          {{ t("本对话已用约 {n} tokens", { n: formatTokens(usage.used) }) }}<template v-if="usage.limit"> / {{ t("上限 {n}", { n: formatTokens(usage.limit) }) }}</template>
        </p>
      </div>
    </div>
    <div v-if="!pinned" class="latest-dock">
      <button class="latest" type="button" @click="latest"><Icon name="chevronDown" :size="14" />{{ t("回到最新") }}</button>
    </div>
  </div>
</template>

<style scoped>
.ai-chat {
  position: relative;
  flex: 1;
  min-height: 0;
  display: flex;
  flex-direction: column;
}

.chat-nav {
  flex: none;
  width: min(880px, calc(100% - 80px));
  margin: 20px auto 0;
}

.back {
  display: inline-flex;
  align-items: center;
  gap: 4px;
  min-height: 32px;
  margin-left: -8px;
  padding: 4px 8px;
  font-size: 12px;
  color: var(--accent);
}

.chat-scroll {
  flex: 1;
  min-height: 0;
  overflow-y: auto;
  overscroll-behavior: contain;
  scrollbar-width: thin;
}

.chat-inner {
  width: min(880px, calc(100% - 80px));
  margin: 0 auto;
  padding: 12px 0 25px;
}

.chat-heading {
  margin-bottom: 28px;
}

.eyebrow {
  font-size: 11px;
  font-weight: 650;
  letter-spacing: 0.08em;
  color: var(--accent);
}

.chat-heading h1 {
  margin: 12px 0 9px;
  font-size: 24px;
  line-height: 1.25;
  font-weight: 650;
  letter-spacing: -0.025em;
  overflow-wrap: anywhere;
}

.chat-heading p {
  margin: 0;
  font-size: 13px;
  line-height: 1.7;
  color: var(--fg-dim);
}

.empty {
  margin: 0;
  font-size: 13px;
  line-height: 1.7;
  color: var(--fg-dim);
}

.usage {
  margin: 8px 0 0 43px;
  font-size: 11px;
  color: var(--fg-dim);
}

.latest-dock {
  position: absolute;
  bottom: 12px;
  inset-inline: 0;
  display: flex;
  justify-content: center;
  pointer-events: none;
}

.latest {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  min-height: 32px;
  padding: 5px 13px;
  border: 1px solid var(--line-strong);
  border-radius: 999px;
  background: var(--surface);
  color: var(--fg);
  font-size: 12px;
  box-shadow: 0 3px 12px #0002;
  pointer-events: auto;
}

.latest:hover {
  border-color: var(--accent);
}

@media (max-width: 959px) {
  .chat-nav,
  .chat-inner {
    width: min(880px, calc(100% - 42px));
  }
}

@media (max-width: 550px) {
  .chat-nav {
    position: sticky;
    top: 0;
    z-index: 1;
    background: var(--canvas-flat);
    align-self: stretch;
    width: auto;
    margin: 0;
    padding: 14px 16px 0;
  }

  .ai-chat {
    flex: 1 0 auto;
    min-height: auto;
  }

  .latest-dock {
    position: sticky;
    bottom: 12px;
    flex: none;
    height: 32px;
    margin-top: -44px;
    margin-bottom: 12px;
  }

  .chat-scroll {
    flex: none;
    overflow: visible;
  }

  .chat-inner {
    width: calc(100% - 32px);
  }

  .chat-heading {
    margin-bottom: 19px;
  }

  .chat-heading h1 {
    font-size: 22px;
  }

  .usage {
    margin-left: 30px;
  }
}
</style>
