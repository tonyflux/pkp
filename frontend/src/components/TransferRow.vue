<script setup lang="ts">
// One task on the transfer list: a file tile,
// the name and where it went, the state in words, and only the actions that
// state has. A running row carries its progress line; a finished one is a
// quiet line of the completed list — no full bar, no red trash. A click opens
// the details island; ⌘/Ctrl-click, the tile's pick box or Space adds it to a
// batch, ⇧-click extends one. Its buttons do their own thing. Under All accounts
// the row names its account (owner); a row of an account that needs signing
// back in offers only Log in again.
import { computed } from "vue";
import AccountIdentity from "./AccountIdentity.vue";
import Icon from "./Icon.vue";
import { bytes, stamp } from "../format";
import {
  accountName,
  cancelUpload,
  openDownload,
  openSignIn,
  pauseDownload,
  removeTask,
  resumeDownload,
  retryUpload,
  revealDownload,
  revealFile,
  viewFile,
} from "../store";
import { groupOf, stateLabel, type TransferItem, type TransferState } from "../transfers";
import { t } from "../i18n";
import type { Account } from "../../bindings/github.com/tonyflux/pkp/internal/services";

const props = withDefaults(
  defineProps<{ item: TransferItem; open: boolean; picked: boolean; selecting: boolean; owner?: Account | null; last?: boolean }>(),
  { owner: null, last: false },
);
const emit = defineEmits<{ open: []; toggle: []; range: [] }>();

function onClick(event: MouseEvent) {
  if (event.shiftKey) emit("range");
  else if (event.metaKey || event.ctrlKey) emit("toggle");
  else emit("open");
}

// ⇧-click picks a range; it must not also select the rows' text.
function onMousedown(event: MouseEvent) {
  if (event.shiftKey) event.preventDefault();
}

const STATE_ICON: Record<TransferState, string> = {
  running: "clock",
  waiting: "clock",
  paused: "pause",
  failed: "alert",
  cancelled: "close",
  interrupted: "stop",
  done: "check",
};

const group = computed(() => groupOf(props.item.state));
const label = computed(() => {
  const parts = [props.item.name, stateLabel(props.item)];
  if (props.owner) parts.push(accountName(props.owner.id));
  return parts.join("，");
});
// Waiting on its account's sign-in (Login required / Pending sync): neither moving nor
// failed, so it takes the warning tone instead of its last known state's.
const stale = computed(() => {
  const it = props.item;
  return it.blocked && it.state !== "done" && !(it.kind === "cloud" && it.state === "failed");
});
const look = computed(() => (stale.value ? "stale" : props.item.state));
const tile = computed(() => (props.item.category && props.item.category !== "other" ? props.item.category : "file"));

// The second line: what kind of transfer and where it goes while it runs,
// the reason when it failed, and where it went, how big and when once over.
// An upload or download that can continue says how much of it is already
// there — unless its progress bar is showing, whose figures say the same.
// The path is the one part allowed to give way when the row narrows.
const meta = computed(() => {
  const it = props.item;
  const parts: { text: string; shrink?: boolean; reason?: boolean }[] = [];
  if (it.state === "failed") {
    parts.push({ text: it.reason, reason: true });
  } else {
    if (group.value === "active") parts.push({ text: it.kind === "cloud" ? t("云添加") : it.kind === "download" ? t("下载到电脑") : t("本地上传") });
    if (it.savedTo) parts.push({ text: t("保存到 {path}", { path: it.savedTo }), shrink: true });
    if (it.resumable && (it.progress === null || it.blocked)) {
      const moved = { done: bytes(it.doneBytes), total: bytes(it.totalBytes) };
      parts.push({ text: it.kind === "download" ? t("已下载 {done} / {total}", moved) : t("已上传 {done} / {total}", moved) });
    }
    else if (group.value !== "active" && it.totalBytes) parts.push({ text: bytes(it.totalBytes) });
  }
  if (it.endedAt) parts.push({ text: stamp(it.endedAt) });
  return parts;
});
</script>

