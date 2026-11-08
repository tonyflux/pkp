<script setup lang="ts">
import { computed, ref, watch } from "vue";
import Icon from "./Icon.vue";
import Orbit from "./Orbit.vue";
import FileViewSwitch from "./FileViewSwitch.vue";
import PickerItems from "./PickerItems.vue";
import {
  accountLabel,
  accounts,
  addOffline,
  addTorrent,
  clearTorrent,
  closeRelocate,
  cloudAdd,
  currentAccount,
  currentFolderId,
  drive,
  externalOpen,
  pickCloudAddFolder,
  openPikPakShare,
  pickTorrent,
  relocate,
  ui,
} from "../store";
import { bytes } from "../format";
import type { OfflineResult, TorrentFile } from "../../bindings/github.com/tonyflux/pkp/internal/services";
import { t } from "../i18n";
import { shareLink } from "../sharelink";

// Draft text survives closing and reopening the sheet: losing a pasted batch
// to a stray click is the most annoying failure this panel can have. It lives
// in the store so switching accounts can ask before discarding it.
const draft = computed({
  get: () => cloudAdd.draft,
  set: (text: string) => {
    cloudAdd.draft = text;
  },
});
const results = ref<OfflineResult[]>([]);
const submitting = computed(() => cloudAdd.submitting);

interface Line {
  index: number;
  text: string;
  valid: boolean;
  reason: string;
}

const lines = computed<Line[]>(() => {
  const seen = new Set<string>();
  return draft.value
    .split("\n")
    .map((raw) => raw.trim())
    .filter((text) => text.length > 0)
    .map((text, i) => {
      if (seen.has(text)) return { index: i + 1, text, valid: false, reason: t("重复") };
      seen.add(text);
      const ok =
        !!shareLink(text) ||
        text.startsWith("magnet:?") ||
        text.startsWith("http://") ||
        text.startsWith("https://") ||
        text.startsWith("ed2k://") ||
        text.startsWith("thunder://");
      return { index: i + 1, text, valid: ok, reason: ok ? "" : t("无法识别的链接格式") };
    });
});

const valid = computed(() => lines.value.filter((l) => l.valid));
const incomingShare = computed(() => valid.value.find((line) => shareLink(line.text)));
// [] is the server's own download folder: no parent goes out (drive.CreateOfflineTask).
const destId = computed(() => cloudAdd.dest.at(-1)?.id ?? "");
const folder = computed(() => cloudAdd.dest.at(-1)?.name ?? t("我的文件"));
const path = computed(() => [t("全部文件"), ...cloudAdd.dest.map((c) => c.name)].join(" / "));
// With several accounts, name which one receives the files.
const destination = computed(() =>
  accounts.list.length > 1 && currentAccount.value ? `${accountLabel(currentAccount.value)} / ${folder.value}` : folder.value,
);

// The gutter and the state column sit beside the textarea, one entry per raw
// line so they stay aligned with what the user sees; blank lines get no
// number. Parsed entries come out in raw order, so a running cursor maps them.
interface Gutter {
  key: number;
  no: string;
  line: Line | null;
}

const gutter = computed<Gutter[]>(() => {
  let next = 0;
  return draft.value.split("\n").map((raw, i) => {
    if (!raw.trim()) return { key: i, no: "", line: null };
    const line = lines.value[next++];
    return { key: i, no: String(line.index).padStart(2, "0"), line };
  });
});

const rows = computed(() => Math.max(3, gutter.value.length));

function close() {
  ui.cloudAddOpen = false;
}

function clearLinks() {
  if (submitting.value || cloudAdd.reading) return;
  draft.value = "";
  results.value = [];
}

function cancel() {
  if (submitting.value || cloudAdd.reading) return;
  externalOpen.paused = true;
  if (cloudAdd.tab === "link") clearLinks();
  else clearTorrent();
  close();
}

function pasteShare(event: ClipboardEvent) {
  const text = event.clipboardData?.getData("text/plain") ?? "";
  if (!shareLink(text)) return;
  const field = event.target as HTMLTextAreaElement;
  if (draft.value.trim() && (field.selectionStart !== 0 || field.selectionEnd !== draft.value.length)) return;
  event.preventDefault();
  if (openPikPakShare(text)) clearLinks();
}

