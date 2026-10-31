<script setup lang="ts">
// The menu bar panel (Menu Bar Resident): a small window Go anchors
// under PKP's menu bar item. It says whether the main window is closed, which
// account the file page shows, and how every account's transfers stand —
// one transfer in detail, the rest as counts — and it only navigates: the
// buttons bring the main window back on a page, or quit. Nothing here
// pauses, retries or switches accounts. Its state comes from Go
// (MenuBarService) and the account names from DriveService.Accounts.
import { computed, onBeforeUnmount, onMounted, ref } from "vue";
import { Events } from "@wailsio/runtime";
import { DriveService, MenuBarService } from "../bindings/github.com/tonyflux/pkp/internal/services";
import type { Account, MenuBarState } from "../bindings/github.com/tonyflux/pkp/internal/services";
import Icon from "./components/Icon.vue";
import Logo from "./components/Logo.vue";
import { onWindows } from "./dom";
import { t } from "./i18n";
import { countLine, inboxCard, panelCard, windowLine } from "./menubar";
import { accountLabel } from "./store";

const state = ref<MenuBarState | null>(null);
const accounts = ref<Account[]>([]);

function name(id: string): string {
  return accountLabel(accounts.value.find((a) => a.id === id)) || t("原账号");
}

const current = computed(() => accounts.value.find((a) => a.id === state.value?.account) ?? null);
const accountName = computed(() => (current.value ? accountLabel(current.value) : t("未登录")));
// App Lock: Go sends counts only; the panel names nothing.
const locked = computed(() => !!state.value?.locked);
const card = computed(() => (state.value ? panelCard(state.value, name) : null));
const count = computed(() => (state.value ? countLine(state.value) : ""));
// countLine joins its parts with " · "; a line may break only between them,
// the dot staying at the end of the line it closes.
const countParts = computed(() => count.value.split(" · "));
const subtitle = computed(() => (state.value ? windowLine(state.value) : t("仍在运行")));
const percent = computed(() => (card.value?.progress == null ? 0 : Math.floor(card.value.progress)));
const inbox = computed(() => (state.value ? inboxCard(state.value, name, accounts.value.length > 1) : null));

const ICON: Record<string, string> = {
  running: "transfer",
  waiting: "clock",
  attention: "alert",
  idle: "check",
  unknown: "globe",
  reading: "clock",
};
const kindIcon = computed(() => {
  const c = card.value;
  if (!c) return "clock";
  if (c.tone === "running") return c.source === "upload" ? "upload" : c.source === "download" ? "download" : "cloudAdd";
  return ICON[c.tone];
});

async function loadAccounts() {
  accounts.value = (await DriveService.Accounts().catch(() => null)) ?? [];
}

function open(target: "app" | "files" | "transfers" | "cloudAdd" | "sync") {
  const c = card.value;
  const transfers = target === "transfers" && c;
  void MenuBarService.Open(target, transfers ? c.source : "", transfers ? c.key : "");
}

// View Organization Suggestions: the main window opens the assistant's plan for the arrival.
function openArrival() {
  if (inbox.value) void MenuBarService.Open("assistant", "menubar", inbox.value.key);
}

function quit() {
  void MenuBarService.Quit();
}

// The window is exactly as tall as the panel's content; Go keeps it at the
// item (top edge under the macOS menu bar, bottom edge above a Windows
// taskbar) and caps it on a short screen, where the panel scrolls.
const body = ref<HTMLElement | null>(null);
let sized = 0;
const resize = new ResizeObserver(() => {
  const h = Math.ceil(body.value?.getBoundingClientRect().height ?? 0);
  if (h > 0 && h !== sized) {
    sized = h;
    void MenuBarService.Fit(h);
  }
});

