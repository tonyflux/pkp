<script setup lang="ts">
// Overview: the account, not the folder on screen. One chart —
// the storage ring — and two blocks under it, set apart by space and a
// hairline. Every figure comes from the account: a split of the storage by
// kind only when the backend could vouch for it, transfers from the
// transfer centre's own lists, plays from the play history. Missing data
// says so; nothing is estimated to fill the space.
import { computed } from "vue";
import { N_, t } from "../i18n";
import Icon from "./Icon.vue";
import Thumb from "./Thumb.vue";
import MembershipBadge from "./MembershipBadge.vue";
import { ago, bytes, clock, share, sizeParts } from "../format";
import {
  currentId,
  currentTasks,
  currentDownloads,
  currentUploads,
  drive,
  forgetPlay,
  loadQuota,
  openHistory,
  openTransfers,
  resumePlay,
} from "../store";
import { fromDownload, fromTask, fromUpload, groupOf, stateLabel, type TransferItem } from "../transfers";
import type { RecentPlay } from "../../bindings/github.com/tonyflux/pkp/internal/services";

/* ---------- storage ---------- */

// r = 72 − stroke/2: the ring's outer edge is the 144px box.
const R = 67;
const C = 2 * Math.PI * R;
// Under this share of the ring an arc is a speck; it is drawn with Other
// (the legend still gives its own figure).
const SPECK = 0.005;

type Kind = "video" | "image" | "document" | "other";
const KINDS: { key: Kind; label: string }[] = [
  { key: "video", label: N_("视频") },
  { key: "image", label: N_("图片") },
  { key: "document", label: N_("文档") },
  { key: "other", label: N_("其他") },
];

const quota = computed(() => drive.quota);
const unlimited = computed(() => !!quota.value?.isUnlimited);
const used = computed(() => Math.max(0, quota.value?.usage ?? 0));
const limit = computed(() => Math.max(0, quota.value?.limit ?? 0));
const remaining = computed(() => Math.max(0, limit.value - used.value));
const full = computed(() => !unlimited.value && limit.value > 0 && remaining.value === 0);
const breakdown = computed(() => quota.value?.breakdown ?? null);

// What the ring is a share of: the total, or the usage when it overflows
// the total, so the arcs never add up past the circle.
const whole = computed(() => Math.max(limit.value, used.value));

const arcs = computed(() => {
  if (unlimited.value || whole.value <= 0) return [];
  const parts: { key: string; value: number }[] = breakdown.value
    ? KINDS.map((k) => ({ key: k.key, value: breakdown.value![k.key] }))
    : [{ key: "used", value: used.value }];
  // Specks join Other in the drawing only.
  if (breakdown.value) {
    const other = parts[parts.length - 1];
    for (const part of parts.slice(0, -1)) {
      if (part.value > 0 && part.value / whole.value < SPECK) {
        other.value += part.value;
        part.value = 0;
      }
    }
  }
  let start = 0;
  return parts
    .filter((p) => p.value > 0)
    .map((p) => {
      const length = Math.min(C - start, (p.value / whole.value) * C);
      const arc = { key: p.key, length, start };
      start += length;
      return arc;
    });
});

const centre = computed(() => sizeParts(unlimited.value ? used.value : remaining.value));
const ringLabel = computed(() =>
  unlimited.value
    ? t("不限容量，已用 {used}", { used: bytes(used.value, "0 B", 1) })
    : t("总容量 {limit}，已用 {used}，剩余 {left}", {
        limit: bytes(limit.value, "0 B", 1),
        used: bytes(used.value, "0 B", 1),
        left: bytes(remaining.value, "0 B", 1),
      }),
);

function hm(iso: string): string {
  const d = new Date(iso);
  return Number.isNaN(d.getTime()) ? "" : `${String(d.getHours()).padStart(2, "0")}:${String(d.getMinutes()).padStart(2, "0")}`;
}