<template>
  <article
    class="task"
    :class="[look, { open, picked: picked && !open, selecting, owned: !!owner, last }]"
    tabindex="0"
    :aria-label="label"
    @click="onClick"
    @mousedown="onMousedown"
    @keydown.enter.self="emit('open')"
    @keydown.space.self.prevent="emit('toggle')"
  >
    <div class="top">
      <span class="tile">
        <button
          class="check"
          :class="{ on: picked }"
          role="checkbox"
          :aria-checked="picked"
          :aria-label="t('选择 {name}', { name: item.name })"
          @click.stop="emit('toggle')"
        >
          <Icon name="check" :size="12" />
        </button>
        <span class="file-icon" :class="tile" aria-hidden="true">
          <Icon :name="tile" :size="21" />
        </span>
      </span>

      <div class="text">
        <strong class="name" :title="item.name">{{ item.name }}</strong>
        <!-- The path gives way first; once too little of it would show, it
             takes a line of its own instead. A part that starts a line has
             its separator clipped by the text column. -->
        <div class="meta">
          <span v-for="(part, i) in meta" :key="i" class="part" :class="{ shrink: part.shrink, reason: part.reason }" :title="part.shrink ? part.text : undefined">{{
            part.text
          }}</span>
        </div>
      </div>

      <AccountIdentity v-if="owner" class="owner" :account="owner" :size="28" dense />

      <div class="side">
        <span class="state" :class="look">
          <!-- A moving transfer gets a beating mark instead of the clock it
               shares with waiting: the row says it is alive between readings. -->
          <i v-if="look === 'running'" class="beat" aria-hidden="true" />
          <Icon v-else :name="stale ? 'alert' : STATE_ICON[item.state]" :size="14" />
          <span class="state-text">{{ stateLabel(item) }}</span>
        </span>

        <!-- Only real actions sit on the row; everything else is in the
             details it opens. A signed-out account exposes only sign-in. -->
        <template v-if="item.blocked">
          <button class="act" :title="t('重新登录「{name}」', { name: accountName(item.account) })" @click.stop="openSignIn(item.account)">{{ t("重新登录") }}</button>
        </template>
        <template v-else-if="item.kind === 'download' && (item.state === 'running' || item.state === 'waiting')">
          <button class="act" :title="t('暂停，已下载的部分保留')" @click.stop="pauseDownload(item)">{{ t("暂停") }}</button>
        </template>
        <template v-else-if="item.kind === 'download' && (item.state === 'paused' || group === 'attention')">
          <button v-if="item.resumable" class="act" :title="t('从中断的地方继续下载')" @click.stop="resumeDownload(item)">{{ t("继续") }}</button>
          <button v-else class="act" :title="t('重新下载')" @click.stop="resumeDownload(item)">{{ t("重试") }}</button>
        </template>
        <template v-else-if="item.kind === 'download' && group === 'done'">
          <span class="quick">
            <button class="mini" :title="t('打开')" :aria-label="t('打开 {name}', { name: item.name })" @click.stop="openDownload(item)">
              <Icon name="file" :size="16" />
            </button>
            <button class="mini" :title="t('在电脑上打开它所在的文件夹')" :aria-label="t('打开 {name} 所在的文件夹', { name: item.name })" @click.stop="revealDownload(item)">
              <Icon name="folder" :size="16" />
            </button>
          </span>
          <button class="mini" :title="t('详情')" :aria-label="t('{name} 的详情', { name: item.name })" @click.stop="emit('open')">
            <Icon name="info" :size="16" />
          </button>
        </template>
        <template v-else-if="item.kind === 'upload' && group === 'active'">
          <button class="act" :title="t('取消上传')" @click.stop="cancelUpload(item)">{{ t("取消") }}</button>
        </template>
        <template v-else-if="item.kind === 'upload' && group === 'attention'">
          <button v-if="item.resumable" class="act" :title="t('从中断的地方继续上传')" @click.stop="retryUpload(item)">{{ t("继续") }}</button>
          <button v-else class="act" :title="t('重新上传')" @click.stop="retryUpload(item)">{{ t("重试") }}</button>
        </template>
        <template v-else-if="item.kind === 'cloud' && item.fileGone">
          <!-- Its file was deleted after it finished: nothing to retry or to
               open; the record is all that is left. -->
          <button class="act" :title="t('只清理传输列表；文件已不在原处')" @click.stop="removeTask(item, false)">{{ t("移除记录") }}</button>
        </template>
        <template v-else-if="item.kind === 'cloud' && group !== 'done'">
          <button class="act" @click.stop="emit('open')">{{ item.state === "failed" ? t("查看原因") : t("查看详情") }}</button>
        </template>
        <template v-else-if="group === 'done'">
          <span v-if="item.fileId" class="quick">
            <button class="mini" :title="t('查看')" :aria-label="t('查看 {name}', { name: item.name })" @click.stop="viewFile(item)">
              <Icon name="eye" :size="16" />
            </button>
            <button class="mini" :title="t('打开所在文件夹')" :aria-label="t('打开 {name} 所在的文件夹', { name: item.name })" @click.stop="revealFile(item)">
              <Icon name="folder" :size="16" />
            </button>
          </span>
          <button class="mini" :title="t('详情')" :aria-label="t('{name} 的详情', { name: item.name })" @click.stop="emit('open')">
            <Icon name="info" :size="16" />
          </button>
        </template>
      </div>
    </div>

    <!-- Progress only exists for a transfer that is really moving: a
         waiting one says so in words, a finished one has its check. -->
    <div v-if="item.progress !== null && !item.blocked" class="progress">
      <div
        class="track"
        role="progressbar"
        :aria-label="t('{name} 的进度', { name: item.name })"
        :aria-valuenow="Math.round(item.progress)"
        aria-valuemin="0"
        aria-valuemax="100"
      >
        <span class="fill" :style="{ width: item.progress + '%' }" />
        <i class="head" :style="{ left: item.progress + '%' }" aria-hidden="true" />
      </div>
      <div class="data">
        <b>{{ item.progress.toFixed(0) }}%</b>
        <span v-if="item.totalBytes">{{ bytes(item.doneBytes, "0 B") }} / {{ bytes(item.totalBytes) }}</span>
        <span v-if="item.speed">{{ bytes(item.speed) }}/s</span>
        <span v-if="item.threads">{{ t("{n} 线程", { n: item.threads }) }}</span>
      </div>
    </div>
  </article>
