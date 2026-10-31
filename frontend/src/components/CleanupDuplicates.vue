<script setup lang="ts">
// Cleanup · Duplicate files: what the whole
// range could give back, the filters, one card per group of identical
// content with its copies, and the dock that removes the ticked copies of
// the page on screen. Every group keeps at least one copy; a starred copy is
// never ticked.
import { computed, ref } from "vue";
import Icon from "./Icon.vue";
import Orbit from "./Orbit.vue";
import CleanupDock from "./CleanupDock.vue";
import CleanupScopePick from "./CleanupScopePick.vue";
import SelectMenu from "./SelectMenu.vue";
import { bytes, day, share, shownPath, sizeParts, stamp } from "../format";
import { t, num } from "../i18n";
import { useRowWindows } from "../windowing";
import {
  DUP_MIN_SIZES,
  DUP_SORTS,
  REMOVE_LIMIT,
  cleanup,
  drive,
  dupLock,
  dupPicked,
  dupPickedBytes,
  keepDupCopy,
  openCleanupConfirm,
  scanDuplicates,
  setCleanupPermanent,
  setDupMinSize,
  setDupSameFolder,
  setDupSort,
  toggleDupCopy,
  type DupGroupView,
} from "../store";
import type { DupCopy } from "../../bindings/github.com/tonyflux/pkp/internal/services";

const d = computed(() => cleanup.dup);
const page = computed(() => d.value.page);
const groups = computed(() => page.value?.groups ?? []);
const busy = computed(() => !!cleanup.running);

const wasted = computed(() => sizeParts(page.value?.waste ?? 0));
// The share of the space in use; the server's figure includes the trash.
const usedShare = computed(() => {
  const usage = drive.quota?.usage ?? 0;
  return usage > 0 && page.value?.waste ? share(page.value.waste / usage) : "";
});

const minLabel = computed(() => DUP_MIN_SIZES.find((c) => c.value === cleanup.minSize)?.label ?? "");
const sortLabel = computed(() => DUP_SORTS.find((s) => s.key === cleanup.sort)?.label ?? "");

const listTitle = computed(() => {
  if (cleanup.sameFolder) return t("同一文件夹内的重复");
  if (cleanup.sort === "copies") return t("先从副本最多的开始");
  if (cleanup.sort === "modified") return t("最近修改的重复");
  return t("先从占空间最多的开始");
});
const range = computed(() => {
  const p = page.value;
  if (!p || !p.groups.length) return "";
  return t("第 {from}–{to} 组 · 共 {total} 组", { from: p.offset + 1, to: p.offset + p.groups.length, total: p.total });
});
const synced = computed(() => (page.value?.syncedAt ? t("本地索引 · 最后同步 {time}", { time: stamp(page.value.syncedAt) }) : t("本地索引")));

// The first card of a scan opens on its own: the copies are what the defaults are about.
function isOpen(group: DupGroupView, index: number): boolean {
  return d.value.open[group.hash] ?? (index === 0 && page.value?.offset === 0);
}

function toggleOpen(group: DupGroupView, index: number) {
  d.value.open[group.hash] = !isOpen(group, index);
}

const scroller = ref<HTMLElement | null>(null);
const { windows } = useRowWindows<DupGroupView>({
  scroller,
  lists: () => [{ id: "groups", items: groups.value }],
  key: (g) => g.hash,
  shape: (g) => (d.value.open[g.hash] ? `open:${g.copies.length}` : "closed"),
});
const drawn = computed(() => windows.value.groups ?? { rows: [], style: {} });

function kept(group: DupGroupView): number {
  return group.copies.filter((c) => !d.value.picked[c.id]).length;
}

function groupLine(group: DupGroupView): string {
  const parts = [t("{n} 份", { n: group.count }), t("每份 {size}", { size: bytes(group.size) })];
  if (group.sameFolder) parts.push(t("同一文件夹"));
  parts.push(t("保留 {n} 份", { n: kept(group) }));
  return parts.join(" · ");
}