// Why there is no split by kind: the index is still being built, or it was
// built but its sums did not match the account's figure at the same time.
const noSplitReason = computed(() =>
  drive.indexProgress?.ready ? t("分类统计暂不可用：与账号用量不一致") : t("分类统计暂不可用：本地索引建立后显示"),
);

/* ---------- transfers ---------- */

// The account on screen only. Cloud tasks are live too: the
// store polls an account's list while one of its tasks is moving.
const cloudItems = computed(() => currentTasks.value.map((task) => fromTask(task)));
const uploadItems = computed(() => currentUploads.value.map((task) => fromUpload(task)));
const downloadItems = computed(() => currentDownloads.value.map((task) => fromDownload(task)));
const all = computed(() => [...uploadItems.value, ...downloadItems.value, ...cloudItems.value]);
// Moving work first, then what waits; at most two rows here.
const active = computed(() => {
  const rows = all.value.filter((item) => groupOf(item.state) === "active");
  return [...rows.filter((item) => item.state === "running"), ...rows.filter((item) => item.state !== "running")];
});
const attention = computed(() => {
  const cloud = cloudItems.value.filter((item) => groupOf(item.state) === "attention").length;
  const upload = uploadItems.value.filter((item) => groupOf(item.state) === "attention").length;
  const download = downloadItems.value.filter((item) => groupOf(item.state) === "attention").length;
  return { cloud, upload, download, total: cloud + upload + download };
});

function kindLabel(item: TransferItem): string {
  return item.kind === "upload" ? t("上传") : item.kind === "download" ? t("下载") : t("云添加");
}

function openAttention() {
  const a = attention.value;
  openTransfers(a.cloud ? "cloud" : a.upload ? "upload" : "download", "attention");
}

/* ---------- plays ---------- */

// A play has a progress worth drawing once it is past the first seconds of
// a file whose length is known; otherwise it is only Recently Opened.
function hasProgress(play: RecentPlay): boolean {
  return play.duration > 0 && play.position >= 5;
}

function progressOf(play: RecentPlay): number {
  return Math.min(100, (play.position / play.duration) * 100);
}
</script>