const panel = ref<HTMLElement | null>(null);
function onKeydown(event: KeyboardEvent) {
  if (event.key === "Escape") {
    event.preventDefault();
    void MenuBarService.Dismiss();
  }
}
// Opening the panel gives its window focus; keyboard users start in it.
function onFocus() {
  if (!panel.value?.contains(document.activeElement)) panel.value?.focus({ preventScroll: true });
}

// Light or dark is the system's, as the bar the panel hangs from.
const dark = window.matchMedia("(prefers-color-scheme: dark)");
function applyTheme() {
  document.documentElement.dataset.theme = dark.matches ? "dark" : "light";
}

const offs: (() => void)[] = [];
onMounted(async () => {
  document.documentElement.dataset.window = "menubar";
  applyTheme();
  dark.addEventListener("change", applyTheme);
  window.addEventListener("keydown", onKeydown);
  window.addEventListener("focus", onFocus);
  if (body.value) resize.observe(body.value);
  offs.push(
    Events.On("menubar:state", (event: { data: MenuBarState }) => {
      state.value = event.data;
    }),
    Events.On("drive:accounts", () => void loadAccounts()),
  );
  await loadAccounts();
  state.value = await MenuBarService.State().catch(() => state.value);
});
onBeforeUnmount(() => {
  dark.removeEventListener("change", applyTheme);
  window.removeEventListener("keydown", onKeydown);
  window.removeEventListener("focus", onFocus);
  resize.disconnect();
  for (const off of offs) off();
});
</script>

<template>
  <main ref="panel" class="menubar" tabindex="-1" :aria-label="onWindows ? t('PKP 系统托盘状态') : t('PKP 菜单栏状态')">
    <div ref="body" class="body">
      <header class="head">
        <Logo :size="38" />
        <div class="titles">
          <strong>PKP <small>for PikPak</small></strong>
          <span>{{ subtitle }}</span>
        </div>
        <span class="signal" aria-hidden="true"></span>
      </header>

      <div v-if="locked" class="account-line">
        <span>{{ t("应用锁") }}</span>
        <strong>{{ t("已锁定") }}</strong>
      </div>
      <div v-else class="account-line">
        <span>{{ t("当前账号") }}</span>
        <strong :title="accountName">{{ accountName }}</strong>
      </div>

      <section class="activity" aria-labelledby="menubar-activity">
        <div class="section-heading">
          <h2 id="menubar-activity">{{ t("传输状态") }}</h2>
          <span class="count">
            <template v-for="(part, i) in countParts" :key="i">
              <span class="part">{{ part }}<template v-if="i < countParts.length - 1">&nbsp;·</template></span>{{ " " }}
            </template>
          </span>
        </div>

        <div v-if="locked" class="card idle" role="status">
          <div class="top">
            <span class="glyph"><Icon name="lock" :size="18" /></span>
            <div class="words">
              <strong>{{ t("PKP 已锁定") }}</strong>
              <small>{{ t("文件与账号信息已隐藏，传输继续运行") }}</small>
            </div>
          </div>
        </div>
        <div v-else-if="card" class="card" :class="card.tone" role="status">
          <div class="top">
            <span class="glyph"><Icon :name="kindIcon" :size="18" /></span>
            <div class="words">
              <strong :title="card.title">{{ card.title }}</strong>
              <small :title="card.detail">{{ card.detail }}</small>
            </div>
            <b v-if="card.progress !== null">{{ percent }}%</b>
          </div>
          <template v-if="card.progress !== null">
            <div
              class="progress"
              role="progressbar"
              :aria-label="t('{title} 进度', { title: card.title })"
              aria-valuemin="0"
              aria-valuemax="100"
              :aria-valuenow="percent"
            >
              <span :style="{ width: `${card.progress}%` }"></span>
            </div>
            <p class="foot">
              <span>{{ card.amount }}</span>
              <span>{{ card.note }}</span>
            </p>
          </template>
          <p v-else-if="card.amount" class="foot">
            <span>{{ card.amount }}</span>
          </p>
        </div>
        <p v-if="!locked && card?.extra" class="extra"><Icon name="alert" :size="14" />{{ card.extra }}</p>

        <button v-if="!locked" type="button" class="section-link" @click="open('transfers')">
          {{ t("打开传输中心") }} <Icon name="chevronRight" :size="15" />
        </button>
      </section>

      <section v-if="state && (state.syncRunning || state.syncAttention)" class="activity" aria-live="polite">
        <p>{{ t("同步：{running} 个进行中，{attention} 个需要处理", { running: state.syncRunning, attention: state.syncAttention }) }}</p>
        <button v-if="!locked" type="button" class="section-link" @click="open('sync')">
          {{ t("查看同步") }} <Icon name="chevronRight" :size="15" />
        </button>
      </section>

      <section v-if="inbox && !locked" class="inbox" aria-labelledby="menubar-inbox">
        <div class="section-heading">
          <h2 id="menubar-inbox">{{ t("新入库") }}</h2>
          <span>{{ inbox.extra }}</span>
        </div>
        <div class="card arrival" role="status">
          <div class="top">
            <span class="glyph"><Icon name="spark" :size="18" /></span>
            <div class="words">
              <strong :title="inbox.title">{{ inbox.title }}</strong>
              <small :title="inbox.detail">{{ inbox.detail }}</small>
            </div>
          </div>
          <button type="button" class="arrival-open" @click="openArrival">
            {{ t("查看整理建议") }} <Icon name="chevronRight" :size="15" />
          </button>
        </div>
      </section>

      <section v-if="!locked" class="quick" :aria-label="t('快捷操作')">
        <button type="button" :disabled="!current" :title="current ? '' : t('登录后可用')" @click="open('cloudAdd')">
          <Icon name="plus" :size="16" />{{ t("云添加") }}
        </button>
        <button type="button" @click="open('files')"><Icon name="folder" :size="16" />{{ t("打开文件") }}</button>
      </section>

      <footer class="footer">
        <button v-if="locked" type="button" @click="open('app')">{{ t("解锁 PKP") }} <span aria-hidden="true">↗</span></button>
        <button v-else type="button" @click="open('app')">{{ t("打开 PKP") }} <span aria-hidden="true">↗</span></button>
        <button type="button" class="quit" @click="quit">{{ t("退出 PKP") }} <span v-if="!onWindows" aria-hidden="true">⌘ Q</span></button>
      </footer>
    </div>
  </main>
