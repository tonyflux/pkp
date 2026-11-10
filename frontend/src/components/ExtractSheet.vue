<script setup lang="ts">
// Online Extraction: one modal layer for the whole flow. The archive is
// read first; the service's answer decides whether the contents or a
// password page come next (never both at once). The folder browser takes
// the same layer's place instead of stacking a second sheet; Esc leaves it
// first, then the sheet. Nothing here shows a percentage, a count or a
// result the service did not give.
import { computed, nextTick, onBeforeUnmount, onMounted, ref, watch } from "vue";
import Icon from "./Icon.vue";
import Orbit from "./Orbit.vue";
import FileViewSwitch from "./FileViewSwitch.vue";
import PickerItems from "./PickerItems.vue";
import { bytes } from "../format";
import { archiveType, itemMeta, selectionSummary } from "../extract";
import { t } from "../i18n";
import {
  browseExtractTo,
  closeExtract,
  closeExtractBrowser,
  confirmExtractBrowser,
  enterExtractResult,
  extract,
  openExtractBrowser,
  pickAllExtract,
  readArchive,
  retryExtract,
  revealExtracted,
  searchExtractFolders,
  setExtractHere,
  submitArchivePassword,
  submitExtract,
  toggleExtractPick,
  trailPath,
} from "../store";

const ICON: Record<string, string> = {
  folder: "folder",
  video: "film",
  image: "image",
  audio: "audio",
  document: "document",
  archive: "archive",
};

const browsing = computed(() => extract.browser.open);
const contentsLayout = ref<"grid" | "list">("grid");
const browserLayout = ref<"grid" | "list">("grid");
const picked = computed(() => new Set(extract.picked));
const allPicked = computed(() => extract.items.length > 0 && picked.value.size === extract.items.length);
const somePicked = computed(() => picked.value.size > 0 && !allPicked.value);
const summary = computed(() => selectionSummary(extract.items, picked.value));
const archiveName = computed(() => extract.entry?.name ?? "");
const archiveLine = computed(() => {
  const e = extract.entry;
  if (!e) return "";
  return e.size ? `${archiveType(e.name)} · ${bytes(e.size)}` : archiveType(e.name);
});
// The folder the extraction makes: the service names it after the archive.
const madeFolder = computed(() => extract.task.folder || archiveName.value.replace(/\.[^.]+$/, ""));
const madeWhat = computed(() => (extract.task.files ? t("{n} 项", { n: extract.task.files }) : t("所选内容")));

// The status sentences keep what and where in bold, wherever a language puts
// them: the translation is filled with markers, then cut at them.
const MARK = "\u0000";
function boldParts(sentence: string, values: Record<string, string>): { text: string; bold: boolean }[] {
  return sentence.split(MARK).map((piece, i) => (i % 2 ? { text: values[piece] ?? "", bold: true } : { text: piece, bold: false }));
}
const marked = { what: `${MARK}what${MARK}`, place: `${MARK}place${MARK}` };
const runningLead = computed(() =>
  boldParts(t("{what}将保存到 {place} 下的新文件夹「{folder}」。现在可以继续浏览文件，完成后会在页面底部提示。", { ...marked, folder: madeFolder.value }), {
    what: madeWhat.value,
    place: extract.task.place,
  }),
);
const doneLead = computed(() =>
  boldParts(t("{what}已保存到 {place}，原压缩包仍在原处。", marked), { what: madeWhat.value, place: extract.task.place || t("云盘") }),
);

const heading = computed(() => {
  switch (extract.stage) {
    case "running":
      return { kicker: t("进行中"), title: t("解压进行中"), sub: t("云端处理 · 原文件保持不变") };
    case "done":
      return { kicker: t("已完成"), title: t("解压完成"), sub: t("文件已保存到云盘") };
    case "taskFailed":
      return { kicker: t("未完成"), title: t("解压没有完成"), sub: t("原压缩包保持不变") };
    default:
      return { kicker: t("云端操作"), title: t("在线解压"), sub: t("直接解压到云盘，不占用电脑的本地空间") };
  }
});

const herePath = computed(() => (extract.here ? trailPath(extract.here) : t("正在读取位置…")));
const chosenPath = computed(() => (extract.chosen ? trailPath(extract.chosen) : t("浏览云盘中的文件夹")));

/* ---------- password ---------- */

const secret = ref("");
const reveal = ref(false);
const secretField = ref<HTMLInputElement>();

watch(
  () => extract.stage,
  async (stage, was) => {
    // A failed try clears the field: the next one starts from nothing.
    if (stage === "password") {
      if (was !== "password") secret.value = "";
      await nextTick();
      secretField.value?.focus();
    }
  },
);