<template>
  <div class="overview">
    <!-- ---------- Storage Space ---------- -->
    <section class="storage" aria-labelledby="ov-storage">
      <div class="title">
        <h2 id="ov-storage">{{ t("存储空间") }}</h2>
        <span v-if="drive.quotaAt" class="stamp">{{ t("更新于 {time}", { time: hm(drive.quotaAt) }) }}</span>
      </div>
      <!-- The plan the space comes with; keyed so another account reads its own. -->
      <MembershipBadge v-if="currentId" :key="currentId" :account="currentId" class="membership" />

      <!-- Waiting: the ring's own size, and no figures — never a 0. -->
      <div v-if="!quota && !drive.quotaError" class="ring" aria-busy="true" :aria-label="t('正在读取容量')">
        <svg viewBox="0 0 144 144" aria-hidden="true"><circle class="track skel-ring" cx="72" cy="72" :r="R" /></svg>
      </div>

      <div v-else-if="!quota" class="unavailable">
        <p>{{ t("容量暂不可用") }}</p>
        <small>{{ drive.quotaError }}</small>
        <button class="retry" @click="loadQuota">{{ t("重试") }}</button>
      </div>

      <p v-else-if="!unlimited && limit === 0" class="unavailable">
        <span>{{ t("这个账号暂无可用空间") }}</span>
      </p>

      <template v-else>
        <div class="ring">
          <svg viewBox="0 0 144 144" role="img" :aria-label="ringLabel">
            <circle class="track" cx="72" cy="72" :r="R" />
            <g transform="rotate(-90 72 72)">
              <circle
                v-for="arc in arcs"
                :key="arc.key"
                class="arc"
                :class="arc.key"
                cx="72"
                cy="72"
                :r="R"
                :stroke-dasharray="`${arc.length} ${C - arc.length}`"
                :stroke-dashoffset="-arc.start"
              />
            </g>
          </svg>
          <div class="centre" aria-hidden="true">
            <small>{{ unlimited ? t("已用空间") : t("剩余空间") }}</small>
            <strong class="tnum">{{ centre.value }} <span>{{ centre.unit }}</span></strong>
          </div>
        </div>

        <div class="caption">
          <span class="tnum">
            <strong>{{ bytes(used, "0 B", 1) }}</strong>
            <span class="dim"> / {{ unlimited ? t("不限容量") : bytes(limit, "0 B", 1) }}</span>
          </span>
          <span v-if="full" class="full">{{ t("空间已满") }}</span>
          <span v-else-if="!unlimited" class="dim">{{ t("已使用 {pct}", { pct: share(used / limit) }) }}</span>
        </div>

        <dl v-if="breakdown" class="legend">
          <div v-for="k in KINDS" :key="k.key">
            <dt><i :class="k.key" />{{ t(k.label) }}</dt>
            <dd class="tnum">{{ bytes(breakdown[k.key], "0 B", 1) }}</dd>
          </div>
        </dl>
        <template v-else>
          <dl class="legend">
            <div>
              <dt><i class="used" />{{ t("已用") }}</dt>
              <dd class="tnum">{{ bytes(used, "0 B", 1) }}</dd>
            </div>
            <div v-if="!unlimited">
              <dt><i class="free" />{{ t("剩余") }}</dt>
              <dd class="tnum">{{ bytes(remaining, "0 B", 1) }}</dd>
            </div>
          </dl>
          <p class="note">{{ noSplitReason }}</p>
        </template>
        <p v-if="breakdown" class="note">{{ t("分类按本地索引统计，含回收站 · {time} 同步", { time: hm(breakdown.syncedAt) }) }}</p>

        <p v-if="drive.quotaError" class="stale" role="status">
          {{ t("刷新失败，显示 {time} 的数据", { time: hm(drive.quotaAt) }) }}
          <button class="retry" @click="loadQuota">{{ t("重试") }}</button>
        </p>
      </template>
    </section>

    <!-- ---------- In Progress ---------- -->
    <section class="block" aria-labelledby="ov-active">
      <div class="title">
        <h2 id="ov-active">{{ t("正在进行") }} <span class="badge tnum">{{ active.length }}</span></h2>
        <button class="link" @click="openTransfers()">{{ t("全部") }} <Icon name="chevronRight" :size="14" /></button>
      </div>

      <div v-for="item in active.slice(0, 2)" :key="item.key" class="task">
        <div class="task-head">
          <Icon :name="item.kind === 'upload' ? 'upload' : item.kind === 'download' ? 'download' : 'cloudAdd'" :size="18" />
          <div class="task-text">
            <strong :title="item.name">{{ item.name }}</strong>
            <small>{{ kindLabel(item) }} · {{ stateLabel(item) }}</small>
          </div>
          <span v-if="item.progress !== null" class="tnum pct">{{ Math.floor(item.progress) }}%</span>
        </div>
        <div
          v-if="item.progress !== null"
          class="bar"
          role="progressbar"
          :aria-label="t('{name} 进度', { name: item.name })"
          aria-valuemin="0"
          aria-valuemax="100"
          :aria-valuenow="Math.floor(item.progress)"
        >
          <i :style="{ width: `${item.progress}%` }" />
        </div>
        <div v-if="(item.progress !== null && item.totalBytes > 0) || item.savedTo" class="task-meta">
          <span v-if="item.progress !== null && item.totalBytes > 0" class="tnum">
            {{ bytes(item.doneBytes, "0 B", 1) }} / {{ bytes(item.totalBytes, "0 B", 1) }}
          </span>
          <span v-if="item.savedTo" class="to" :title="t('保存到 {path}', { path: item.savedTo })">{{ t("保存到 {path}", { path: item.savedTo }) }}</span>
        </div>
      </div>

      <p v-if="!active.length" class="empty">
        {{ t("暂无进行中的任务") }}
        <button class="link" @click="openTransfers()">{{ t("前往传输中心") }}</button>
      </p>

      <button v-if="attention.total" class="attention" @click="openAttention">
        <Icon name="alert" :size="16" />
        <span>{{ t("{n} 项需要处理", { n: attention.total }) }}</span>
        <Icon name="chevronRight" :size="14" />
      </button>
    </section>

    <!-- ---------- Continue Watching ---------- -->
    <section class="block" aria-labelledby="ov-plays">
      <div class="title">
        <h2 id="ov-plays">{{ t("继续播放") }}</h2>
        <button class="link" @click="openHistory()">{{ t("播放历史") }} <Icon name="chevronRight" :size="14" /></button>
      </div>

      <template v-if="drive.playsLoaded">
        <div v-for="play in drive.plays.slice(0, 2)" :key="play.fileId" class="play" :class="{ gone: !play.available }">
          <button
            class="play-main"
            :disabled="!play.available"
            :title="play.available ? t('从 {time} 继续播放「{name}」', { time: clock(play.position), name: play.name }) : undefined"
            @click="resumePlay(play)"
          >
            <span class="thumb">
              <Thumb :src="play.thumbnail" :alt="play.name">
                <Icon name="video" :size="20" />
              </Thumb>
              <Icon v-if="play.available && play.thumbnail" class="glyph" name="play" :size="16" filled />
            </span>
            <span class="play-text">
              <strong :title="play.name">{{ play.name }}</strong>
              <small v-if="!play.available" :title="t('文件已删除或无权访问')">{{ t("记录不可用") }}</small>
              <small v-else-if="hasProgress(play)" class="tnum">{{ clock(play.position) }} / {{ clock(play.duration) }}</small>
              <small v-else>{{ t("最近打开 · {when}", { when: ago(play.playedAt) }) }}</small>
              <span v-if="play.available && hasProgress(play)" class="bar thin" aria-hidden="true">
                <i :style="{ width: `${progressOf(play)}%` }" />
              </span>
            </span>
          </button>
          <button v-if="!play.available" class="link remove danger-action" @click="forgetPlay(play)">{{ t("移除") }}</button>
        </div>
        <p v-if="!drive.plays.length" class="empty">{{ t("播放后可在这里继续观看") }}</p>
      </template>
      <div v-else class="play" aria-busy="true" :aria-label="t('正在读取播放记录')">
        <span class="thumb skel" />
        <span class="play-text">
          <span class="skel line" style="width: 70%" />
          <span class="skel line" style="width: 40%" />
        </span>
      </div>
    </section>
  </div>