</template>

<style>
/* The window is the panel: nothing paints behind it but the system's frost. */
:root[data-window="menubar"],
:root[data-window="menubar"] body {
  background: transparent;
}
</style>

<style scoped>
.menubar {
  --mb-fill: rgba(27, 43, 64, 0.86);
  --mb-fg: #f2f8ff;
  --mb-muted: #b6c9d9;
  --mb-accent: #acd9ff;
  --mb-line: rgba(202, 222, 243, 0.16);
  --mb-well: rgba(9, 29, 52, 0.43);
  --mb-card: rgba(12, 29, 49, 0.64);
  --mb-card-line: rgba(194, 216, 237, 0.19);
  --mb-button: rgba(108, 146, 193, 0.13);
  --mb-button-line: rgba(178, 213, 245, 0.2);
  --mb-hover: rgba(145, 187, 226, 0.2);
  --mb-icon: rgba(100, 159, 215, 0.2);
  --mb-track: rgba(133, 174, 211, 0.23);
  --mb-success: #95ddc0;
  --mb-warning: #f3c878;

  height: 100%;
  overflow-y: auto;
  overscroll-behavior: contain;
  background: var(--mb-fill);
  color: var(--mb-fg);
  font-size: 13px;
  line-height: 1.45;
  outline: none;
  -webkit-user-select: none;
  user-select: none;
}