const thumbIcon: Record<string, string> = { video: "play", image: "image", audio: "audio", document: "document", archive: "archive" };

function onCheck(group: DupGroupView, copy: DupCopy, event: Event) {
  const input = event.target as HTMLInputElement;
  toggleDupCopy(group, copy.id, input.checked);
  // A refused tick (the last copy, a starred one) stays unticked.
  input.checked = !!d.value.picked[copy.id];
}

const count = computed(() => dupPicked.value.length);
const tooMany = computed(() => count.value > REMOVE_LIMIT);
const dockTitle = computed(() => t("将处理 {n} 项 · 预计可回收 {size}", { n: count.value, size: bytes(dupPickedBytes.value, "0 B") }));
const dockHint = computed(() => {
  if (tooMany.value) return t("一次最多处理 {n} 项，请缩小范围或取消部分勾选", { n: REMOVE_LIMIT });
  if (cleanup.permanent) return t("无法撤销；只对本页勾选的副本彻底删除，每组至少保留一份。");
  return t("至少保留每组一份。先移到回收站，空间尚不会释放。");
});

const methods = computed(() => [
  { value: "trash", label: t("移到回收站") },
  { value: "delete", label: t("彻底删除") },
]);

// A removal drops groups from the page and from Go's next scan alike, so the
// next page starts after what is left here, not a whole page further.
function turn(forward: boolean) {
  const p = page.value;
  if (!p) return;
  scroller.value?.scrollTo({ top: 0 });
  void scanDuplicates(forward ? p.offset + p.groups.length : Math.max(0, p.offset - 50));
}
</script>