</template>

<style scoped>
/* The chart's colours: named, so the legend and the arcs agree,
   and set per theme — the dark set loses its contrast on the light sheet. */
.overview {
  --chart-video: #275fad;
  --chart-image: #8256b8;
  --chart-document: #27725e;
  --chart-other: #95661c;
  --chart-remaining: rgba(83, 116, 148, 0.14);
  display: flex;
  flex-direction: column;
}

:root[data-theme="dark"] .overview {
  --chart-video: #83c7ff;
  --chart-image: #bca5f3;
  --chart-document: #8cdcc4;
  --chart-other: #f3c878;
  --chart-remaining: rgba(182, 201, 217, 0.18);
}

/* The heading and what sits at its end (Updated / Watch History / All) share a
   baseline: centred, the 12px stamp sat 2px above the 15px heading's. */
.title {
  display: flex;
  align-items: baseline;
  justify-content: space-between;
  gap: 8px;
  min-height: 24px;
}

h2 {
  margin: 0;
  font-size: 15px;
  line-height: 22px;
  font-weight: 600;
}

.stamp {
  font-size: 12px;
  color: var(--fg-dim);
}

.membership {
  margin-top: 4px;
}

.dim {
  color: var(--fg-dim);
}

/* ---------- ring ---------- */

.ring {
  position: relative;
  width: 144px;
  height: 144px;
  margin: 18px auto 14px;
}