watch(
  () => extract.open,
  (open) => {
    if (!open) {
      secret.value = "";
      reveal.value = false;
    }
  },
);

/* ---------- the folder browser ---------- */

const backButton = ref<HTMLButtonElement>();
const otherButton = ref<HTMLButtonElement>();
const searching = computed(() => !!extract.browser.query.trim());
const browserPath = computed(() => trailPath(extract.browser.trail));

watch(browsing, async (open) => {
  await nextTick();
  if (open) backButton.value?.focus({ preventScroll: true });
  else if (extract.open && extract.stage === "select") otherButton.value?.focus({ preventScroll: true });
});

function subfolders(n: number): string {
  if (n < 0) return "";
  return n ? t("{n} 个子文件夹", { n }) : t("没有子文件夹");
}

/* ---------- keys ---------- */

// The sheet owns Esc while it is up: the browser first, then the sheet.
function onKeydown(ev: KeyboardEvent) {
  if (!extract.open || ev.key !== "Escape") return;
  ev.stopImmediatePropagation();
  ev.preventDefault();
  dismiss();
}

function dismiss() {
  if (browsing.value) closeExtractBrowser();
  else closeExtract();
}

onMounted(() => window.addEventListener("keydown", onKeydown, true));
onBeforeUnmount(() => window.removeEventListener("keydown", onKeydown, true));
</script>