</template>

<style scoped>
/* A card on the reading surface: flat, no backdrop blur per row (scrolling
   cost, and contrast on a textured scene), raised only by tint and edge. */
.task {
  position: relative;
  border-radius: 15px;
  border: 1px solid color-mix(in srgb, var(--accent) 12%, transparent);
  background: color-mix(in srgb, var(--fg) 4%, transparent);
  outline: none;
  cursor: default;
  transition:
    background var(--dur) var(--ease),
    border-color var(--dur) var(--ease),
    transform 0.22s var(--ease);
}

.task:hover {
  background: color-mix(in srgb, var(--fg) 6.5%, transparent);
  border-color: color-mix(in srgb, var(--accent) 27%, transparent);
  transform: translateY(-1px);
}

/* The row whose details are open, or the keyboard's, outranks every state tint. */
.task:focus-visible:not(.done),
.task.open:not(.done) {
  border-color: color-mix(in srgb, var(--accent) 65%, transparent);
}

/* Work in flight is lifted a little; nothing else is. */
.task.running {
  background: color-mix(in srgb, var(--accent) 8%, transparent);
  border-color: color-mix(in srgb, var(--accent) 24%, transparent);
}

/* A failure is only tinted: the reason, not the colour, says what happened. */
.task.failed {
  background: color-mix(in srgb, var(--danger) 8%, transparent);
  border-color: color-mix(in srgb, var(--danger) 22%, transparent);
}

/* Finished rows are lines of one list (the parent draws its frame). */
.task.done {
  border: 0;
  border-bottom: 1px solid var(--line);
  border-radius: 0;
  background: transparent;
}

/* The frame closes the list. A prop, not :last-child: the list draws only
   the rows near the visible part, so its last drawn row is rarely its last. */
.task.done.last {
  border-bottom: 0;
}

.task.done:hover {
  transform: none;
  background: color-mix(in srgb, var(--accent) 7%, transparent);
}

.task.done.open,
.task.done:focus-visible {
  box-shadow: inset 3px 0 0 var(--accent);
}

/* Picked for a batch: the accent edge and a faint wash, whatever the state. */
.task.picked {
  border-color: color-mix(in srgb, var(--accent) 65%, transparent);
  background: color-mix(in srgb, var(--accent) 12%, transparent);
}

.task.done.picked {
  background: color-mix(in srgb, var(--accent) 10%, transparent);
  box-shadow: inset 3px 0 0 var(--accent);
}

.top {
  display: grid;
  grid-template-columns: auto minmax(0, 1fr) auto;
  grid-template-areas: "tile text side";
  align-items: center;
  gap: 14px;
  padding: 14px 16px 14px 14px;
}

.tile {
  grid-area: tile;
}

.text {
  grid-area: text;
}

.side {
  grid-area: side;
}

/* All accounts: whose task it is sits between the text and the actions, never
   wider than its share, so the name keeps the room. */
.task.owned .top {
  grid-template-columns: auto minmax(0, 1fr) minmax(0, max-content) auto;
  grid-template-areas: "tile text owner side";
}

.owner {
  grid-area: owner;
  max-width: 200px;
}

/* Waiting on its account's sign-in: a warning tint, not a failure's. */
.task.stale {
  background: color-mix(in srgb, var(--warning) 7%, transparent);
  border-color: color-mix(in srgb, var(--warning) 22%, transparent);
}

.state.stale {
  color: var(--warning);
}