.ring svg {
  display: block;
  width: 144px;
  height: 144px;
}

.ring circle {
  fill: none;
  stroke-width: 10;
}

.track {
  stroke: var(--chart-remaining);
}

/* Flat ends: a round cap would spill over its neighbour and hide a thin arc. */
.arc {
  stroke-linecap: butt;
}

.arc.video {
  stroke: var(--chart-video);
}

.arc.image {
  stroke: var(--chart-image);
}

.arc.document {
  stroke: var(--chart-document);
}

.arc.other {
  stroke: var(--chart-other);
}

.arc.used {
  stroke: var(--accent);
}

.skel-ring {
  animation: breathe 1.4s ease-in-out infinite;
}

@keyframes breathe {
  50% {
    opacity: 0.45;
  }
}

.centre {
  position: absolute;
  inset: 0;
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
}

.centre small {
  font-size: 12px;
  color: var(--fg-dim);
}

.centre strong {
  font-size: 30px;
  line-height: 40px;
  font-weight: 650;
  letter-spacing: -0.03em;
}

.centre strong span {
  font-size: 14px;
  font-weight: 500;
  letter-spacing: 0;
}

.caption {
  display: flex;
  align-items: baseline;
  justify-content: space-between;
  gap: 8px;
  font-size: 13px;
}

.caption .dim,
.caption > .dim {
  font-size: 12px;
}

.full {
  font-size: 12px;
  font-weight: 600;
  color: var(--danger);
}

/* Auto tracks: each column as wide as its label and figure, so a long
   label (ドキュメント) takes room from a short neighbour before it wraps. */
.legend {
  display: grid;
  grid-template-columns: auto auto;
  gap: 8px 12px;
  margin: 14px 0 0;
  font-size: 12px;
}

.legend > div {
  display: flex;
  justify-content: space-between;
  gap: 6px;
}

.legend dt {
  display: flex;
  align-items: center;
  gap: 6px;
  color: var(--fg-dim);
}

.legend dd {
  margin: 0;
  white-space: nowrap;
}

.legend i {
  width: 6px;
  height: 6px;
  border-radius: 50%;
}

.legend i.video {
  background: var(--chart-video);
}

.legend i.image {
  background: var(--chart-image);
}

.legend i.document {
  background: var(--chart-document);
}

.legend i.other {
  background: var(--chart-other);
}

.legend i.used {
  background: var(--accent);
}

.legend i.free {
  background: var(--chart-remaining);
  box-shadow: inset 0 0 0 1px var(--line-strong);
}

.note {
  margin: 10px 0 0;
  font-size: 11px;
  line-height: 16px;
  color: var(--fg-dim);
}

.unavailable {
  margin: 14px 0 0;
  padding: 18px 14px;
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: 6px;
  text-align: center;
  text-wrap: balance;
  border-radius: 14px;
  background: color-mix(in srgb, var(--fg) 5%, transparent);
  font-size: 13px;
}

.unavailable p {
  margin: 0;
  font-weight: 600;
}

.unavailable small {
  font-size: 12px;
  color: var(--fg-dim);
  overflow-wrap: anywhere;
}

.stale {
  margin: 10px 0 0;
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 4px 8px;
  font-size: 12px;
  color: var(--warning);
}

.retry {
  min-height: 32px;
  padding: 0 12px;
  font-size: 12px;
}

/* ---------- blocks ---------- */

.block {
  margin-top: 24px;
  padding-top: 20px;
  border-top: 1px solid var(--line);
}