<template>
  <div class="tab">
    <div ref="scroller" class="scroll">
      <div class="content">
        <section class="summary" :aria-label="t('可回收空间')">
          <div class="primary">
            <span class="kicker">{{ cleanup.scope.length ? t("当前账号 · 范围内可回收") : t("当前账号 · 全盘可回收") }}</span>
            <strong class="big tnum">{{ wasted.value || "0" }}<span>{{ wasted.unit || "B" }}</span></strong>
            <span class="caption">
              <template v-if="usedShare"><Icon name="arrowRight" :size="13" class="up" /> {{ t("约占已用空间的 {share}", { share: usedShare }) }} · </template>
              {{ t("按文件内容指纹识别") }}
            </span>
          </div>
          <div class="side">
            <div class="stat">
              <strong class="tnum">{{ num(page?.total ?? 0) }}</strong>
              <span>{{ t("组内容重复") }}</span>
            </div>
            <div class="stat">
              <strong class="tnum">{{ num(page?.copies ?? 0) }}</strong>
              <span>{{ t("份多余副本") }}</span>
            </div>
            <p>{{ t("同名与否不影响结果。每组默认留一份，可逐项修改。") }}</p>
          </div>
        </section>

        <div class="filters">
          <div class="filter">
            <span>{{ t("扫描范围") }}</span>
            <CleanupScopePick />
          </div>
          <div class="filter">
            <span>{{ t("最小文件") }}</span>
            <SelectMenu
              class="pick"
              :class="{ busy }"
              :model-value="cleanup.minSize"
              :options="DUP_MIN_SIZES"
              :label="t('最小文件')"
              :disabled="busy"
              @update:model-value="setDupMinSize"
            >
              <span>{{ minLabel }}</span>
              <Icon name="chevronDown" :size="15" />
            </SelectMenu>
          </div>
          <div class="filter">
            <span>{{ t("排序") }}</span>
            <SelectMenu
              class="pick"
              :class="{ busy }"
              :model-value="cleanup.sort"
              :options="DUP_SORTS.map((s) => ({ value: s.key, label: s.label }))"
              :label="t('排序')"
              :disabled="busy"
              @update:model-value="setDupSort"
            >
              <span>{{ sortLabel }}</span>
              <Icon name="chevronDown" :size="15" />
            </SelectMenu>
          </div>
          <span class="spacer" />
          <label class="same" :title="t('所有副本都在同一个文件夹里的组，最没有争议，适合先清理')">
            <input
              type="checkbox"
              :checked="cleanup.sameFolder"
              :disabled="busy"
              @change="setDupSameFolder(($event.target as HTMLInputElement).checked)"
            />
            <span>{{ t("只看同一文件夹") }}</span>
          </label>
        </div>

        <div class="intro">
          <div>
            <strong>{{ listTitle }}</strong>
            <span class="tnum">{{ range }}</span>
            <Orbit v-if="d.loading" :size="14" :label="t('读取中')" />
          </div>
          <span class="sync"><Icon name="clock" :size="14" />{{ synced }}</span>
        </div>

        <p v-if="d.error" class="error" role="alert">{{ t("没能读取：{error}", { error: d.error }) }}</p>

        <div v-if="!groups.length && !d.loading" class="none">
          <Icon name="check" :size="22" />
          <strong>{{ cleanup.sameFolder ? t("没有同一文件夹里的重复") : t("没有找到重复文件") }}</strong>
          <span>{{ t("只按内容判断：同一指纹、同样大小才算重复。") }}</span>
        </div>

        <div data-list="groups" class="groups" :style="drawn.style">
          <article
            v-for="{ item: group, index, style } in drawn.rows"
            :key="group.hash"
            class="group"
            :class="{ open: isOpen(group, index) }"
            :data-row="group.hash"
            :style="style"
          >
            <div class="head">
              <span class="thumb" :class="group.category">
                <img v-if="group.thumbnail" :src="group.thumbnail" alt="" loading="lazy" referrerpolicy="no-referrer" />
                <Icon v-else :name="thumbIcon[group.category] ?? 'file'" :size="20" />
              </span>
              <div class="name">
                <strong :title="group.name">{{ group.name }}</strong>
                <span class="tnum">{{ groupLine(group) }}</span>
              </div>
              <div class="waste">
                <small>{{ t("可回收") }}</small>
                <strong class="tnum">{{ bytes(group.waste) }}</strong>
              </div>
              <button
                class="quiet icon expand"
                :aria-expanded="isOpen(group, index)"
                :aria-label="isOpen(group, index) ? t('收起「{name}」的 {n} 份副本', { name: group.name, n: group.count }) : t('展开「{name}」的 {n} 份副本', { name: group.name, n: group.count })"
                @click="toggleOpen(group, index)"
              >
                <Icon name="chevronDown" :size="18" />
              </button>
            </div>

            <ul v-if="isOpen(group, index)" class="copies">
              <li v-for="copy in group.copies" :key="copy.id" class="copy" :class="{ going: d.picked[copy.id] }">
                <label class="check" :title="dupLock(group, copy.id)">
                  <input
                    type="checkbox"
                    :checked="!!d.picked[copy.id]"
                    :disabled="busy || (!d.picked[copy.id] && !!dupLock(group, copy.id))"
                    :aria-label="d.picked[copy.id] ? t('{name}：移走', { name: copy.name }) : t('{name}：保留', { name: copy.name })"
                    @change="onCheck(group, copy, $event)"
                  />
                  <span>{{ d.picked[copy.id] ? t("移走") : t("保留") }}</span>
                </label>
                <div class="info">
                  <strong :title="copy.name">{{ copy.name }}</strong>
                  <small :title="shownPath(copy.path)">{{ shownPath(copy.path) }}</small>
                </div>
                <span class="marks">
                  <span v-if="copy.starred" class="badge star"><Icon name="star" :size="11" filled />{{ t("星标") }}</span>
                  <span v-if="copy.played" class="badge">{{ t("有播放记录") }}</span>
                </span>
                <time class="tnum" :datetime="copy.modifiedTime">{{ day(copy.modifiedTime) }}</time>
                <button
                  class="keep"
                  :aria-pressed="d.keep[group.hash] === copy.id"
                  :aria-label="t('保留这份：{name}', { name: copy.name })"
                  :disabled="busy"
                  @click="keepDupCopy(group, copy.id)"
                >
                  <i aria-hidden="true" />{{ t("保留这份") }}
                </button>
              </li>
            </ul>
          </article>
        </div>

        <button v-if="page && page.small > 0 && cleanup.minSize > 0" class="small" :disabled="busy" @click="setDupMinSize(0)">
          <Icon name="chevronDown" :size="16" />
          <span>{{ t("另有 {n} 组小文件（合计 {size}）", { n: page.small, size: bytes(page.smallWaste, "0 B") }) }}</span>
          <b>{{ t("展开") }}</b>
        </button>

        <nav v-if="page && page.total > groups.length" class="paging" :aria-label="t('翻页')">
          <span class="tnum">{{ range }}</span>
          <div>
            <button :disabled="busy || d.loading || page.offset === 0" @click="turn(false)">{{ t("上一页") }}</button>
            <button :disabled="busy || d.loading || page.offset + groups.length >= page.total" @click="turn(true)">{{ t("下一页") }}</button>
          </div>
        </nav>
      </div>
    </div>

    <CleanupDock :title="dockTitle" :hint="dockHint" :warn="tooMany">
      <div class="method">
        <span>{{ t("处理方式") }}</span>
        <SelectMenu
          class="pick"
          :class="{ busy }"
          :model-value="cleanup.permanent ? 'delete' : 'trash'"
          :options="methods"
          :label="t('处理方式')"
          :disabled="busy"
          @update:model-value="setCleanupPermanent($event === 'delete')"
        >
          <span>{{ cleanup.permanent ? t("彻底删除") : t("移到回收站") }}</span>
          <Icon name="chevronDown" :size="15" />
        </SelectMenu>
      </div>
      <button
        class="primary go"
        :class="{ danger: cleanup.permanent }"
        :disabled="busy || !count || tooMany"
        @click="openCleanupConfirm('dup')"
      >
        {{ cleanup.permanent ? t("彻底删除") : t("移到回收站") }}
        <Icon name="arrowRight" :size="15" />
      </button>
    </CleanupDock>
  </div>