.task.done .top {
  padding-block: 10px;
}

.file-icon {
  --tint: var(--accent);
  width: 44px;
  height: 44px;
  display: grid;
  place-items: center;
  border-radius: 12px;
  color: var(--tint);
  background: linear-gradient(
    150deg,
    color-mix(in srgb, var(--tint) 24%, transparent),
    color-mix(in srgb, var(--tint) 9%, transparent)
  );
  border: 1px solid color-mix(in srgb, var(--tint) 26%, transparent);
}

.file-icon.archive,
.file-icon.document {
  --tint: var(--tint-violet);
}

/* The pick box has a column of its own before the tile: lined up on every
   row, centred on the tile, and never over the icon. The column is always
   kept, so nothing shifts when the boxes show — on hover and focus, and on
   every row while two or more are picked, so adding one needs no modifier. */
.tile {
  display: flex;
  align-items: center;
  gap: 12px;
}

/* The ring is a border: on a 1x screen WebKit draws a 1.5px inset-shadow
   ring lopsided (2px top and left, 1px bottom and right). */
.check {
  flex: none;
  width: 20px;
  height: 20px;
  min-height: 0;
  padding: 0;
  border: 1.5px solid var(--fg-dim);
  border-radius: 50%;
  display: grid;
  place-items: center;
  color: transparent;
  background: var(--canvas);
  box-shadow: 0 1px 3px rgba(0, 0, 0, 0.2);
  opacity: 0;
  transition: opacity var(--dur) var(--ease);
}

.task:hover .check,
.task:focus-within .check,
.task.selecting .check,
.check.on {
  opacity: 1;
}

.check:hover:not(:disabled) {
  border-color: var(--accent);
  background: var(--canvas);
}

.check.on,
.check.on:hover:not(:disabled) {
  color: var(--on-primary);
  border-color: transparent;
  background: var(--primary) border-box;
  box-shadow: none;
}

.task.failed .file-icon {
  --tint: var(--danger);
}

.text {
  min-width: 0;
}

.name {
  display: block;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  font-size: 15px;
  font-weight: 600;
  line-height: 21px;
}

/* Each part carries its separator in a 20px lead; the lead of the first
   part on every line sits left of the text column and is clipped away. */
.meta {
  --lead: 20px;
  display: flex;
  flex-wrap: wrap;
  margin: 3px 0 0 calc(-1 * var(--lead));
  clip-path: inset(0 0 0 var(--lead));
  font-size: 13px;
  line-height: 18px;
  color: var(--fg-dim);
  white-space: nowrap;
}

.part {
  position: relative;
  flex: none;
  padding-left: var(--lead);
}

.part::before {
  content: "·" / "";
  position: absolute;
  left: 0;
  width: var(--lead);
  text-align: center;
  opacity: 0.55;
}

/* The path keeps the line while at least a readable start of it fits,
   and takes the next line rather than shrink to a sliver. */
.part.shrink {
  flex: 1 1 calc(var(--lead) + 7.5em);
  max-width: max-content;
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
}

/* Why it failed is read whole: it wraps beside the time. */
.part.reason {
  flex: 1 1 12em;
  max-width: max-content;
  min-width: 0;
  white-space: normal;
}

.task.failed .meta {
  color: var(--danger);
}

.side {
  display: flex;
  align-items: center;
  justify-content: flex-end;
  gap: 10px;
  white-space: nowrap;
}

.state {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  min-width: 74px;
  font-size: 12px;
  font-weight: 600;
  color: var(--fg-dim);
}

.state.running {
  color: var(--accent);
}

/* The running mark: a dot the size of the icons it stands in for, with a
   ring that swells out of it and fades, once every 1.6s. */
.beat {
  position: relative;
  width: 14px;
  height: 14px;
  flex: none;
}

.beat::before,
.beat::after {
  content: "";
  position: absolute;
  inset: 4px;
  border-radius: 50%;
  background: currentColor;
}

.beat::after {
  animation: beat 1.6s cubic-bezier(0.2, 0.6, 0.35, 1) infinite;
}

@keyframes beat {
  from {
    transform: scale(1);
    opacity: 0.55;
  }
  to {
    transform: scale(2.6);
    opacity: 0;
  }
}

.state.done {
  color: var(--success);
}

.state.failed {
  color: var(--danger);
}

.act {
  min-height: 32px;
  padding: 0 13px;
  border: 0;
  border-radius: 10px;
  background: color-mix(in srgb, var(--accent) 14%, transparent);
  color: color-mix(in srgb, var(--accent) 55%, var(--fg));
  font-size: 12px;
  font-weight: 600;
}