.badge {
  margin-left: 3px;
  padding: 1px 6px;
  border-radius: 8px;
  font-size: 11px;
  font-weight: 500;
  color: var(--fg-dim);
  background: color-mix(in srgb, var(--fg) 8%, transparent);
}

.link {
  min-height: 40px;
  padding: 0 10px;
  gap: 2px;
  border-color: transparent;
  background: transparent;
  color: var(--accent);
  font-size: 12px;
  font-weight: 500;
}

/* The roomier hover pill hangs past the column edge; the words stay where they were. */
.title .link {
  margin-right: -6px;
}

.link:hover:not(:disabled) {
  background: var(--accent-soft);
  border-color: transparent;
}

.task {
  margin-top: 12px;
}

.task-head {
  display: flex;
  align-items: center;
  gap: 10px;
}

.task-head > svg {
  flex: none;
  color: var(--accent);
}

.task-text {
  flex: 1;
  min-width: 0;
  display: flex;
  flex-direction: column;
}

.task-text strong,
.play-text strong {
  font-size: 13px;
  font-weight: 600;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.task-text small,
.play-text small {
  font-size: 12px;
  color: var(--fg-dim);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.pct {
  font-size: 12px;
}

.bar {
  display: block;
  height: 4px;
  margin-top: 10px;
  border-radius: 4px;
  overflow: hidden;
  background: color-mix(in srgb, var(--fg) 10%, transparent);
}

.bar i {
  display: block;
  height: 100%;
  border-radius: inherit;
  background: var(--progress);
}

.bar.thin {
  height: 3px;
  margin-top: 6px;
}

.task-meta {
  display: flex;
  justify-content: space-between;
  gap: 8px;
  margin-top: 8px;
  font-size: 11px;
  color: var(--fg-dim);
}

.task-meta > .tnum {
  flex: none;
  white-space: nowrap;
}

.task-meta .to {
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.empty {
  margin: 10px 0 0;
  display: flex;
  align-items: center;
  justify-content: space-between;
  flex-wrap: wrap;
  gap: 4px;
  font-size: 13px;
  color: var(--fg-dim);
}

.attention {
  width: 100%;
  min-height: 40px;
  margin-top: 12px;
  justify-content: flex-start;
  gap: 8px;
  border-radius: 12px;
  border-color: color-mix(in srgb, var(--danger) 26%, transparent);
  background: color-mix(in srgb, var(--danger) 8%, transparent);
  color: var(--danger);
  font-size: 13px;
  font-weight: 500;
}

.attention span {
  flex: 1;
  text-align: left;
}

/* ---------- plays ---------- */

.play {
  display: flex;
  align-items: center;
  gap: 6px;
  margin-top: 12px;
}

.play-main {
  flex: 1;
  min-width: 0;
  justify-content: flex-start;
  gap: 12px;
  padding: 0;
  border: 0;
  border-radius: 10px;
  background: transparent;
  text-align: left;
}

.play-main:hover:not(:disabled) {
  background: transparent;
}

.play-main:disabled {
  opacity: 1;
  cursor: default;
}

.thumb {
  position: relative;
  flex: none;
  width: 76px;
  height: 48px;
  display: grid;
  place-items: center;
  border-radius: 8px;
  overflow: hidden;
  background: color-mix(in srgb, var(--fg) 10%, transparent);
}

.thumb .glyph {
  position: relative;
  color: #ffffff;
  filter: drop-shadow(0 1px 3px rgba(0, 0, 0, 0.55));
}

.play-main:hover .thumb .glyph,
.play-main:focus-visible .thumb .glyph {
  transform: scale(1.12);
}

.play-text {
  flex: 1;
  min-width: 0;
  display: flex;
  flex-direction: column;
  gap: 2px;
}

.gone .thumb,
.gone .play-text strong {
  opacity: 0.55;
}

.play .line {
  display: block;
  height: 10px;
  border-radius: 5px;
}

.remove {
  flex: none;
}
</style>