</template>

<style scoped>
.tab {
  position: relative;
  flex: 1;
  min-height: 0;
  display: flex;
}

.scroll {
  flex: 1;
  min-height: 0;
  overflow-y: auto;
  scrollbar-width: thin;
}

/* Room under the last card for the dock floating over it. */
.content {
  max-width: 1380px;
  margin: 0 auto;
  padding: 22px var(--pad) 120px;
}

/* ---------- Reclaimable across all disks ---------- */

.summary {
  display: grid;
  grid-template-columns: minmax(0, 1fr) minmax(0, auto);
  gap: 24px;
  padding: 24px 26px;
  border-radius: 20px;
  border: 1px solid var(--line-strong);
  background: linear-gradient(140deg, var(--accent-soft), transparent 70%), color-mix(in srgb, var(--fg) 3%, transparent);
}

.kicker {
  font-size: 12px;
  font-weight: 600;
  color: var(--fg-dim);
}

.big {
  display: block;
  margin: 6px 0 8px;
  font-size: 52px;
  line-height: 1;
  font-weight: 700;
  letter-spacing: -0.03em;
}

.big span {
  margin-left: 8px;
  font-size: 22px;
  font-weight: 600;
  letter-spacing: 0;
  color: var(--fg-dim);
}

.caption {
  display: inline-flex;
  align-items: center;
  gap: 4px;
  font-size: 13px;
  color: var(--fg-dim);
}

.caption .up {
  color: var(--success);
  transform: rotate(-45deg);
}

.side {
  display: grid;
  grid-template-columns: repeat(2, auto);
  column-gap: 40px;
  row-gap: 14px;
  align-content: start;
  max-width: 300px;
}

.stat {
  display: flex;
  flex-direction: column;
}

.stat strong {
  font-size: 26px;
  font-weight: 700;
  line-height: 1.15;
}

.stat span {
  font-size: 12px;
  color: var(--fg-dim);
}