:root[data-theme="light"] .menubar {
  --mb-fill: rgba(246, 250, 255, 0.84);
  --mb-fg: #16202c;
  --mb-muted: #566676;
  --mb-accent: #2763c4;
  --mb-line: rgba(30, 60, 100, 0.12);
  --mb-well: rgba(30, 70, 120, 0.07);
  --mb-card: rgba(255, 255, 255, 0.7);
  --mb-card-line: rgba(30, 60, 100, 0.12);
  --mb-button: rgba(255, 255, 255, 0.62);
  --mb-button-line: rgba(30, 60, 100, 0.14);
  --mb-hover: rgba(39, 99, 196, 0.1);
  --mb-icon: rgba(39, 99, 196, 0.12);
  --mb-track: rgba(39, 99, 196, 0.16);
  --mb-success: #1f8a5c;
  --mb-warning: #a8670a;
}

@media (prefers-reduced-transparency: reduce) {
  .menubar {
    --mb-fill: #1c3048;
  }
  :root[data-theme="light"] .menubar {
    --mb-fill: #f4f8fc;
  }
}

.head {
  display: flex;
  align-items: center;
  gap: 11px;
  padding: 17px 18px 14px;
}

.titles {
  flex: 1;
  min-width: 0;
}

.titles strong {
  display: block;
  font-size: 16px;
  line-height: 20px;
}

.titles small {
  font-weight: 450;
  font-size: 10px;
  color: var(--mb-muted);
}

.titles span {
  display: block;
  margin-top: 2px;
  color: var(--mb-muted);
  font-size: 12px;
}

.signal {
  flex: none;
  width: 7px;
  height: 7px;
  border-radius: 50%;
  background: var(--mb-success);
  box-shadow: 0 0 0 4px color-mix(in srgb, var(--mb-success) 14%, transparent);
}

.account-line {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  margin: 0 14px 12px;
  padding: 9px 11px;
  border-radius: 9px;
  background: var(--mb-well);
  color: var(--mb-muted);
  font-size: 12px;
}

.account-line strong {
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  color: var(--mb-fg);
  font-weight: 600;
}

.activity,
.inbox {
  padding: 15px 17px 10px;
  border-top: 1px solid var(--mb-line);
}

.inbox {
  padding-bottom: 14px;
}

.arrival .glyph {
  background: color-mix(in srgb, var(--mb-warning) 14%, transparent);
  color: var(--mb-warning);
}

.arrival-open {
  display: flex;
  align-items: center;
  justify-content: center;
  gap: 4px;
  width: 100%;
  height: 32px;
  margin-top: 11px;
  border: 1px solid var(--mb-button-line);
  border-radius: 8px;
  background: var(--mb-button);
  color: var(--mb-accent);
  font: inherit;
  font-size: 12px;
  font-weight: 600;
  cursor: pointer;
}

.arrival-open:hover {
  background: var(--mb-hover);
}

.section-heading {
  display: flex;
  align-items: baseline;
  justify-content: space-between;
  gap: 9px;
  margin-bottom: 12px;
}

.section-heading h2 {
  flex: none;
  margin: 0;
  font-size: 13px;
  font-weight: 650;
  white-space: nowrap;
}

/* The counts are words: a longer language takes a second line, on the
   right, rather than losing its end. */
.section-heading > span {
  min-width: 0;
  text-align: right;
  text-wrap: balance;
  color: var(--mb-muted);
  font-size: 11px;
}

.count .part {
  white-space: nowrap;
}

.card {
  padding: 12px;
  border: 1px solid var(--mb-card-line);
  border-radius: 12px;
  background: var(--mb-card);
}

.card:not(.running) {
  min-height: 76px;
  display: flex;
  flex-direction: column;
  justify-content: center;
}

.top {
  display: flex;
  align-items: center;
  gap: 10px;
}

/* Not .icon: a parent's scoped rule also matches a child component's root, Icon's svg.icon. */
.glyph {
  flex: none;
  width: 34px;
  height: 34px;
  display: grid;
  place-items: center;
  border-radius: 9px;
  background: var(--mb-icon);
  color: var(--mb-accent);
}