async function submit() {
  if (incomingShare.value) {
    const text = incomingShare.value.text;
    if (openPikPakShare(text)) draft.value = draft.value.split("\n").filter((line) => line.trim() !== text).join("\n");
    return;
  }
  cloudAdd.submitting = true;
  try {
    results.value = (await addOffline(valid.value.map((l) => l.text), destId.value)) ?? [];
    if (results.value.length && results.value.every((r) => r.ok)) {
      draft.value = "";
      results.value = [];
      close();
    }
  } finally {
    cloudAdd.submitting = false;
  }
}

// A fresh open starts from the last outcome cleared, so stale red marks do not
// read as new failures. With nothing pending it saves into the page's folder
// again; a pending draft keeps the folder picked for it.
watch(
  () => ui.cloudAddOpen,
  (open) => {
    if (!open) {
      if (relocate.mode === "cloudAdd") closeRelocate();
      return;
    }
    results.value = [];
    if (!cloudAdd.draft.trim() && !cloudAdd.torrent) {
      cloudAdd.dest = currentFolderId.value ? drive.trail.map((c) => ({ ...c })) : [];
    }
  },
);

function resultFor(text: string) {
  return results.value.find((r) => r.url === text);
}

// What the state column says for one entry: a server verdict wins over the
// local parse, so a rejected link keeps its reason after submission.
function verdict(line: Line): { ok: boolean; reason: string } {
  const r = resultFor(line.text);
  if (r) return { ok: r.ok, reason: r.ok ? "" : r.error };
  return { ok: line.valid, reason: line.reason };
}

/* ---------- Torrent ---------- */

// The torrent lives in the store beside the link draft, so switching tabs —
// or closing the sheet — never throws away a picked file and its selection.
const torrent = computed(() => cloudAdd.torrent);
const filesLayout = ref<"grid" | "list">("grid");
const files = computed<TorrentFile[]>(() => torrent.value?.files ?? []);
const pickedFiles = computed(() => files.value.filter((f) => cloudAdd.picked.has(f.index)));
const pickedSize = computed(() => pickedFiles.value.reduce((sum, f) => sum + f.size, 0));
const videoCount = computed(() => files.value.filter((f) => f.category === "video").length);

function toggleFile(file: TorrentFile) {
  if (cloudAdd.picked.has(file.index)) cloudAdd.picked.delete(file.index);
  else cloudAdd.picked.add(file.index);
}

function pickAll() {
  for (const f of files.value) cloudAdd.picked.add(f.index);
}

function pickNone() {
  cloudAdd.picked.clear();
}

// Videos Only is the one shortcut a torrent really needs: a release is one
// video among samples, screenshots and notes.
function pickVideos() {
  cloudAdd.picked.clear();
  for (const f of files.value) if (f.category === "video") cloudAdd.picked.add(f.index);
}

// The same buckets the file page's icons use (Go's categoryOf).
const GLYPH: Record<string, string> = { video: "video", audio: "audio", image: "image", document: "document", archive: "archive" };

function glyph(file: TorrentFile): string {
  return GLYPH[file.category] ?? "file";
}

async function submitTorrent() {
  cloudAdd.submitting = true;
  try {
    if (!(await addTorrent(destId.value))) return;
    clearTorrent();
    close();
  } finally {
    cloudAdd.submitting = false;
  }
}
</script>