.side p {
  grid-column: 1 / -1;
  margin: 0;
  font-size: 12px;
  color: var(--fg-dim);
}

/* ---------- filters ---------- */

.filters {
  display: flex;
  align-items: flex-end;
  flex-wrap: wrap;
  gap: 12px;
  margin: 22px 0 18px;
}

.filter {
  display: flex;
  flex-direction: column;
  gap: 6px;
  font-size: 12px;
  color: var(--fg-dim);
}

.spacer {
  flex: 1;
}

.pick {
  display: inline-flex;
  align-items: center;
  justify-content: space-between;
  gap: 10px;
  min-width: 120px;
  min-height: 38px;
  padding: 0 12px 0 14px;
  border-radius: 10px;
  border: 1px solid var(--line-strong);
  background: color-mix(in srgb, var(--glass-control) 55%, transparent);
  font-size: 13px;
  font-weight: 600;
  color: var(--fg);
  white-space: nowrap;
}

.pick.busy {
  opacity: 0.5;
}

.pick > svg {
  flex: none;
  opacity: 0.8;
}

.same {
  display: inline-flex;
  align-items: center;
  gap: 8px;
  min-height: 38px;
  font-size: 13px;
  cursor: pointer;
}

.same input,
.check input {
  width: 16px;
  height: 16px;
  margin: 0;
  padding: 0;
  accent-color: var(--accent);
}

/* ---------- list ---------- */

.intro {
  display: flex;
  align-items: baseline;
  justify-content: space-between;
  gap: 16px;
  margin-bottom: 12px;
}

.intro > div {
  display: flex;
  align-items: center;
  gap: 10px;
  min-width: 0;
}

.intro strong {
  font-size: 16px;
  font-weight: 650;
  white-space: nowrap;
}

.intro > div > span,
.sync {
  font-size: 12px;
  color: var(--fg-dim);
  white-space: nowrap;
}

.sync {
  display: inline-flex;
  align-items: center;
  gap: 6px;
}

.error {
  margin: 0 0 12px;
  font-size: 13px;
  color: var(--danger);
}

.none {
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: 6px;
  padding: 42px 0;
  color: var(--fg-dim);
  font-size: 13px;
  text-align: center;
  text-wrap: balance;
}

.none svg {
  color: var(--success);
}

.none strong {
  font-size: 16px;
  color: var(--fg);
}

.groups {
  position: relative;
  display: flex;
  flex-direction: column;
  gap: 10px;
}

.group {
  border-radius: 16px;
  border: 1px solid var(--line);
  background: color-mix(in srgb, var(--fg) 3%, transparent);
  overflow: hidden;
}

.group.open {
  border-color: var(--line-strong);
}

.head {
  display: grid;
  grid-template-columns: auto minmax(0, 1fr) auto auto;
  align-items: center;
  gap: 14px;
  padding: 12px 14px 12px 16px;
}

.thumb {
  display: grid;
  place-items: center;
  width: 72px;
  height: 46px;
  border-radius: 9px;
  overflow: hidden;
  background: linear-gradient(140deg, color-mix(in srgb, var(--accent) 30%, transparent), color-mix(in srgb, var(--fg) 6%, transparent));
  color: var(--fg);
}

.thumb img {
  width: 100%;
  height: 100%;
  object-fit: cover;
}

.name {
  display: flex;
  flex-direction: column;
  gap: 3px;
  min-width: 0;
}