<template>
  <Teleport to="body">
    <div v-if="extract.open" class="scrim" @click.self="dismiss">
      <!-- Keys stay in the sheet: Enter on a focused button must not also
           open whatever the page behind has selected. -->
      <section
        class="sheet glass glass-sheet extract"
        :class="[`at-${extract.stage}`, { browsing }]"
        role="dialog"
        aria-modal="true"
        aria-labelledby="extract-title"
        @keydown.stop
      >
        <!-- ---------- folder browser, in place of the sheet's content ---------- -->
        <template v-if="browsing">
          <header class="browser-top">
            <button ref="backButton" class="icon back" :title="t('返回解压设置')" :aria-label="t('返回解压设置')" @click="closeExtractBrowser">
              <Icon name="arrowLeft" :size="17" />
            </button>
            <div class="titles">
              <h2 id="extract-title">{{ t("选择保存位置") }}</h2>
              <p>{{ t("浏览当前账号的整个云盘") }}</p>
            </div>
            <FileViewSwitch v-model="browserLayout" :disabled="extract.submitting" />
          </header>

          <label class="search">
            <Icon name="search" :size="17" />
            <input
              type="search"
              :value="extract.browser.query"
              :placeholder="t('搜索文件夹')"
              :aria-label="t('搜索全盘文件夹')"
              autocomplete="off"
              spellcheck="false"
              @input="searchExtractFolders(($event.target as HTMLInputElement).value)"
            />
          </label>

          <nav v-if="!searching" class="crumbs" :aria-label="t('当前云盘路径')">
            <button class="quiet" :class="{ here: !extract.browser.trail.length }" @click="browseExtractTo([])">{{ t("全部文件") }}</button>
            <template v-for="(crumb, i) in extract.browser.trail" :key="crumb.id">
              <Icon name="chevronRight" :size="13" />
              <button
                class="quiet"
                :class="{ here: i === extract.browser.trail.length - 1 }"
                :title="crumb.name"
                @click="browseExtractTo(extract.browser.trail.slice(0, i + 1))"
              >
                {{ crumb.name }}
              </button>
            </template>
          </nav>
          <p v-else class="crumbs found">{{ t("搜索整个云盘的文件夹") }}</p>

          <PickerItems :layout="browserLayout" class="folders glass-well" role="list" :aria-busy="extract.browser.loading || extract.browser.searching">
            <p v-if="extract.browser.error" class="state picker-state warn">
              {{ t("没有读到文件夹：{error}", { error: extract.browser.error }) }}
              <button class="small" @click="searching ? searchExtractFolders(extract.browser.query) : browseExtractTo(extract.browser.trail)">{{ t("重试") }}</button>
            </p>
            <template v-else-if="searching">
              <p v-if="extract.browser.searching" class="state picker-state"><Orbit :size="18" /> {{ t("正在搜索…") }}</p>
              <p v-else-if="!extract.browser.searchable" class="state picker-state">{{ t("云盘索引还在建立，暂时不能搜索全盘；可以逐层浏览。") }}</p>
              <p v-else-if="!extract.browser.results.length" class="state picker-state">{{ t("没有找到文件夹，试试其他名称") }}</p>
              <button
                v-for="folder in extract.browser.results"
                v-else
                :key="folder.id"
                class="quiet picker-item"
                role="listitem"
                @click="enterExtractResult(folder)"
              >
                <span class="picker-thumb"><Icon name="folder" :size="19" filled /></span>
                <span class="picker-copy">
                  <strong class="picker-name" :title="folder.name">{{ folder.name }}</strong>
                  <small class="picker-meta" :title="trailPath(folder.trail ?? [])">{{ trailPath((folder.trail ?? []).slice(0, -1)) }}</small>
                </span>
                <Icon class="picker-chevron" name="chevronRight" :size="15" />
              </button>
            </template>
            <p v-else-if="extract.browser.loading" class="state picker-state"><Orbit :size="18" /> {{ t("正在加载文件夹…") }}</p>
            <p v-else-if="!extract.browser.folders.length" class="state picker-state">{{ t("这里没有子文件夹；可以选择当前位置") }}</p>
            <template v-else>
              <button
                v-for="folder in extract.browser.folders"
                :key="folder.id"
                class="quiet picker-item"
                role="listitem"
                @click="browseExtractTo([...extract.browser.trail, { id: folder.id, name: folder.name }])"
              >
                <span class="picker-thumb"><Icon name="folder" :size="19" filled /></span>
                <span class="picker-copy">
                  <strong class="picker-name" :title="folder.name">{{ folder.name }}</strong>
                  <small class="picker-meta">{{ subfolders(folder.subfolders) }}</small>
                </span>
                <Icon class="picker-chevron" name="chevronRight" :size="15" />
              </button>
            </template>
          </PickerItems>

          <footer class="browser-foot">
            <p class="location">
              <span>{{ t("保存到") }}</span><strong :title="browserPath">{{ browserPath }}</strong>
            </p>
            <div class="buttons">
              <button @click="closeExtractBrowser">{{ t("取消") }}</button>
              <button class="primary" :disabled="extract.browser.loading" @click="confirmExtractBrowser">{{ t("选择此文件夹") }}</button>
            </div>
          </footer>
        </template>

        <template v-else>
          <header class="top">
            <span class="mark" :class="{ done: extract.stage === 'done' }" aria-hidden="true">
              <Icon :name="extract.stage === 'done' ? 'check' : 'archive'" :size="28" />
            </span>
            <div class="titles">
              <span class="kicker">CLOUD EXTRACTION / {{ heading.kicker }}</span>
              <h2 id="extract-title">{{ heading.title }}</h2>
              <p>{{ heading.sub }}</p>
            </div>
            <button class="icon close" :title="t('关闭')" :aria-label="t('关闭在线解压')" @click="closeExtract">
              <Icon name="close" :size="16" />
            </button>
          </header>

          <!-- ---------- reading / password / could not read ---------- -->
          <div v-if="extract.stage === 'reading'" class="gate" role="status" aria-live="polite">
            <span class="emblem"><Icon name="archive" :size="34" /></span>
            <span class="gate-kicker">{{ t("正在读取压缩包") }}</span>
            <h3>{{ t("正在获取文件列表…") }}</h3>
            <p>{{ t("先尝试读取内容；只有云端要求密码时才会请你输入。") }}</p>
            <Orbit :size="20" />
          </div>

          <form v-else-if="extract.stage === 'password'" class="gate" @submit.prevent="submitArchivePassword(secret)">
            <span class="emblem"><Icon name="lock" :size="32" /></span>
            <span class="gate-kicker">{{ t("需要密码") }}</span>
            <h3>{{ t("这个压缩包需要密码") }}</h3>
            <p id="extract-password-hint">{{ t("输入密码后重新读取文件列表；读取成功才能选择要解压的内容。") }}</p>
            <div class="password">
              <label for="extract-password">{{ t("压缩包密码") }}</label>
              <div class="secret" :class="{ wrong: extract.passwordError }">
                <input
                  id="extract-password"
                  ref="secretField"
                  v-model="secret"
                  :type="reveal ? 'text' : 'password'"
                  :placeholder="t('输入解压密码')"
                  autocomplete="off"
                  spellcheck="false"
                  aria-describedby="extract-password-hint"
                  :aria-invalid="!!extract.passwordError"
                  @input="extract.passwordError = ''"
                />
                <button
                  type="button"
                  class="quiet"
                  :aria-pressed="reveal"
                  :aria-label="reveal ? t('隐藏密码') : t('显示密码')"
                  @click="reveal = !reveal"
                >
                  {{ reveal ? t("隐藏") : t("显示") }}
                </button>
              </div>
              <p v-if="extract.passwordError" class="error" role="alert">{{ extract.passwordError }}</p>
              <button type="submit" class="primary wide">{{ t("用密码重试读取") }}</button>
            </div>
            <small class="fine">{{ t("密码只用于这次读取和解压，不会保存。") }}</small>
          </form>

          <div v-else-if="extract.stage === 'failed' && extract.failure" class="gate" role="alert">
            <span class="emblem warn"><Icon name="alert" :size="32" /></span>
            <span class="gate-kicker warn">{{ t("读取失败") }}</span>
            <h3>{{ extract.failure.title }}</h3>
            <p>{{ extract.failure.detail }}</p>
            <div class="gate-buttons">
              <button @click="closeExtract">{{ t("关闭") }}</button>
              <button v-if="extract.failure.retry" class="primary" @click="readArchive(extract.password)">{{ t("重试") }}</button>
            </div>
          </div>

          <!-- ---------- choosing ---------- -->
          <template v-else-if="extract.stage === 'select'">
            <div class="body">
              <div class="source">
                <span class="source-mark"><Icon name="archive" :size="21" /></span>
                <div class="source-words">
                  <strong :title="archiveName">{{ archiveName }}</strong>
                  <small>{{ archiveLine }}</small>
                </div>
                <span class="tag">{{ t("源文件") }}</span>
              </div>

              <div class="section-head">
                <div>
                  <strong>{{ t("选择要解压的内容") }}</strong>
                  <small aria-live="polite">{{ summary }}</small>
                </div>
                <div class="contents-tools">
                  <label class="all">
                    <input
                      type="checkbox"
                      :checked="allPicked"
                      :indeterminate.prop="somePicked"
                      :disabled="extract.submitting"
                      @change="pickAllExtract(($event.target as HTMLInputElement).checked)"
                    />
                    {{ t("全选") }}
                  </label>
                  <FileViewSwitch v-model="contentsLayout" :disabled="extract.submitting" />
                </div>
              </div>
              <PickerItems :layout="contentsLayout" class="items glass-well" role="group" :aria-label="t('压缩包里的内容')">
                <label v-for="item in extract.items" :key="item.path" class="picker-item" :class="{ 'is-picked': picked.has(item.path) }" :title="item.path">
                  <span class="picker-thumb"><Icon :name="ICON[item.category] ?? 'file'" :size="19" :filled="item.folder" /></span>
                  <span class="picker-copy">
                    <strong class="picker-name" :title="item.name">{{ item.name }}</strong>
                    <small class="picker-meta">
                      {{ itemMeta(item) }}
                      <span v-if="!item.folder && item.size" class="item-size">· {{ bytes(item.size) }}</span>
                    </small>
                  </span>
                  <input
                    class="picker-check"
                    type="checkbox"
                    :checked="picked.has(item.path)"
                    :disabled="extract.submitting"
                    @change="toggleExtractPick(item.path)"
                  />
                </label>
              </PickerItems>

              <div class="section-head place">
                <strong>{{ t("保存位置") }}</strong>
                <small>{{ t("原压缩包保留") }}</small>
              </div>
              <div class="destinations glass-well" role="radiogroup" :aria-label="t('保存位置')">
                <button
                  class="quiet dest"
                  role="radio"
                  :aria-checked="extract.dest === 'here'"
                  :disabled="extract.submitting"
                  @click="setExtractHere"
                >
                  <span class="radio" aria-hidden="true" />
                  <Icon name="folder" :size="20" />
                  <span class="dest-words">
                    <strong>{{ t("当前文件夹") }}</strong>
                    <small :title="herePath">{{ herePath }}</small>
                  </span>
                </button>
                <button
                  ref="otherButton"
                  class="quiet dest"
                  role="radio"
                  :aria-checked="extract.dest === 'other'"
                  :disabled="extract.submitting"
                  @click="openExtractBrowser"
                >
                  <span class="radio" aria-hidden="true" />
                  <Icon name="folder" :size="20" />
                  <span class="dest-words">
                    <strong>{{ t("选择其他文件夹") }}</strong>
                    <small :title="chosenPath">{{ chosenPath }}</small>
                  </span>
                  <Icon class="chevron" name="chevronRight" :size="16" />
                </button>
              </div>
              <p v-if="extract.note" class="note" role="alert">{{ extract.note }}</p>
            </div>

            <footer class="foot">
              <span class="fine">{{ t("仅在云盘内处理") }}</span>
              <div class="buttons">
                <button :disabled="extract.submitting" @click="closeExtract">{{ t("取消") }}</button>
                <button class="primary go" :class="{ working: extract.submitting }" :disabled="extract.submitting" @click="submitExtract">
                  <Orbit v-if="extract.submitting" :size="15" />
                  {{ extract.submitting ? t("正在提交…") : t("解压所选") }}
                  <Icon v-if="!extract.submitting" name="arrowRight" :size="16" />
                </button>
              </div>
            </footer>
          </template>

          <!-- ---------- the cloud task ---------- -->
          <template v-else>
            <div class="body status">
              <div class="visual" :class="extract.stage" aria-hidden="true">
                <span class="ring">
                  <Icon :name="extract.stage === 'done' ? 'check' : extract.stage === 'taskFailed' ? 'alert' : 'archive'" :size="44" />
                </span>
              </div>

              <template v-if="extract.stage === 'running'">
                <p class="word"><i class="live" />{{ t("云端任务进行中") }}</p>
                <h3>{{ t("正在解压所选内容") }}</h3>
                <p v-if="extract.task.place" class="lead">
                  <template v-for="(part, i) in runningLead" :key="i"><strong v-if="part.bold">{{ part.text }}</strong><template v-else>{{ part.text }}</template></template>
                </p>
                <p v-else class="lead">{{ t("这个压缩包已有解压任务在云端进行。现在可以继续浏览文件，完成后会在页面底部提示。") }}</p>
                <div class="progress-head">
                  <span>{{ t("正在处理") }}</span>
                  <span>{{
                    extract.task.stale
                      ? t("暂时读不到云端进度，正在重试")
                      : extract.task.progress > 0
                        ? `${extract.task.progress}%`
                        : t("进度以云端任务实际返回为准")
                  }}</span>
                </div>
                <div
                  class="bar"
                  :class="{ unknown: extract.task.progress <= 0 }"
                  role="progressbar"
                  :aria-label="t('解压进度')"
                  :aria-valuenow="extract.task.progress > 0 ? extract.task.progress : undefined"
                  aria-valuemin="0"
                  aria-valuemax="100"
                >
                  <span :style="extract.task.progress > 0 ? { width: `${extract.task.progress}%` } : undefined" />
                </div>
              </template>

              <template v-else-if="extract.stage === 'done'">
                <p class="word ok"><Icon name="check" :size="14" />{{ t("云端任务已完成") }}</p>
                <h3>{{ t("所选内容已解压") }}</h3>
                <p class="lead">
                  <template v-for="(part, i) in doneLead" :key="i"><strong v-if="part.bold">{{ part.text }}</strong><template v-else>{{ part.text }}</template></template>
                </p>
                <button v-if="extract.task.fileId" class="quiet result" @click="revealExtracted">
                  <Icon name="folder" :size="18" />
                  <span class="name" :title="madeFolder">{{ madeFolder }}</span>
                  <span class="go-to">{{ t("查看保存位置") }}<Icon name="chevronRight" :size="14" /></span>
                </button>
              </template>

              <template v-else>
                <p class="word bad"><Icon name="alert" :size="14" />{{ t("云端任务没有完成") }}</p>
                <h3>{{ t("这次没有解压出文件") }}</h3>
                <p class="lead">{{ extract.task.error }}</p>
              </template>
            </div>

            <footer class="foot">
              <span class="fine">{{ extract.stage === "running" ? t("原压缩包不会删除") : t("原压缩包保留") }}</span>
              <div class="buttons">
                <button v-if="extract.stage === 'running'" class="primary" @click="closeExtract">{{ t("返回文件") }}</button>
                <button v-else-if="extract.stage === 'done'" class="primary" @click="closeExtract">{{ t("完成") }}</button>
                <template v-else>
                  <button @click="closeExtract">{{ t("关闭") }}</button>
                  <button class="primary" @click="retryExtract">{{ t("重新选择") }}</button>
                </template>
              </div>
            </footer>
          </template>
        </template>
      </section>
    </div>
  </Teleport>