<template>
  <Teleport to="body">
    <div v-if="ui.cloudAddOpen" class="scrim" @click.self="close">
      <section class="sheet glass glass-sheet" role="dialog" :aria-label="t('云添加')">
        <header>
          <div>
            <h2>{{ t("云添加") }}</h2>
            <p>{{ cloudAdd.tab === "torrent" ? t("把种子里的文件保存到你的云盘") : t("将链接内容保存到你的云盘") }}</p>
          </div>
          <button class="icon close" :title="t('关闭')" @click="close"><Icon name="close" :size="16" /></button>
        </header>

        <div class="source glass-well" role="tablist" :aria-label="t('添加方式')">
          <button
            role="tab"
            :class="{ on: cloudAdd.tab === 'link' }"
            :aria-selected="cloudAdd.tab === 'link'"
            :disabled="submitting || cloudAdd.reading"
            @click="cloudAdd.tab = 'link'"
          >
            <Icon name="link" :size="17" />{{ t("链接") }}
          </button>
          <button
            role="tab"
            :class="{ on: cloudAdd.tab === 'torrent' }"
            :aria-selected="cloudAdd.tab === 'torrent'"
            :disabled="submitting || cloudAdd.reading"
            @click="cloudAdd.tab = 'torrent'"
          >
            <Icon name="magnet" :size="17" />Torrent
          </button>
        </div>

        <p v-if="externalOpen.pending.length && !cloudAdd.reading" class="pending" role="status">
          {{ t("另有 {n} 项待打开，提交或清空对应类型的内容后继续。", { n: externalOpen.pending.length }) }}
        </p>

        <!-- The entry well is a recessed near-opaque surface. -->
        <template v-if="cloudAdd.tab === 'link'">
          <div class="well">
            <div class="gutter" aria-hidden="true">
              <span v-for="g in gutter" :key="g.key" class="no">{{ g.no || (gutter.length === 1 ? "01" : "") }}</span>
            </div>
            <textarea
              v-model="draft"
              :rows="rows"
              :disabled="submitting"
              wrap="off"
              spellcheck="false"
              :aria-label="t('每行一个链接')"
              placeholder="magnet:?xt=urn:btih:…&#10;https://example.com/clip.mp4"
              @paste="pasteShare"
            />
            <div class="states">
              <span v-for="g in gutter" :key="g.key" class="state">
                <template v-if="g.line">
                  <span v-if="verdict(g.line).ok" class="ok" :title="t('已识别')"><Icon name="check" :size="13" /></span>
                  <span v-else class="reason" :title="verdict(g.line).reason">
                    <Icon name="alert" :size="14" /><span class="why">{{ verdict(g.line).reason }}</span>
                  </span>
                </template>
              </span>
            </div>
          </div>

          <div class="hint">
            <span>{{ incomingShare ? t("PikPak 分享将先预览，确认后再转存") : t("每行一个链接，支持批量添加") }}</span>
            <span class="picks">
              <button v-if="draft" class="quiet" :disabled="submitting || cloudAdd.reading" @click="clearLinks">{{ t("清空链接") }}</button>
              <span class="tally">{{ t("已识别 {n} 条链接", { n: valid.length }) }}</span>
            </span>
          </div>
        </template>

        <!-- Torrent: the file is read on this computer — nothing is uploaded
             to look inside it — and the chosen files become one task. -->
        <template v-else>
          <div v-if="!torrent" class="well empty">
            <Icon name="magnet" :size="26" />
            <p>{{ t("选择一个 .torrent 文件，挑出要保存的文件") }}</p>
            <button class="pick" :disabled="cloudAdd.reading || submitting" @click="pickTorrent">
              <Orbit v-if="cloudAdd.reading" :size="15" :label="t('读取中')" />
              {{ cloudAdd.reading ? t("读取中…") : t("选择种子文件") }}
            </button>
          </div>

          <template v-else>
            <div class="torrent glass-well">
              <Icon name="magnet" :size="20" />
              <div class="what">
                <strong :title="torrent.name">{{ torrent.name }}</strong>
                <small>{{ t("{n} 个文件 · {size}", { n: files.length, size: bytes(torrent.size) }) }}</small>
              </div>
              <button class="quiet" :disabled="cloudAdd.reading || submitting" @click="clearTorrent">{{ t("移除") }}</button>
              <button class="quiet" :disabled="cloudAdd.reading || submitting" @click="pickTorrent">{{ t("换一个") }}</button>
            </div>

            <div class="files-toolbar">
              <span class="picks">
                <button class="quiet" :disabled="submitting" @click="pickAll">{{ t("全选") }}</button>
                <button class="quiet" :disabled="submitting" @click="pickNone">{{ t("全不选") }}</button>
                <button v-if="videoCount && videoCount < files.length" class="quiet" :disabled="submitting" @click="pickVideos">{{ t("只选视频") }}</button>
              </span>
              <div class="files-tools"><span class="tally">{{ t("已选 {n} 个 · {size}", { n: pickedFiles.length, size: bytes(pickedSize) }) }}</span><FileViewSwitch v-model="filesLayout" :disabled="submitting || cloudAdd.reading" /></div>
            </div>
            <PickerItems :layout="filesLayout" class="files glass-well" role="group" aria-label="Torrent">
              <label v-for="f in files" :key="f.index" class="picker-item" :class="{ 'is-picked': cloudAdd.picked.has(f.index) }">
                <span class="picker-thumb"><Icon :name="glyph(f)" :size="17" /></span>
                <span class="picker-copy">
                  <span class="picker-name" :title="f.path">{{ f.path }}</span>
                  <span class="picker-meta size">{{ bytes(f.size) }}</span>
                </span>
                <input class="picker-check" type="checkbox" :checked="cloudAdd.picked.has(f.index)" :disabled="submitting" @change="toggleFile(f)" />
              </label>
            </PickerItems>

          </template>
        </template>

        <div class="dest glass-well">
          <Icon name="folder" :size="20" filled />
          <span>{{ t("保存到") }}</span>
          <strong :title="cloudAdd.dest.length ? path : destination">{{ destination }}</strong>
          <small v-if="!destId">{{ t("（服务端的默认下载文件夹）") }}</small>
          <button class="quiet change" :disabled="submitting" @click="pickCloudAddFolder">{{ t("更改") }}</button>
        </div>

        <footer>
          <button :disabled="submitting || cloudAdd.reading" :title="t('丢弃当前内容并关闭')" @click="cancel">{{ t("取消") }}</button>
          <button
            v-if="cloudAdd.tab === 'torrent'"
            class="primary"
            :class="{ working: submitting }"
            :disabled="!pickedFiles.length || submitting"
            @click="submitTorrent"
          >
            <Orbit v-if="submitting" :size="15" :label="t('提交中')" />
            {{ submitting ? t("提交中…") : t("添加 {n} 个文件", { n: pickedFiles.length }) }}
          </button>
          <button v-else class="primary" :class="{ working: submitting }" :disabled="!valid.length || submitting" @click="submit">
            <Orbit v-if="submitting" :size="15" :label="t('提交中')" />
            {{ submitting ? t("提交中…") : incomingShare ? t("预览分享") : t("添加 {n} 项", { n: valid.length }) }}
          </button>
        </footer>
      </section>
    </div>
  </Teleport>