.name strong {
  font-size: 14px;
  font-weight: 650;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.name span {
  font-size: 12px;
  color: var(--fg-dim);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.waste {
  display: flex;
  flex-direction: column;
  align-items: flex-end;
}

.waste small {
  font-size: 11px;
  color: var(--fg-dim);
}

.waste strong {
  font-size: 17px;
  font-weight: 700;
  color: var(--accent);
}

.expand svg {
  transition: transform var(--dur) var(--ease);
}

.open .expand svg {
  transform: rotate(180deg);
}

.copies {
  margin: 0;
  padding: 0 0 4px;
  list-style: none;
  border-top: 1px solid var(--line);
}

.copy {
  display: grid;
  grid-template-columns: 84px minmax(0, 1fr) auto auto auto;
  align-items: center;
  gap: 12px;
  padding: 9px 14px 9px 98px;
}

.copy + .copy {
  border-top: 1px solid color-mix(in srgb, var(--line) 60%, transparent);
}

.check {
  display: inline-flex;
  align-items: center;
  gap: 8px;
  font-size: 12px;
  color: var(--success);
  cursor: pointer;
}

.going .check {
  color: var(--fg-dim);
}

.info {
  display: flex;
  flex-direction: column;
  min-width: 0;
}

.info strong {
  font-size: 13px;
  font-weight: 600;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.info small {
  font-size: 11px;
  color: var(--fg-dim);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.badge {
  display: inline-flex;
  align-items: center;
  gap: 3px;
  padding: 2px 7px;
  border-radius: 6px;
  background: color-mix(in srgb, var(--accent) 14%, transparent);
  font-size: 11px;
  font-weight: 600;
  color: var(--accent);
  white-space: nowrap;
}

.badge.star {
  background: color-mix(in srgb, var(--warning) 16%, transparent);
  color: var(--warning);
}

.marks {
  display: flex;
  gap: 6px;
}

.copy time {
  font-size: 12px;
  color: var(--fg-dim);
  white-space: nowrap;
}

.keep {
  gap: 6px;
  min-height: 28px;
  padding: 0 10px;
  border-radius: 8px;
  font-size: 12px;
  color: var(--fg-dim);
}

.keep i {
  width: 8px;
  height: 8px;
  border-radius: 50%;
  border: 1.5px solid currentColor;
}

.keep[aria-pressed="true"] {
  border-color: color-mix(in srgb, var(--success) 45%, transparent);
  background: color-mix(in srgb, var(--success) 12%, transparent);
  color: var(--success);
}

.keep[aria-pressed="true"] i {
  background: currentColor;
}

.small {
  width: 100%;
  justify-content: flex-start;
  gap: 10px;
  margin-top: 10px;
  min-height: 46px;
  border-radius: 14px;
  border-style: dashed;
  background: transparent;
  font-size: 13px;
  color: var(--fg-dim);
}

.small span {
  flex: 1;
  text-align: left;
}

.small b {
  font-weight: 600;
  color: var(--accent);
}

.paging {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  margin-top: 14px;
  font-size: 12px;
  color: var(--fg-dim);
}

.paging div {
  display: flex;
  gap: 8px;
}

.paging button {
  min-height: 32px;
  font-size: 13px;
}

/* ---------- dock ---------- */

.method {
  display: inline-flex;
  align-items: center;
  gap: 10px;
  font-size: 12px;
  color: var(--fg-dim);
}

.go {
  gap: 6px;
}

.go svg {
  transform: rotate(-45deg);
}

@container (max-width: 1100px) {
  .summary {
    grid-template-columns: minmax(0, 1fr);
  }

  .side {
    max-width: none;
    grid-template-columns: repeat(2, auto) minmax(0, 1fr);
  }

  .side p {
    grid-column: auto;
    align-self: center;
  }
}

@container (max-width: 900px) {
  .copy {
    padding-left: 16px;
    grid-template-columns: 76px minmax(0, 1fr) auto auto;
  }

  .copy time,
  .copy .badge:not(.star) {
    display: none;
  }

  .method > span:first-child {
    display: none;
  }
}

@container (max-width: 650px) {
  .big {
    font-size: 40px;
  }

  .side {
    grid-template-columns: repeat(2, auto);
  }

  .side p {
    grid-column: 1 / -1;
  }

  .waste {
    display: none;
  }

  .head {
    grid-template-columns: auto minmax(0, 1fr) auto;
  }

  .thumb {
    width: 52px;
    height: 36px;
  }

  .method {
    display: none;
  }
}
</style>