</template>

<style scoped>
.sheet.extract {
  position: relative;
  width: min(620px, calc(100vw - 48px));
  max-height: calc(100vh - 48px);
  padding: 26px 30px 24px;
  overflow: hidden;
}

.sheet.browsing {
  height: min(690px, calc(100vh - 48px));
}

/* ---------- heading ---------- */

.top {
  display: flex;
  align-items: center;
  gap: 16px;
  flex: none;
  margin-bottom: 20px;
}

.mark,
.source-mark,
.emblem {
  display: grid;
  place-items: center;
  flex: none;
  color: var(--accent);
  background: var(--accent-soft);
  border: 1px solid color-mix(in srgb, var(--accent) 32%, transparent);
}

.mark {
  width: 58px;
  height: 58px;
  border-radius: 18px;
}

.mark.done {
  color: var(--success);
  background: color-mix(in srgb, var(--success) 14%, transparent);
  border-color: color-mix(in srgb, var(--success) 34%, transparent);
}

.titles {
  flex: 1;
  min-width: 0;
}

.kicker,
.gate-kicker {
  display: block;
  font-size: 11px;
  font-weight: 800;
  letter-spacing: 0.14em;
  color: var(--accent);
}

.titles h2 {
  margin: 2px 0 2px;
  font-size: 26px;
  font-weight: 600;
  line-height: 1.25;
}