.idle .glyph {
  background: color-mix(in srgb, var(--mb-success) 14%, transparent);
  color: var(--mb-success);
}

.attention .glyph {
  background: color-mix(in srgb, var(--mb-warning) 14%, transparent);
  color: var(--mb-warning);
}

.unknown .glyph,
.reading .glyph,
.waiting .glyph {
  color: var(--mb-muted);
}

.words {
  flex: 1;
  min-width: 0;
}

.words strong {
  display: block;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  font-size: 12px;
  font-weight: 650;
}

.words small {
  display: block;
  margin-top: 1px;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  color: var(--mb-muted);
  font-size: 11px;
}

/* A card that names no file or task is all words of the panel's own:
   they wrap. An arrival's sentence keeps two lines before it gives way. */
.card:is(.idle, .unknown, .reading, .attention) .words :is(strong, small) {
  white-space: normal;
}

.arrival .words strong {
  display: -webkit-box;
  -webkit-box-orient: vertical;
  -webkit-line-clamp: 2;
  white-space: normal;
  overflow-wrap: anywhere;
}

.top b {
  flex: none;
  color: var(--mb-accent);
  font-size: 12px;
  font-variant-numeric: tabular-nums;
}

.progress {
  height: 4px;
  margin: 13px 1px 8px;
  border-radius: 4px;
  background: var(--mb-track);
  overflow: hidden;
}

.progress span {
  display: block;
  height: 100%;
  border-radius: 4px;
  background: linear-gradient(90deg, #528deb, #a5dbff);
}

.foot {
  display: flex;
  flex-wrap: wrap;
  justify-content: space-between;
  column-gap: 8px;
  margin: 0;
  color: var(--mb-muted);
  font-size: 11px;
  font-variant-numeric: tabular-nums;
}

.foot span + span {
  margin-left: auto;
}

.card:not(.running) .foot {
  margin: 8px 0 0 44px;
}

.extra {
  display: flex;
  align-items: center;
  gap: 6px;
  margin: 8px 3px 0;
  color: var(--mb-warning);
  font-size: 11px;
}

.section-link {
  display: flex;
  align-items: center;
  justify-content: space-between;
  width: 100%;
  margin-top: 6px;
  padding: 9px 12px;
  border-radius: 9px;
  border: 0;
  background: transparent;
  color: var(--mb-accent);
  font: inherit;
  font-size: 12px;
  text-align: left;
  cursor: pointer;
}

.quick {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: 8px;
  padding: 0 17px 14px;
}

.quick button {
  display: flex;
  align-items: center;
  justify-content: flex-start;
  gap: 8px;
  min-height: 42px;
  padding: 0 11px;
  border: 1px solid var(--mb-button-line);
  border-radius: 9px;
  background: var(--mb-button);
  color: var(--mb-fg);
  font: inherit;
  font-size: 12px;
  text-align: left;
  cursor: pointer;
}

.quick button :deep(svg) {
  color: var(--mb-accent);
}

.quick button:disabled {
  opacity: 0.5;
  cursor: default;
}

.footer {
  padding: 7px;
  border-top: 1px solid var(--mb-line);
}

.footer button {
  width: 100%;
  height: 34px;
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 0 12px;
  border: 0;
  border-radius: 7px;
  background: transparent;
  color: var(--mb-fg);
  font: inherit;
  font-size: 12px;
  text-align: left;
  cursor: pointer;
}

.footer button span {
  color: var(--mb-muted);
  font-size: 11px;
}

.quick button:not(:disabled):hover,
.footer button:hover,
.section-link:hover {
  background: var(--mb-hover);
}

.menubar :focus-visible {
  outline: 2px solid var(--mb-accent);
  outline-offset: 2px;
}
</style>