.act:hover:not(:disabled) {
  background: color-mix(in srgb, var(--accent) 26%, transparent);
}

.quick {
  display: inline-flex;
  gap: 2px;
}

.mini {
  width: 32px;
  height: 32px;
  min-height: 32px;
  padding: 0;
  border: 0;
  border-radius: 10px;
  background: transparent;
  color: var(--fg-dim);
}

.mini:hover:not(:disabled) {
  background: color-mix(in srgb, var(--accent) 18%, transparent);
  color: var(--fg);
}

/* The bar starts under the name: past the pick box, the tile and the gaps
   (14 + 20 + 12 + 44 + 14). */
.progress {
  display: grid;
  grid-template-columns: minmax(0, 1fr) auto;
  align-items: center;
  gap: 16px;
  margin: -3px 18px 15px 104px;
}

/* The track does not clip: the head's glow spills past its 5px. The fill
   clips its own sheen. */
.track {
  position: relative;
  height: 5px;
  border-radius: 8px;
  background: color-mix(in srgb, var(--accent) 20%, transparent);
}

/* Readings land seconds apart (a cloud task's every ~10s): the fill and its
   head glide to the new figure instead of jumping, both on one curve. */
.fill,
.head {
  --glide: 1.2s cubic-bezier(0.22, 1, 0.36, 1);
}

.fill {
  position: relative;
  display: block;
  height: 100%;
  overflow: hidden;
  border-radius: inherit;
  background: linear-gradient(90deg, color-mix(in srgb, var(--accent) 62%, var(--primary-deep)), var(--accent));
  box-shadow: 0 0 12px color-mix(in srgb, var(--accent) 45%, transparent);
  transition: width var(--glide);
}

/* Light running along what has arrived, then a rest before the next pass. */
.fill::before {
  content: "";
  position: absolute;
  inset: 0;
  transform: translateX(-100%);
  background: linear-gradient(90deg, transparent 10%, color-mix(in srgb, #fff 55%, transparent) 50%, transparent 90%);
  animation: sheen 2.4s cubic-bezier(0.45, 0, 0.25, 1) infinite;
}

@keyframes sheen {
  70%,
  100% {
    transform: translateX(100%);
  }
}

/* The leading edge: a bright bead that breathes where the bytes are. */
.head {
  position: absolute;
  top: 50%;
  width: 9px;
  height: 9px;
  margin: -4.5px 0 0 -4.5px;
  border-radius: 50%;
  background: radial-gradient(circle, #fff 0 30%, var(--accent) 70%);
  box-shadow: 0 0 10px 2px color-mix(in srgb, var(--accent) 60%, transparent);
  transition: left var(--glide);
  animation: bead 1.6s ease-in-out infinite;
}

@keyframes bead {
  50% {
    transform: scale(0.72);
    box-shadow: 0 0 4px 0 color-mix(in srgb, var(--accent) 40%, transparent);
  }
}

.data {
  display: flex;
  gap: 14px;
  font-size: 12px;
  color: var(--fg-dim);
  font-variant-numeric: tabular-nums;
  white-space: nowrap;
}

.data b {
  font-weight: 600;
  color: var(--fg);
}

/* Narrow canvas (details beside it, small window): smaller tile,
   the figures under the bar instead of beside it. */
@container (max-width: 600px) {
  .top {
    gap: 10px;
    padding: 11px 12px 11px 10px;
  }

  .tile {
    gap: 8px;
  }

  .task.done .top {
    padding-block: 8px;
  }

  .file-icon {
    width: 36px;
    height: 36px;
    border-radius: 10px;
  }

  .name {
    font-size: 14px;
  }

  .meta {
    font-size: 12px;
  }

  .state {
    min-width: 0;
  }

  /* The owner moves under the name rather than squeezing it or the actions. */
  .task.owned .top {
    grid-template-columns: auto minmax(0, 1fr) auto;
    grid-template-areas:
      "tile text side"
      "tile owner side";
    row-gap: 6px;
  }

  .owner {
    max-width: 100%;
    justify-self: start;
  }

  /* The green check says done; the word gives its width to the path. */
  .task.done .state-text {
    display: none;
  }

  /* 10 + 20 + 8 + 36 + 10, as above. */
  .progress {
    grid-template-columns: 1fr;
    gap: 6px;
    margin: -2px 12px 12px 84px;
  }
}

/* Narrowest: the row keeps its name, state and progress; the actions move
   into the details the row opens. */
@container (max-width: 460px) {
  .act,
  .quick {
    display: none;
  }
}

@media (prefers-reduced-motion: reduce) {
  .task {
    transition: none;
  }

  .task:hover {
    transform: none;
  }
}
</style>