.titles p {
  margin: 0;
  font-size: 13px;
  color: var(--fg-dim);
  text-wrap: balance;
}

.close,
.back {
  flex: none;
  width: 38px;
  height: 38px;
  min-height: 38px;
  align-self: flex-start;
}

.close {
  border-radius: 50%;
}

.back {
  border-radius: 12px;
  align-self: center;
}

/* ---------- reading / password / failure ---------- */

.gate {
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: 0;
  min-height: 290px;
  padding: 14px 14px 22px;
  text-align: center;
  overflow-y: auto;
}

.emblem {
  width: 72px;
  height: 72px;
  border-radius: 22px;
  margin-bottom: 16px;
}

.emblem.warn {
  color: var(--warning);
  background: color-mix(in srgb, var(--warning) 14%, transparent);
  border-color: color-mix(in srgb, var(--warning) 32%, transparent);
}

.gate-kicker.warn {
  color: var(--warning);
}

.gate h3 {
  margin: 6px 0 4px;
  font-size: 21px;
  font-weight: 600;
  text-wrap: balance;
}

.gate > p {
  max-width: 420px;
  margin: 0 0 16px;
  font-size: 13px;
  line-height: 1.6;
  color: var(--fg-dim);
  text-wrap: balance;
}

.password {
  width: min(400px, 100%);
  text-align: left;
}