</template>

<style scoped>
.sheet {
  width: min(700px, calc(100vw - 80px));
  max-height: calc(100dvh - 48px);
  gap: 16px;
}

header {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: 12px;
}

header h2 {
  margin: 0;
  font-size: 26px;
  font-weight: 600;
  line-height: 34px;
  letter-spacing: 0.01em;
}

header p {
  margin: 6px 0 0;
  font-size: 15px;
  font-weight: 500;
  color: var(--fg-dim);
}

.close {
  width: 40px;
  height: 40px;
  min-height: 40px;
}

/* ---------- source segments ---------- */

.source {
  display: grid;
  grid-template-columns: 1fr 1fr;
  padding: 4px;
  border-radius: 14px;
}

.source button {
  min-height: 40px;
  gap: 10px;
  border: none;
  border-radius: 11px;
  background: none;
  color: var(--fg-dim);
  font-size: 15px;
  font-weight: 500;
}

.source button.on {
  background: var(--primary) border-box;
  color: var(--on-primary);
  font-weight: 600;
  box-shadow: inset 0 1px 0 var(--primary-rim), 0 6px 16px var(--primary-glow);
}

.source button:disabled {
  opacity: 0.6;
}

.pending {
  margin: 0;
  color: var(--fg-dim);
  font-size: 13px;
  line-height: 1.5;
}

/* ---------- the entry well ---------- */

.well {
  --row: 32px;
  display: flex;
  align-items: flex-start;
  max-height: 260px;
  overflow-y: auto;
  padding: 10px 14px 10px 18px;
  border-radius: 16px;
  background: var(--canvas);
  border: 1px solid var(--line);
  box-shadow: inset 0 1px 2px rgba(0, 0, 0, 0.08);
  transition: border-color var(--dur) var(--ease);
}

.well:focus-within {
  border-color: color-mix(in srgb, var(--accent) 60%, transparent);
}

.gutter,
.states {
  display: flex;
  flex-direction: column;
  flex: none;
  padding-top: 1px;
}

.no,
.state {
  display: flex;
  align-items: center;
  height: var(--row);
  font-variant-numeric: tabular-nums;
}

.no {
  width: 36px;
  font-size: 14px;
  color: var(--fg-dim);
}

.well textarea {
  flex: 1;
  min-width: 0;
  margin: 0;
  padding: 1px 0 0;
  border: none;
  border-radius: 0;
  background: transparent;
  resize: none;
  overflow-y: hidden;
  overflow-x: auto;
  white-space: pre;
  font-family: inherit;
  font-size: 15px;
  font-weight: 500;
  line-height: var(--row);
  color: var(--fg);
}

.well textarea:focus {
  background: transparent;
}

.well textarea::placeholder {
  color: var(--fg-dim);
  opacity: 0.7;
}