.password label {
  font-size: 13px;
  font-weight: 700;
}

.secret {
  display: flex;
  align-items: center;
  margin-top: 7px;
  border: 1px solid var(--line-strong);
  border-radius: 12px;
  background: var(--canvas);
  overflow: hidden;
  transition: border-color var(--dur) var(--ease);
}

.secret:focus-within {
  border-color: var(--accent);
}

.secret.wrong {
  border-color: var(--danger);
}

.secret input {
  flex: 1;
  min-width: 0;
  padding: 11px 14px;
  border: 0;
  border-radius: 0;
  background: transparent;
  font-size: 15px;
}

.secret button {
  flex: none;
  min-height: 0;
  padding: 0 14px;
  color: var(--accent);
  font-size: 13px;
}

.error,
.note {
  margin: 7px 0 0;
  font-size: 12px;
  color: var(--danger);
}

.primary.wide {
  width: 100%;
  min-height: 46px;
  margin-top: 14px;
  font-size: 15px;
  font-weight: 600;
}

.fine {
  font-size: 12px;
  color: var(--fg-dim);
}

.gate > .fine {
  margin-top: 14px;
}

.gate-buttons {
  display: flex;
  gap: 10px;
}

.gate-buttons button {
  min-width: 112px;
  min-height: 42px;
  font-weight: 600;
}

/* ---------- choosing ---------- */

.body {
  flex: 1;
  min-height: 0;
  overflow-y: auto;
  overscroll-behavior: contain;
  scrollbar-width: thin;
  padding-right: 2px;
}

.source {
  display: flex;
  align-items: center;
  gap: 12px;
  padding: 13px 14px;
  border: 1px solid var(--line);
  border-radius: 17px;
  background: var(--glass-inset);
}

.source-mark {
  width: 42px;
  height: 42px;
  border-radius: 12px;
}

.source-words,
.dest-words {
  flex: 1;
  min-width: 0;
}

.source-words strong,
.source-words small,
.dest-words strong,
.dest-words small {
  display: block;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.source-words strong {
  font-size: 15px;
}

.source-words small,
.dest-words small {
  font-size: 12px;
  color: var(--fg-dim);
}

.tag {
  flex: none;
  padding: 4px 9px;
  border-radius: 8px;
  font-size: 11px;
  color: var(--fg-dim);
  background: color-mix(in srgb, var(--fg) 7%, transparent);
}

.section-head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  margin: 20px 1px 9px;
}

.contents-tools {
  display: flex;
  align-items: center;
  gap: 12px;
  flex: none;
}

.section-head strong {
  display: block;
  font-size: 15px;
}

.section-head small {
  display: block;
  font-size: 12px;
  color: var(--fg-dim);
}

.section-head.place {
  align-items: baseline;
}

.all {
  display: flex;
  align-items: center;
  gap: 7px;
  font-size: 13px;
  white-space: nowrap;
  cursor: pointer;
}

.all input {
  flex: none;
  width: 17px;
  height: 17px;
  margin: 0;
  padding: 0;
  accent-color: var(--accent);
  cursor: pointer;
}

.items {
  max-height: 232px;
  overflow-y: auto;
  border-radius: 15px;
  scrollbar-width: thin;
}

.item-size {
  font-variant-numeric: tabular-nums;
}

.destinations {
  border-radius: 17px;
  overflow: hidden;
}

.dest {
  display: flex;
  align-items: center;
  justify-content: flex-start;
  gap: 12px;
  width: 100%;
  min-height: 68px;
  padding: 10px 17px;
  border-radius: 0;
  color: var(--fg);
  text-align: left;
}

.dest + .dest {
  border-top: 1px solid var(--line);
}

.dest[aria-checked="true"] {
  background: var(--accent-soft);
}

.dest > svg {
  flex: none;
  color: var(--accent);
}

.dest .chevron {
  color: var(--fg-dim);
}

.dest-words strong {
  font-size: 14px;
}

.radio {
  display: grid;
  place-items: center;
  flex: none;
  width: 19px;
  height: 19px;
  border-radius: 50%;
  border: 1.5px solid var(--fg-dim);
}

.dest[aria-checked="true"] .radio {
  border-color: var(--accent);
}

.dest[aria-checked="true"] .radio::after {
  content: "";
  width: 9px;
  height: 9px;
  border-radius: 50%;
  background: var(--accent);
}

.note {
  margin-top: 10px;
  line-height: 1.5;
}

.foot,
.browser-foot {
  flex: none;
  border-top: 1px solid var(--line);
  margin-top: 16px;
  padding-top: 16px;
}

.foot {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
}

.foot .fine {
  min-width: 0;
  text-wrap: pretty;
}

.buttons {
  display: flex;
  flex: none;
  gap: 10px;
}

.buttons button {
  min-width: 96px;
  min-height: 44px;
  padding: 0 20px;
  font-size: 14px;
  font-weight: 700;
}

.primary.go {
  gap: 10px;
}

/* ---------- the cloud task ---------- */

.status {
  text-align: center;
}

.visual {
  display: grid;
  place-items: center;
  height: 152px;
  background: radial-gradient(ellipse 46% 50% at 50% 55%, color-mix(in srgb, var(--accent) 20%, transparent), transparent);
}

.ring {
  display: grid;
  place-items: center;
  width: 112px;
  height: 112px;
  border-radius: 34px;
  transform: rotate(-8deg);
  color: var(--accent);
  background: var(--accent-soft);
  border: 1px solid color-mix(in srgb, var(--accent) 34%, transparent);
  box-shadow:
    0 0 0 18px color-mix(in srgb, var(--accent) 5%, transparent),
    0 0 0 39px color-mix(in srgb, var(--accent) 3%, transparent);
}

.ring > svg {
  transform: rotate(8deg);
}

.visual.done {
  background: radial-gradient(ellipse 46% 50% at 50% 55%, color-mix(in srgb, var(--success) 20%, transparent), transparent);
}

.visual.done .ring {
  color: var(--success);
  background: color-mix(in srgb, var(--success) 14%, transparent);
  border-color: color-mix(in srgb, var(--success) 34%, transparent);
  box-shadow:
    0 0 0 18px color-mix(in srgb, var(--success) 5%, transparent),
    0 0 0 39px color-mix(in srgb, var(--success) 3%, transparent);
}

.visual.taskFailed {
  background: none;
}

.visual.taskFailed .ring {
  color: var(--warning);
  background: color-mix(in srgb, var(--warning) 14%, transparent);
  border-color: color-mix(in srgb, var(--warning) 34%, transparent);
  box-shadow: none;
}

.word {
  display: flex;
  align-items: center;
  justify-content: center;
  gap: 6px;
  margin: 4px 0 0;
  font-size: 12px;
  font-weight: 700;
  letter-spacing: 0.06em;
  color: var(--accent);
}

.word.ok {
  color: var(--success);
}

.word.bad {
  color: var(--warning);
}

.live {
  width: 8px;
  height: 8px;
  border-radius: 50%;
  background: var(--accent);
  box-shadow: 0 0 10px var(--accent);
}

.status h3 {
  margin: 8px 0 8px;
  font-size: 21px;
  font-weight: 600;
  text-wrap: balance;
}

.lead {
  max-width: 470px;
  margin: 0 auto;
  font-size: 13px;
  line-height: 1.7;
  color: var(--fg-dim);
  text-wrap: balance;
}

.lead strong {
  color: var(--fg);
}

.progress-head {
  display: flex;
  justify-content: space-between;
  gap: 12px;
  margin: 24px 0 10px;
  font-size: 12px;
}

.progress-head span:last-child {
  color: var(--fg-dim);
  font-variant-numeric: tabular-nums;
}

.bar {
  height: 7px;
  border-radius: 6px;
  background: color-mix(in srgb, var(--accent) 16%, transparent);
  overflow: hidden;
}