.states {
  align-items: flex-end;
  margin-left: 12px;
}

.ok {
  display: grid;
  place-items: center;
  width: 20px;
  height: 20px;
  border-radius: 50%;
  background: var(--success);
  color: var(--canvas-flat);
}

/* A server's own words can run long: the line keeps its height, the rest
   is in the tooltip. */
.reason {
  display: inline-flex;
  align-items: center;
  gap: 5px;
  max-width: 180px;
  font-size: 13px;
  font-weight: 500;
  color: var(--danger);
}

.reason svg {
  flex: none;
}

.why {
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

/* ---------- Torrent ---------- */

/* The empty state is the same recessed well, used as one column. */
.well.empty {
  flex-direction: column;
  align-items: center;
  justify-content: center;
  gap: 12px;
  min-height: 180px;
  padding: 24px;
  color: var(--fg-dim);
}

.well.empty p {
  margin: 0;
  font-size: 15px;
  font-weight: 500;
}

.well.empty .pick {
  min-height: 42px;
  padding: 0 22px;
  gap: 10px;
  font-size: 15px;
  font-weight: 600;
}

.torrent {
  display: flex;
  align-items: center;
  gap: 12px;
  min-height: 54px;
  padding: 10px 18px;
  border-radius: 14px;
}

.torrent > svg {
  flex: none;
  color: var(--accent);
}

.torrent .what {
  display: flex;
  flex-direction: column;
  min-width: 0;
  margin-right: auto;
}

.torrent strong {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  font-size: 15px;
  font-weight: 600;
}

.torrent small {
  font-size: 13px;
  font-weight: 500;
  color: var(--fg-dim);
}

.files-toolbar {
  display: flex;
  align-items: center;
  justify-content: space-between;
  flex-wrap: wrap;
  gap: 8px;
}

.files {
  max-height: 260px;
  min-height: 120px;
  overflow-y: auto;
  overscroll-behavior: contain;
  scrollbar-width: thin;
  border: 1px solid var(--line);
  border-radius: 16px;
}

.files-tools { display: flex; align-items: center; gap: 10px; }

.size {
  font-variant-numeric: tabular-nums;
}

.picks {
  display: flex;
  align-items: center;
  gap: 4px;
}

.picks .quiet,
.torrent .quiet,
.dest .change {
  min-height: 28px;
  padding: 0 10px;
  border: none;
  border-radius: 8px;
  background: none;
  font-size: 14px;
  font-weight: 500;
  color: var(--accent);
}

.picks .quiet:hover,
.torrent .quiet:hover,
.dest .change:hover {
  background: var(--accent-soft);
}

.dest .change {
  flex: none;
}

/* ---------- helper row ---------- */

.hint {
  display: flex;
  justify-content: space-between;
  gap: 12px;
  margin-top: -2px;
  font-size: 14px;
  font-weight: 500;
  color: var(--fg-dim);
}

.tally em {
  margin: 0 3px;
  font-style: normal;
  font-weight: 600;
  color: var(--accent);
}

/* ---------- destination ---------- */

.dest {
  display: flex;
  align-items: center;
  gap: 12px;
  min-height: 54px;
  padding: 10px 18px;
  border-radius: 14px;
  font-size: 15px;
  font-weight: 500;
}

.dest > svg {
  flex: none;
  color: var(--accent);
}

.dest span {
  color: var(--fg-dim);
}

.dest strong {
  margin-right: auto;
  font-weight: 500;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.dest small {
  font-size: 13px;
  color: var(--fg-dim);
  white-space: nowrap;
}

/* ---------- footer ---------- */

footer {
  display: flex;
  justify-content: flex-end;
  gap: 10px;
  margin-top: 4px;
}

footer button {
  min-width: 180px;
  min-height: 46px;
  font-size: 16px;
  font-weight: 600;
}

footer button.primary {
  min-width: 196px;
}
@media (max-height: 700px) {
  .sheet { padding: 20px 24px; gap: 10px; }
  header h2 { font-size: 22px; line-height: 28px; }
  header p { margin: 3px 0 0; font-size: 13px; }
  .source button { min-height: 34px; }
}
@media (max-width: 600px) {
  .sheet { width: calc(100vw - 32px); padding: 20px; gap: 10px; }
  .files-tools { flex-wrap: wrap; margin-left: auto; justify-content: flex-end; }
  .dest { flex-wrap: wrap; }
  footer button, footer button.primary { flex: 1; min-width: 0; }
}
</style>