.bar > span {
  display: block;
  height: 100%;
  border-radius: 6px;
  background: var(--progress);
  transition: width 0.4s var(--ease);
}

/* No percentage from the service: a sweep, never a made-up width. */
.bar.unknown > span {
  width: 36%;
  background: linear-gradient(90deg, transparent, var(--accent), transparent);
  animation: sweep 1.8s ease-in-out infinite alternate;
}

@keyframes sweep {
  from {
    transform: translateX(-100%);
  }
  to {
    transform: translateX(280%);
  }
}

@media (prefers-reduced-motion: reduce) {
  .bar.unknown > span {
    animation: none;
    width: 100%;
    opacity: 0.5;
  }
}

.result {
  display: flex;
  align-items: center;
  gap: 10px;
  width: 100%;
  min-height: 52px;
  margin-top: 22px;
  padding: 0 14px;
  border: 1px solid var(--line-strong);
  border-radius: 14px;
  color: var(--fg);
  text-align: left;
}

.result > svg {
  flex: none;
  color: var(--accent);
}

.result .name {
  flex: 1;
  min-width: 0;
  font-size: 14px;
  font-weight: 600;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.go-to {
  display: inline-flex;
  align-items: center;
  gap: 2px;
  flex: none;
  font-size: 12px;
  color: var(--accent);
}

/* ---------- the folder browser ---------- */

.browser-top {
  display: flex;
  align-items: center;
  gap: 12px;
  flex: none;
  margin-bottom: 16px;
}

.browser-top h2 {
  font-size: 20px;
}

.search {
  display: flex;
  align-items: center;
  gap: 10px;
  flex: none;
  height: 44px;
  padding: 0 14px;
  border: 1px solid var(--line-strong);
  border-radius: 12px;
  background: var(--canvas);
  color: var(--fg-dim);
  transition: border-color var(--dur) var(--ease);
}

.search:focus-within {
  border-color: var(--accent);
}

.search input {
  flex: 1;
  min-width: 0;
  padding: 0;
  border: 0;
  border-radius: 0;
  background: transparent;
  font-size: 14px;
}

.crumbs {
  display: flex;
  align-items: center;
  gap: 2px;
  flex: none;
  min-height: 44px;
  margin: 0;
  overflow-x: auto;
  white-space: nowrap;
  color: var(--fg-dim);
  scrollbar-width: thin;
}

.crumbs.found {
  font-size: 13px;
  padding: 0 4px;
}

.crumbs button {
  flex: none;
  max-width: 180px;
  min-height: 30px;
  padding: 0 8px;
  font-size: 13px;
  overflow: hidden;
  text-overflow: ellipsis;
  display: block;
}

.crumbs button.here {
  color: var(--fg);
  font-weight: 600;
}

.folders {
  flex: 1;
  min-height: 0;
  overflow-y: auto;
  overscroll-behavior: contain;
  border-radius: 14px;
  scrollbar-width: thin;
}

.state {
  display: flex;
  align-items: center;
  justify-content: center;
  flex-wrap: wrap;
  gap: 10px;
  min-height: 100%;
  margin: 0;
  padding: 24px 16px;
  font-size: 13px;
  color: var(--fg-dim);
  text-align: center;
  text-wrap: balance;
}

.state.warn {
  color: var(--danger);
}


.location {
  display: flex;
  align-items: baseline;
  gap: 10px;
  min-width: 0;
  margin: 0;
  font-size: 13px;
}

.location span {
  flex: none;
  color: var(--fg-dim);
}

.location strong {
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.browser-foot .buttons {
  justify-content: flex-end;
  margin-top: 12px;
}

/* ---------- narrow and short windows (960×640) ---------- */

@media (max-height: 760px) {
  .sheet.extract {
    padding: 20px 24px 18px;
  }

  .top {
    margin-bottom: 14px;
  }

  .mark {
    width: 48px;
    height: 48px;
    border-radius: 15px;
  }

  .titles h2 {
    font-size: 22px;
  }

  .section-head {
    margin: 14px 1px 7px;
  }

  .items {
    max-height: 168px;
  }


  .dest {
    min-height: 56px;
    padding: 7px 15px;
  }

  .foot,
  .browser-foot {
    margin-top: 12px;
    padding-top: 12px;
  }

  .visual {
    height: 120px;
  }

  .ring {
    width: 84px;
    height: 84px;
    border-radius: 26px;
  }

  .gate {
    min-height: 0;
  }
}

@media (max-width: 640px) {
  .foot .fine,
  .section-head.place small,
  .tag {
    display: none;
  }

  .foot .buttons,
  .browser-foot .buttons {
    flex: 1;
  }

  .foot .buttons button,
  .browser-foot .buttons button {
    flex: 1;
  }
}
</style>
