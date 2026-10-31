<script setup lang="ts">
// Settings:
// Groups on the rail: General holds what this machine decides, Notifications the system
// notifications Go posts, Account management the accounts on this computer, Transfers how cloud
// downloads land in the drive (stored with the account, so every PikPak client
// follows it) and the month's transfer allowance, AI the model the assistant
// talks to and the TMDB credential its
// Media organization matches titles with, Network & proxy the route PKP's own requests take,
// Connections & integrations the ways other
// apps reach the drive. Picking a connection opens its island beside the
// canvas (SettingsIsland). Credentials stay masked until asked for; an API
// key goes straight from its field to the keychain.
import { computed, nextTick, onBeforeUnmount, onMounted, reactive, ref, watch } from "vue";
import { DriveService } from "../../bindings/github.com/tonyflux/pkp/internal/services";
import Icon from "./Icon.vue";
import SelectMenu, { type SelectOption } from "./SelectMenu.vue";
import SuggestMenu, { type SuggestOption } from "./SuggestMenu.vue";
import AccountIdentity from "./AccountIdentity.vue";
import MembershipBadge from "./MembershipBadge.vue";
import Orbit from "./Orbit.vue";
import Toggle from "./Toggle.vue";
import UsageMeter from "./UsageMeter.vue";
import WebDAVIsland from "./WebDAVIsland.vue";
import AccessIsland from "./AccessIsland.vue";
import PrivacySettings from "./PrivacySettings.vue";
import ShortcutsSettings from "./ShortcutsSettings.vue";
import ConnectivityPanel from "./ConnectivityPanel.vue";
import UpdateSettings from "./UpdateSettings.vue";
import { isTyping, modKey, onWindows, systemName } from "../dom";
import { PROXY_MODES, PROXY_SCHEMES, useProxyDraft } from "../proxydraft";
import { PRESETS, previewPath, templateError, type NamingStyle } from "../templates";
import { AI_PRESETS, CUSTOM_PRESET_ID, presetOf } from "../aipresets";
import tmdbLogo from "../assets/tmdb.svg";
import { i18n, LOCALES, N_, t, type LanguagePref } from "../i18n";
import { vSlide } from "../slide";
import {
  DOWNLOAD_THREAD_CHOICES,
  PARALLEL_DOWNLOAD_CHOICES,
  PARALLEL_UPLOAD_CHOICES,
  accountLabel,
  accounts,
  aiSettings,
  applyAIPreset,
  clearWallpaper,
  connections,
  currentAccount,
  currentId,
  drive,
  forgetProxyPassword,
  indexText,
  loadAISettings,
  loadConnections,
  loadMembership,
  loadProxySettings,
  loadTransferSettings,
  openSignIn,
  openTMDBSite,
  pickDownloadDir,
  pickWallpaper,
  proxySettings,
  rebuildIndex,
  revealDownloadDir,
  saveAIConnection,
  saveAIKey,
  saveAINaming,
  saveAITokenLimit,
  saveTMDBKey,
  setAIConsent,
  setLayout,
  setDownloadThreads,
  setNavCompact,
  setParallelDownloads,
  setParallelUploads,
  setReduceTransparency,
  setResumableUploads,
  setSkin,
  setKeepPlaying,
  setNotifyEnabled,
  setNotifyForeground,
  allowNotifications,
  loadNotifySettings,
  openNotifySettings,
  sendTestNotice,
  notifySettingsSteps,
  systemNotices,
  setTransferPreference,
  settingsFocus,
  skins,
  sorts,
  switchAccount,
  testAIConnection,
  testProxy,
  testTMDB,
  tmdb,
  loadTMDBSettings,
  setThemeMode,
  type ThemeMode,
  ui,
  wallpaperSrc,
  watchAppLock,
  setLanguage,
} from "../store";

type Tab = "basic" | "notify" | "privacy" | "accounts" | "transfer" | "ai" | "network" | "connect" | "keys" | "updates";
type Panel = "" | "webdav" | "mcp" | "pat" | "apps";

const tab = ref<Tab>("basic");
const panel = ref<Panel>("");
// The three cards whose island is AccessIsland.
const access = computed(() => (panel.value === "mcp" || panel.value === "pat" || panel.value === "apps" ? panel.value : null));

const tabs: { key: Tab; label: string; icon: string }[] = [
  { key: "basic", label: N_("基本"), icon: "settings" },
  { key: "notify", label: N_("通知"), icon: "bell" },
  // tab|: the rail is narrower than these sections' own titles in some languages.
  { key: "privacy", label: N_("tab|隐私与安全"), icon: "lock" },
  { key: "accounts", label: N_("账号管理"), icon: "user" },
  { key: "transfer", label: N_("传输"), icon: "transfer" },
  { key: "ai", label: "AI", icon: "spark" },
  { key: "network", label: N_("tab|网络与代理"), icon: "shield" },
  { key: "connect", label: N_("tab|连接与集成"), icon: "link" },
  { key: "keys", label: N_("快捷键"), icon: "keyboard" },
  { key: "updates", label: N_("关于与更新"), icon: "info" },
];

const THEME_MODES: { key: ThemeMode; label: string }[] = [
  { key: "system", label: N_("跟随系统") },
  { key: "light", label: N_("浅色") },
  { key: "dark", label: N_("深色") },
];

const languageOptions = computed<SelectOption<LanguagePref>[]>(() => [{ value: "system", label: t("跟随系统") }, ...LOCALES]);
const languageNote = computed(() =>
  i18n.pref === "system"
    ? t("跟随这台电脑的语言设置，当前为{name}。", { name: LOCALES.find((l) => l.value === i18n.locale)?.label ?? i18n.locale })
    : t("PKP 的文字立即切换；系统的打开面板和验证窗口在下次启动 PKP 后跟上。"),
);

// The platform's own names, said in the UI's language.
const keychainName = computed(() => (onWindows ? t("凭据管理器") : t("钥匙串")));

// The four ways in, in the mock's order; each card's pill opens its island.
const links: { key: Exclude<Panel, "">; icon: string; title: string; desc: string; action: string }[] = [
  { key: "webdav", icon: "globe", title: "WebDAV", desc: N_("通过 WebDAV 将文件挂载到本地或第三方应用。"), action: N_("管理") },
  { key: "mcp", icon: "chip", title: N_("MCP 连接 AI 工具"), desc: N_("连接 MCP 兼容的工具，在支持的应用中使用你的文件资源。"), action: N_("查看设置") },
  { key: "pat", icon: "key", title: N_("个人访问令牌"), desc: N_("用于在受信任的应用或脚本中访问你的文件。"), action: N_("管理") },
  { key: "apps", icon: "apps", title: N_("已连接应用"), desc: N_("查看和管理已授权的第三方应用及其访问范围。"), action: N_("查看用量") },
];

const webdavState = computed<{ text: string; tone: "ok" | "off" } | null>(() => {
  const w = connections.webdav;
  if (!w) return null;
  if (!w.premium) return { text: t("需要会员"), tone: "off" };
  return w.enabled ? { text: t("已启用"), tone: "ok" } : { text: t("已停用"), tone: "off" };
});

const prefs = computed(() => connections.prefs);
const quota = computed(() => connections.quota);
const extra = computed(() => {
  const e = quota.value?.extra;
  return e && (e.cloudDownload.limit || e.downstream.limit || e.upload.limit) ? e : null;
});

function select(next: Tab) {
  tab.value = next;
  if (next !== "connect") panel.value = "";
}

// Manage account / Configure AI from elsewhere lands here, on mount or while open.
watch(
  () => settingsFocus.tab,
  (t) => {
    if (!t) return;
    select(t);
    settingsFocus.tab = "";
  },
  { immediate: true },
);

/** What the manager says of an account: the one on screen, signed in, or waiting for its password. */
function accountState(a: { id: string; needsPassword: boolean }): { text: string; tone: "current" | "" | "warning" } {
  if (a.needsPassword) return { text: t("需要重新登录"), tone: "warning" };
  return a.id === currentId.value ? { text: t("当前账号"), tone: "current" } : { text: t("已登录"), tone: "" };
}

function showAppsUsage() {
  select("connect");
  panel.value = "apps";
}

// Each visit reads again: a token created or a client added elsewhere shows
// up without restarting.
watch(
  tab,
  (t) => {
    if (t === "notify") void loadNotifySettings();
    if (t === "privacy") void watchAppLock();
    if (t === "connect") void loadConnections();
    if (t === "transfer") void loadTransferSettings();
    if (t === "ai") {
      void loadAISettings();
      void loadTMDBSettings();
    }
    if (t === "network") void loadProxySettings();
    // Purchases and renewals happen elsewhere: read every signed-in account's again.
    if (t === "accounts") for (const a of accounts.list) if (!a.needsPassword) void loadMembership(a.id);
  },
  { immediate: true },
);

/* ---------- Notifications ---------- */

const notices = computed(() => systemNotices.state);
const noticeLine = computed(() => {
  const s = notices.value;
  if (!s) return t("正在读取…");
  if (!s.available) return s.unavailable;
  if (!s.enabled) return t("已关闭：PKP 不会发送系统通知。");
  if (!s.allowed) return t("已开启，但系统还没有允许 PKP 发送通知。");
  return t("已开启：传输和任务有结果时，由系统通知告诉你。");
});
// macOS asks once and remembers the answer; Windows does not ask at all.
const notifyAllowHint = computed(() =>
  onWindows
    ? t("PKP 的通知在 Windows 里被关掉了。{steps}", { steps: t(notifySettingsSteps) })
    : t("macOS 只询问一次。没看到询问，或之前选了「不允许」，{steps}", { steps: t(notifySettingsSteps) }),
);

// Allow notifications is changed in the system's settings: coming back to the window reads it again.
function onWindowFocus() {
  if (tab.value === "notify") void loadNotifySettings();
}

/* ---------- AI ---------- */

const ai = computed(() => aiSettings.value);

// Provider presets (aipresets.ts): choosing one saves its provider, base URL and
// model at once; Custom keeps the fields to type into.
const PRESET_OPTIONS = computed<SelectOption<string>[]>(() => AI_PRESETS.map((p) => ({ value: p.id, label: p.label })));
// Suggestions under Base URL and Model name; either field still takes any text.
const BASE_URL_OPTIONS = computed<SuggestOption[]>(() =>
  AI_PRESETS.filter((p) => p.provider === "openai" && p.baseUrl).map((p) => ({ value: p.baseUrl, detail: p.label })),
);
const MODEL_OPTIONS = computed<SuggestOption[]>(() =>
  (sameModelConnection.value && modelList.ids?.length ? modelList.ids : presetOf(conn).models).map((value) => ({ value })),
);

// The connection fields edit a copy; a change or blur saves what differs.
const conn = reactive({ provider: "anthropic", baseUrl: "", model: "" });
// Set while saveConnection runs: the saved values do not overwrite the fields then.
let connSaving = false;
// Custom picked while the fields still match a preset: the select says so
// until something is saved; the Base URL field is there to type into.
const customPicked = ref(false);
// Typed into a field Vue does not bind: the key is read from the element
// when saved and the element emptied, so it never sits in reactive state.
const keyField = ref<HTMLInputElement | null>(null);
const keyTyped = ref(false);
const keyEditing = ref(false);
const limitDraft = ref("");
// What Per-conversation limit goes back to when Unlimited is turned off with nothing usable in the field.
const DEFAULT_TOKEN_LIMIT = 50000;
const style = ref<NamingStyle>("plex");
const custom = reactive({ series: "", movie: "" });

// Each card's fields follow the saved values only when those change, so a
// draft in one card survives saving another.
watch(
  () => (ai.value ? `${ai.value.provider}\n${ai.value.baseUrl}\n${ai.value.model}` : ""),
  () => {
    const s = ai.value;
    if (!s || connSaving) return;
    Object.assign(conn, { provider: s.provider === "openai" ? "openai" : "anthropic", baseUrl: s.baseUrl, model: s.model });
    customPicked.value = false;
  },
  { immediate: true },
);

// Lists belong to the saved endpoint and key, never to an unsaved URL draft.
const modelList = reactive({ ids: null as string[] | null, loading: false, error: "" });
let modelRequest = 0;
const sameModelConnection = computed(() => {
  const s = ai.value;
  return !!s && conn.provider === s.provider && (conn.provider === "anthropic" || conn.baseUrl.trim().replace(/\/+$/, "") === s.baseUrl);
});
const canFetchModels = computed(() => {
  const s = ai.value;
  if (!s || !sameModelConnection.value || aiSettings.saving) return false;
  if (s.keyHint) return true;
  try {
    return s.provider === "openai" && ["localhost", "127.0.0.1", "[::1]"].includes(new URL(s.baseUrl).hostname);
  } catch {
    return false;
  }
});
const modelHint = computed(() => {
  if (!sameModelConnection.value) return t("内置推荐仅供参考；可刷新节点模型或手动输入。");
  if (modelList.loading) return t("正在获取模型…");
  if (modelList.error) return t("获取模型失败：{error}。可使用内置推荐或手动输入。", { error: modelList.error });
  if (modelList.ids !== null) return modelList.ids.length
    ? t("已从当前节点获取 {n} 个模型；仍可手动输入。", { n: modelList.ids.length })
    : t("节点没有返回模型；仍可手动输入。");
  return canFetchModels.value
    ? t("内置推荐仅供参考；可刷新节点模型或手动输入。")
    : t("保存 API Key 后自动获取模型；也可手动输入。");
});

async function refreshModels() {
  if (!canFetchModels.value) return;
  const request = ++modelRequest;
  modelList.loading = true;
  modelList.error = "";
  try {
    const ids = await DriveService.AIModels();
    if (request === modelRequest) modelList.ids = ids ?? [];
  } catch (error) {
    if (request !== modelRequest) return;
    modelList.ids = null;
    modelList.error = error instanceof Error ? error.message : String(error);
  } finally {
    if (request === modelRequest) modelList.loading = false;
  }
}

watch(
  () => JSON.stringify([tab.value, ai.value?.provider, ai.value?.baseUrl, ai.value?.keyHint]),
  () => {
    ++modelRequest;
    Object.assign(modelList, { ids: null, loading: false, error: "" });
    if (tab.value === "ai") void refreshModels();
  },
  { immediate: true, flush: "post" },
);
onBeforeUnmount(() => { ++modelRequest; });
watch(
  () => ai.value?.tokenLimit,
  (n) => {
    // Unlimited (0) keeps the last number in the field, for when the limit is turned back on.
    if (n) limitDraft.value = String(n);
    else if (n === 0 && !limitDraft.value) limitDraft.value = String(DEFAULT_TOKEN_LIMIT);
  },
  { immediate: true },
);
watch(
  () => ai.value?.keyHint,
  (hint) => {
    if (hint !== undefined) keyEditing.value = !hint;
  },
  { immediate: true },
);

// The naming fields likewise; an empty custom template starts from Plex's.
watch(
  () => (ai.value ? `${ai.value.naming}\n${ai.value.seriesTemplate}\n${ai.value.movieTemplate}` : ""),
  () => {
    const s = ai.value;
    if (!s) return;
    style.value = s.naming === "chinese" || s.naming === "custom" ? s.naming : "plex";
    custom.series = s.seriesTemplate || PRESETS.plex.series;
    custom.movie = s.movieTemplate || PRESETS.plex.movie;
  },
  { immediate: true },
);

async function saveConnection() {
  if (connSaving) return;
  connSaving = true;
  try {
    // An edit made while the previous one was saving goes next; otherwise
    // the fields show what Go kept (trimmed, trailing "/" dropped) or, when
    // it refused, what is still saved.
    for (;;) {
      const s = ai.value;
      if (!s) return;
      const sent = { provider: conn.provider, baseUrl: conn.baseUrl.trim(), model: conn.model.trim() };
      if (sent.provider === s.provider && sent.model === s.model && (sent.provider !== "openai" || sent.baseUrl === s.baseUrl)) return;
      await saveAIConnection(sent.provider, sent.baseUrl, sent.model);
      const edited = conn.provider !== sent.provider || conn.baseUrl.trim() !== sent.baseUrl || conn.model.trim() !== sent.model;
      if (edited) continue;
      const saved = aiSettings.value;
      if (saved) Object.assign(conn, { provider: saved.provider, baseUrl: saved.baseUrl, model: saved.model });
      return;
    }
  } finally {
    connSaving = false;
  }
}

const preset = computed(() => (customPicked.value ? (AI_PRESETS.find((p) => p.id === CUSTOM_PRESET_ID) ?? presetOf(conn)) : presetOf(conn)));

function choosePreset(id: string | number) {
  if (id !== CUSTOM_PRESET_ID) {
    customPicked.value = false;
    void applyAIPreset(String(id));
    return;
  }
  customPicked.value = true;
  // Custom is an OpenAI-compatible service: Anthropic's model would only be refused there.
  if (conn.provider !== "openai") Object.assign(conn, { provider: "openai", baseUrl: "", model: "" });
}

async function saveKey() {
  const el = keyField.value;
  const key = el?.value.trim() ?? "";
  if (!el || !key) return;
  el.value = "";
  keyTyped.value = false;
  const previousHint = ai.value?.keyHint;
  if (await saveAIKey(key)) {
    keyEditing.value = false;
    if (previousHint === ai.value?.keyHint) void refreshModels();
  }
}

async function editKey() {
  keyEditing.value = true;
  await nextTick();
  keyField.value?.focus();
}

function cancelKey() {
  if (keyField.value) keyField.value.value = "";
  keyTyped.value = false;
  keyEditing.value = !ai.value?.keyHint;
}

const TEST_TEXT: Record<string, string> = {
  ok: N_("连接成功"),
  auth: N_("Key 无效或没有权限"),
  unreachable: N_("地址不可达"),
  config: N_("还不能测试"),
};
const testText = computed(() => {
  const r = aiSettings.test;
  if (!r) return "";
  if (r.kind === "server") return t("服务端返回错误（HTTP {status}）", { status: String(r.status) });
  return TEST_TEXT[r.kind] ? t(TEST_TEXT[r.kind]) : t("测试失败");
});

const unlimited = computed(() => ai.value?.tokenLimit === 0);

const limitError = computed(() => {
  if (unlimited.value) return "";
  const n = Number(limitDraft.value);
  if (!limitDraft.value.trim() || !Number.isInteger(n)) return t("请填写整数");
  return n < 1000 || n > 2000000 ? t("限额应在 {min} 到 {max} tokens 之间", { min: 1000, max: 2000000 }) : "";
});

async function saveLimit() {
  const n = Number(limitDraft.value);
  if (unlimited.value || limitError.value || n === ai.value?.tokenLimit) return;
  await saveAITokenLimit(n);
}

/** setUnlimited is the Unlimited switch; turning it off brings back the number in the field. */
async function setUnlimited(on: boolean) {
  if (on) {
    await saveAITokenLimit(0);
    return;
  }
  const n = Number(limitDraft.value);
  await saveAITokenLimit(Number.isInteger(n) && n >= 1000 && n <= 2000000 ? n : DEFAULT_TOKEN_LIMIT);
}

const TOOLS_TEXT: Record<string, string> = { yes: N_("支持工具调用"), no: N_("不支持（对话将用单步模式）") };
const STREAM_TEXT: Record<string, string> = { yes: N_("支持流式"), no: N_("不支持（回答整段显示）") };
// The last test of this session, else what was found before for this service.
const capabilities = computed(() => {
  const r = aiSettings.test;
  if (r?.ok) return { tools: r.tools, stream: r.stream };
  const s = ai.value;
  return s?.configured && (s.tools || s.stream) ? { tools: s.tools, stream: s.stream } : null;
});

// Before sending to AI: per account, Ask every time or Always allow. Only the
// metadata level is offered: nothing sends subtitle text (aiContentConsent) yet.
const CONSENT_LEVELS = [
  { key: "metadata", label: N_("文件名与路径等元数据"), short: N_("元数据") },
] as const;
const CONSENT_OPTIONS = computed<SelectOption<string>[]>(() => [
  { value: "ask", label: t("每次询问") },
  { value: "always", label: t("始终允许") },
]);

// Custom saves through Save once both templates pass; a preset saves at once,
// keeping the custom ones for next time.
const seriesError = computed(() => (style.value === "custom" ? templateError(custom.series, "series") : ""));
const movieError = computed(() => (style.value === "custom" ? templateError(custom.movie, "movie") : ""));
const previews = computed(() => {
  const naming = style.value === "custom" ? custom : PRESETS[style.value];
  return { series: previewPath(naming.series, "series"), movie: previewPath(naming.movie, "movie") };
});
const customDirty = computed(() => {
  const s = ai.value;
  return !!s && (s.naming !== "custom" || s.seriesTemplate !== custom.series || s.movieTemplate !== custom.movie);
});

function chooseStyle(next: NamingStyle) {
  style.value = next;
  const s = ai.value;
  if (next !== "custom" && s && s.naming !== next) void saveAINaming(next, s.seriesTemplate, s.movieTemplate);
}

function saveCustom() {
  if (seriesError.value || movieError.value) return;
  void saveAINaming("custom", custom.series, custom.movie);
}

/* ---------- TMDB (Media organization) ---------- */

const ts = computed(() => tmdb.settings);

// The TMDB credential, like the AI key: typed into an unbound field, read
// from it once when saved, never kept in reactive state.
const tmdbField = ref<HTMLInputElement | null>(null);
const tmdbTyped = ref(false);
const tmdbEditing = ref(false);
watch(
  () => ts.value?.tmdbKeyHint,
  (hint) => {
    if (hint !== undefined) tmdbEditing.value = !hint;
  },
  { immediate: true },
);

async function saveTmdb() {
  const el = tmdbField.value;
  const key = el?.value.trim() ?? "";
  if (!el || !key) return;
  el.value = "";
  tmdbTyped.value = false;
  if (await saveTMDBKey(key)) tmdbEditing.value = false;
}

async function editTmdb() {
  tmdbEditing.value = true;
  await nextTick();
  tmdbField.value?.focus();
}

function cancelTmdb() {
  if (tmdbField.value) tmdbField.value.value = "";
  tmdbTyped.value = false;
  tmdbEditing.value = !ts.value?.tmdbKeyHint;
}

const TMDB_TEST: Record<string, string> = {
  ok: N_("连接成功"),
  auth: N_("凭据无效或已停用"),
  unreachable: N_("连不上 TMDB"),
  ratelimit: N_("请求过于频繁"),
  server: N_("TMDB 返回错误"),
  config: N_("还不能测试"),
};
const tmdbTestText = computed(() => {
  const r = tmdb.test;
  if (!r) return "";
  return TMDB_TEST[r.kind] ? t(TMDB_TEST[r.kind]) : t("测试失败");
});
const tmdbState = computed(() => (ts.value?.tmdbKeyHint ? t("已配置") : t("未配置")));

/* ---------- Network & proxy ---------- */

/*
 * The draft lives in this component, so moving between Settings categories keeps it
 * while leaving Settings — or reloading the window — throws it away and the
 * saved settings are read again.
 */
const {
  px,
  proxy,
  proxyPassword,
  proxyPasswordShown,
  proxyAdvanced,
  proxyModeButtons,
  proxyInput,
  proxyFieldError,
  proxyIncomplete,
  proxyDirty,
  proxyRouteNode,
  proxyPending,
  proxyStatus,
  proxyElapsed,
  chooseProxyMode,
  onProxyModeKey,
  applyProxyDraft,
} = useProxyDraft();

// Esc closes the island, unless a field inside it is taking the key.
function onKey(ev: KeyboardEvent) {
  if (ev.key !== "Escape" || !panel.value || ev.defaultPrevented) return;
  if (isTyping(ev)) return;
  panel.value = "";
}

onMounted(() => {
  window.addEventListener("keydown", onKey);
  window.addEventListener("focus", onWindowFocus);
});
onBeforeUnmount(() => {
  window.removeEventListener("keydown", onKey);
  window.removeEventListener("focus", onWindowFocus);
});
</script>

<template>
  <section class="settings canvas">
    <header class="head">
      <h1>{{ t("设置") }}</h1>
      <p>{{ t("管理偏好与连接") }}</p>
    </header>

    <div class="cols">
      <nav v-slide class="rail" :aria-label="t('设置分组')">
        <button
          v-for="item in tabs"
          :key="item.key"
          :class="{ on: tab === item.key }"
          :title="t(item.label)"
          :aria-current="tab === item.key ? 'page' : undefined"
          @click="select(item.key)"
        >
          <Icon :name="item.icon" :size="20" />
          {{ t(item.label) }}
        </button>
      </nav>

      <div class="main">
        <!-- ---------- General ---------- -->
        <template v-if="tab === 'basic'">
          <h2>{{ t("基本") }}</h2>
          <p class="lead">{{ t("这些选项只影响这台电脑上的显示方式。") }}</p>

          <div class="card">
            <div class="card-text">
              <strong>{{ t("界面语言") }}</strong>
              <span>{{ languageNote }}</span>
            </div>
            <span class="select glass-well language-pick">
              <SelectMenu class="face" :model-value="i18n.pref" :options="languageOptions" :label="t('界面语言')" @update:model-value="setLanguage" />
              <Icon name="chevronDown" :size="16" />
            </span>
          </div>

          <div class="card">
            <div class="card-text">
              <strong>{{ t("外观") }}</strong>
              <span>{{
                ui.themeMode === "system"
                  ? ui.theme === "dark"
                    ? t("跟随 {system} 的外观设置，当前为深色。", { system: systemName })
                    : t("跟随 {system} 的外观设置，当前为浅色。", { system: systemName })
                  : t("固定使用，不随系统切换。")
              }}</span>
            </div>
            <div v-slide class="seg glass-well" role="radiogroup" :aria-label="t('外观')">
              <button
                v-for="m in THEME_MODES"
                :key="m.key"
                role="radio"
                :aria-checked="ui.themeMode === m.key"
                :class="{ on: ui.themeMode === m.key }"
                @click="setThemeMode(m.key)"
              >
                {{ t(m.label) }}
              </button>
            </div>
          </div>

          <div class="card">
            <div class="card-text">
              <strong>{{ t("主题色") }}</strong>
              <span>{{ t("按钮、选中、进度条和玻璃边缘的颜色，浅色和深色都适用。") }}</span>
            </div>
            <div class="swatches" role="radiogroup" :aria-label="t('主题色')">
              <button
                v-for="s in skins"
                :key="s.key"
                class="swatch"
                :class="{ on: ui.skin === s.key }"
                role="radio"
                :aria-checked="ui.skin === s.key"
                :data-skin-swatch="s.key"
                @click="setSkin(s.key)"
              >
                <i><Icon v-if="ui.skin === s.key" name="check" :size="14" /></i>
                {{ s.label }}
              </button>
            </div>
          </div>

          <div class="card">
            <div class="card-text">
              <strong>{{ t("壁纸") }}</strong>
              <span v-if="ui.wallpaper">{{ t("正在用你选的图片。玻璃上的字看不清时，可以打开下面的「降低透明度」。") }}</span>
              <span v-else>{{ t("内置的雪山湖景，随浅色、深色换明暗；也可以换成电脑里的一张图片。") }}</span>
            </div>
            <div class="actions">
              <img class="wall-preview" :src="wallpaperSrc" :alt="t('当前壁纸')" />
              <button v-if="ui.wallpaper" class="pill" @click="clearWallpaper">{{ t("恢复默认") }}</button>
              <button class="pill" @click="pickWallpaper">{{ t("选择图片…") }}</button>
            </div>
          </div>

          <div class="card">
            <div class="card-text">
              <strong>{{ t("降低透明度") }}</strong>
              <span>{{ t("玻璃改为实色表面：壁纸再亮也不影响文字对比，模糊运算也一并省掉。") }}</span>
            </div>
            <Toggle :model-value="ui.reduceTransparency" :label="t('降低透明度')" @update:model-value="setReduceTransparency" />
          </div>

          <div class="card">
            <div class="card-text">
              <strong>{{ t("侧栏只显示图标") }}</strong>
              <span>{{ t("导航收成一列图标，把宽度让给内容；也可以按 {shortcut} 或侧栏顶部的按钮切换。窗口窄于 1080px 时总是这样显示。", { shortcut: `${modKey}\\` }) }}</span>
            </div>
            <Toggle :model-value="ui.navCompact" :label="t('侧栏只显示图标')" @update:model-value="setNavCompact" />
          </div>

          <div class="card">
            <div class="card-text">
              <strong>{{ t("文件视图") }}</strong>
              <span>{{ t("文件页默认以网格或列表展示。") }}</span>
            </div>
            <div v-slide class="seg glass-well" role="radiogroup" :aria-label="t('文件视图')">
              <button role="radio" :aria-checked="ui.layout === 'grid'" :class="{ on: ui.layout === 'grid' }" @click="setLayout('grid')"><Icon name="grid" :size="16" />{{ t("网格") }}</button>
              <button role="radio" :aria-checked="ui.layout === 'list'" :class="{ on: ui.layout === 'list' }" @click="setLayout('list')"><Icon name="list" :size="16" />{{ t("列表") }}</button>
            </div>
          </div>

          <div class="card">
            <div class="card-text">
              <strong>{{ t("排序") }}</strong>
              <span>{{ t("打开文件夹时的默认顺序，下次启动仍然沿用。") }}</span>
            </div>
            <div v-slide class="seg glass-well" role="radiogroup" :aria-label="t('排序')">
              <button v-for="s in sorts" :key="s.key" role="radio" :aria-checked="ui.sortKey === s.key" :class="{ on: ui.sortKey === s.key }" @click="ui.sortKey = s.key">{{ s.label }}</button>
            </div>
          </div>

          <div class="card">
            <div class="card-text">
              <strong>{{ t("本地索引") }}</strong>
              <span>{{ t("全盘搜索、排序和筛选都从本地索引读取，后台每两分钟同步一次。当前：{state}", { state: indexText(drive.indexProgress) }) }}</span>
            </div>
            <button class="pill" :disabled="drive.indexProgress?.phase === 'full'" @click="rebuildIndex">
              <Icon name="restore" :size="16" />
              {{ t("重建索引") }}
            </button>
          </div>
        </template>

        <!-- ---------- Notifications (System notifications) ---------- -->
        <template v-else-if="tab === 'notify'">
          <h2>{{ t("通知") }}</h2>
          <p class="lead">{{ onWindows ? t("PKP 在后台、或窗口关到系统托盘时，用系统通知告诉你传输和任务的结果。") : t("PKP 在后台、或窗口关到菜单栏时，用系统通知告诉你传输和任务的结果。") }}</p>

          <div class="card">
            <div class="card-text">
              <strong>{{ t("系统通知") }}</strong>
              <span>{{ noticeLine }}</span>
            </div>
            <Toggle
              :model-value="!!notices?.enabled"
              :label="t('系统通知')"
              :disabled="!notices || systemNotices.saving"
              @update:model-value="setNotifyEnabled"
            />
          </div>

          <div v-if="notices?.available && notices.enabled && !notices.allowed" class="card">
            <div class="card-text">
              <strong>{{ t("允许 PKP 发送通知") }}</strong>
              <span>{{ notifyAllowHint }}</span>
            </div>
            <div class="actions">
              <button class="pill" :disabled="systemNotices.saving" @click="allowNotifications">{{ t("允许通知") }}</button>
              <button class="pill" @click="openNotifySettings">{{ onWindows ? t("打开 Windows 设置") : t("打开系统设置") }}</button>
            </div>
          </div>

          <div class="card">
            <div class="card-text">
              <strong>{{ t("PKP 在前台时也通知") }}</strong>
              <span>{{
                onWindows
                  ? t("默认只在 PKP 不在最前面、或窗口已关到系统托盘时通知：PKP 在前台时，页面上已经能看到结果。")
                  : t("默认只在 PKP 不在最前面、或窗口已关到菜单栏时通知：PKP 在前台时，页面上已经能看到结果。")
              }}</span>
            </div>
            <Toggle
              :model-value="!!notices?.foreground"
              :label="t('PKP 在前台时也通知')"
              :disabled="!notices?.enabled || systemNotices.saving"
              @update:model-value="setNotifyForeground"
            />
          </div>

          <div class="card">
            <div class="card-text">
              <strong>{{ t("会通知的事") }}</strong>
              <span>
                {{ t("下载到电脑和本地上传：有文件失败时马上通知，全部完成后合成一条；云添加完成或失败；整理、撤销、删除与还原结束；账号登录失效、需要重新登录。点通知回到 PKP 对应的页面。应用锁锁定时，通知不显示文件名和账号。") }}
              </span>
            </div>
          </div>

          <div class="card">
            <div class="card-text">
              <strong>{{ t("测试") }}</strong>
              <span>{{ systemNotices.note || t("发一条通知，看看它在这台电脑上的样子。") }}</span>
            </div>
            <div class="actions">
              <button class="pill" :disabled="!notices?.available || !notices.enabled" @click="sendTestNotice">{{ t("发送测试通知") }}</button>
              <button class="pill" :disabled="!notices?.available" @click="openNotifySettings">{{ t("系统通知设置") }}</button>
            </div>
          </div>
        </template>

        <!-- ---------- Privacy & security ---------- -->
        <PrivacySettings v-else-if="tab === 'privacy'" />

        <!-- ---------- Keyboard shortcuts ---------- -->
        <ShortcutsSettings v-else-if="tab === 'keys'" />

        <!-- ---------- Account management ---------- -->
        <template v-else-if="tab === 'accounts'">
          <h2>{{ t("账号管理") }}</h2>
          <p class="lead">{{ t("一个 PKP，各有归属的收藏空间") }}</p>

          <div class="section-head">
            <h3 class="group">
              {{ t("已添加账号") }}
              <span class="count" :aria-label="t('{n} 个账号', { n: accounts.list.length })">{{ accounts.list.length }}</span>
            </h3>
            <button class="primary add" @click="openSignIn('')">
              <Icon name="plus" :size="17" />
              {{ t("添加账号") }}
            </button>
          </div>
          <p class="scope">{{ t("备注仅保存在本机，方便区分主号、备用号与不同内容。") }}</p>

          <!-- No whole-row click: each verb is its own button. -->
          <ul class="account-list" :aria-label="t('已添加账号')">
            <li v-for="a in accounts.list" :key="a.id" class="account-row">
              <AccountIdentity :account="a" class="who">
                <MembershipBadge v-if="!a.needsPassword" :account="a.id" class="membership" />
              </AccountIdentity>
              <div class="row-side">
                <span class="row-state" :class="accountState(a).tone">{{ accountState(a).text }}</span>
                <!-- Before sending to AI, per data level. -->
                <label v-for="level in CONSENT_LEVELS" :key="level.key" class="row-consent" :title="t('「{account}」发送{level}给 AI 前', { account: accountLabel(a), level: t(level.short) })">
                  <span>{{ t(level.short) }}</span>
                  <span class="select mini glass-well">
                    <SelectMenu
                      class="face"
                      :model-value="a.aiConsent ? 'always' : 'ask'"
                      :options="CONSENT_OPTIONS"
                      :label="t('「{account}」发送{level}给 AI 前', { account: accountLabel(a), level: t(level.short) })"
                      @update:model-value="setAIConsent(a.id, level.key, $event === 'always')"
                    />
                    <Icon name="chevronDown" :size="14" />
                  </span>
                </label>
                <div class="row-actions">
                  <button
                    v-if="!a.needsPassword && a.id !== currentId"
                    class="pill"
                    :disabled="!!accounts.switching"
                    :aria-label="t('切换到「{account}」', { account: accountLabel(a) })"
                    @click="switchAccount(a.id)"
                  >
                    {{ t("切换") }}
                  </button>
                  <button v-if="a.needsPassword" class="pill" :aria-label="t('重新登录「{account}」', { account: accountLabel(a) })" @click="openSignIn(a.id)">
                    {{ t("重新登录") }}
                  </button>
                  <button
                    class="pill"
                    :aria-label="t('修改「{account}」的备注', { account: accountLabel(a) })"
                    @click="accounts.dialog = { kind: 'rename', account: a.id }"
                  >
                    {{ t("修改备注") }}
                  </button>
                  <button
                    class="pill danger"
                    :aria-label="t('退出并移除「{account}」', { account: accountLabel(a) })"
                    @click="accounts.dialog = { kind: 'remove', account: a.id }"
                  >
                    {{ t("退出并移除") }}
                  </button>
                </div>
              </div>
            </li>
          </ul>

          <h3 class="group">{{ t("播放偏好") }}</h3>
          <div class="card">
            <div class="card-text">
              <strong>{{ t("切换账号时继续播放") }}</strong>
              <span>{{ t("关闭时暂停并保存进度；开启后，播放器会始终显示视频所属账号。") }}</span>
            </div>
            <span class="toggle-line">
              <Toggle :model-value="accounts.keepPlaying" :label="t('切换账号时继续播放')" @update:model-value="setKeepPlaying" />
              <span class="toggle-state" aria-hidden="true">{{ accounts.keepPlaying ? t("已开启") : t("已关闭") }}</span>
            </span>
          </div>

          <p class="info-note">{{ t("切换账号不会停止后台下载、上传或云添加。退出并移除账号前，会说明本机任务的处理方式。") }}</p>
        </template>

        <!-- ---------- Transfers ---------- -->
        <template v-else-if="tab === 'transfer'">
          <h2>{{ t("传输") }}</h2>
          <p class="lead">{{ t("决定云添加的文件怎样存进云盘、本地上传中断后怎样继续、下载到电脑存到哪里，并查看本月的传输配额。") }}</p>

          <h3 class="group">{{ t("云添加") }}</h3>
          <p class="scope">{{ t("以下偏好保存在账号里，对所有 PikPak 客户端生效。") }}</p>

          <div v-if="!prefs" class="late loading">
            <Orbit :size="18" :label="t('正在读取')" />
            {{ t("正在读取传输偏好…") }}
          </div>
          <template v-else>
            <div class="card">
              <div class="card-text">
                <strong>{{ t("分文件夹保存来自其他 App 的文件") }}</strong>
                <span>{{ t("例如从 Telegram 添加的文件，传输完成后存进「My Telegram」文件夹。") }}</span>
              </div>
              <Toggle
                :model-value="prefs.separateAppFolders"
                :label="t('分文件夹保存来自其他 App 的文件')"
                @update:model-value="setTransferPreference('separateAppFolders', $event)"
              />
            </div>

            <div class="card">
              <div class="card-text">
                <strong>{{ t("BT 和磁力链的子文件过滤") }}</strong>
                <span>{{ prefs.smartFilter ? t("识别为广告的文件不会存进云盘。") : t("任务里的全部文件都会存进云盘。") }}</span>
              </div>
              <div v-slide class="seg glass-well" role="radiogroup" :aria-label="t('子文件过滤')">
                <button role="radio" :aria-checked="!prefs.smartFilter" :class="{ on: !prefs.smartFilter }" @click="setTransferPreference('smartFilter', false)">{{ t("filter|关闭") }}</button>
                <button role="radio" :aria-checked="prefs.smartFilter" :class="{ on: prefs.smartFilter }" @click="setTransferPreference('smartFilter', true)">{{ t("智能过滤") }}</button>
              </div>
            </div>

            <div class="card">
              <div class="card-text">
                <strong>{{ t("添加 Torrent 文件时自动解析") }}</strong>
                <span>{{ t("添加种子文件后直接传输其中的文件，不再停下来等你挑选。") }}</span>
              </div>
              <Toggle
                :model-value="prefs.autoParseTorrent"
                :label="t('添加 Torrent 文件时自动解析')"
                @update:model-value="setTransferPreference('autoParseTorrent', $event)"
              />
            </div>
          </template>

          <h3 class="group">{{ t("本地上传") }}</h3>
          <p class="scope">{{ t("以下设置只影响这台电脑。") }}</p>

          <template v-if="connections.local">
            <div class="card">
              <div class="card-text">
                <strong>{{ t("同时上传的文件数") }}</strong>
                <span>{{ t("每个文件另有 4 路分片同时传。调高更容易占满上行带宽，网络不稳时调低更可靠。正在传的不受影响，排队的按新数量开始。") }}</span>
              </div>
              <div v-slide class="seg glass-well" role="radiogroup" :aria-label="t('同时上传的文件数')">
                <button
                  v-for="n in PARALLEL_UPLOAD_CHOICES"
                  :key="n"
                  role="radio"
                  :aria-checked="connections.local.parallelUploads === n"
                  :class="{ on: connections.local.parallelUploads === n }"
                  @click="setParallelUploads(n)"
                >
                  {{ n }}
                </button>
              </div>
            </div>

            <div class="card">
              <div class="card-text">
                <strong>{{ t("断点续传") }}</strong>
                <span>{{ t("上传失败或退出应用后，点「继续」从已上传的部分接着上传。上传凭证 12 小时后过期，过期了就重新申请、从头开始。关闭后，没上传完的上传不保留进度，重试都从头开始。") }}</span>
              </div>
              <Toggle :model-value="connections.local.resumableUploads" :label="t('断点续传')" @update:model-value="setResumableUploads" />
            </div>
          </template>

          <h3 class="group">{{ t("下载到电脑") }}</h3>
          <p class="scope">{{ t("以下设置只影响这台电脑。") }}</p>

          <template v-if="connections.local">
            <div class="card">
              <div class="card-text">
                <strong>{{ t("下载位置") }}</strong>
                <span class="mono">{{ connections.local.downloadDir }}</span>
              </div>
              <div class="actions">
                <button class="pill" @click="pickDownloadDir">{{ t("更改…") }}</button>
                <button class="pill" @click="revealDownloadDir">{{ t("打开") }}</button>
              </div>
            </div>

            <div class="card">
              <div class="card-text">
                <strong>{{ t("同时下载的文件数") }}</strong>
                <span>{{ t("同一账号的下载合计最多 16 个连接，超出的排队等待。正在下载的不受影响，排队的按新数量开始。") }}</span>
              </div>
              <div v-slide class="seg glass-well" role="radiogroup" :aria-label="t('同时下载的文件数')">
                <button
                  v-for="n in PARALLEL_DOWNLOAD_CHOICES"
                  :key="n"
                  role="radio"
                  :aria-checked="connections.local.parallelDownloads === n"
                  :class="{ on: connections.local.parallelDownloads === n }"
                  @click="setParallelDownloads(n)"
                >
                  {{ n }}
                </button>
              </div>
            </div>

            <div class="card">
              <div class="card-text">
                <strong>{{ t("每个文件的线程数") }}</strong>
                <span>{{ t("把一个文件分成几段同时下载。PikPak 对单个下载链接最多允许 8 个连接。只影响之后开始的文件。") }}</span>
              </div>
              <div v-slide class="seg glass-well" role="radiogroup" :aria-label="t('每个文件的线程数')">
                <button
                  v-for="n in DOWNLOAD_THREAD_CHOICES"
                  :key="n"
                  role="radio"
                  :aria-checked="connections.local.downloadThreads === n"
                  :class="{ on: connections.local.downloadThreads === n }"
                  @click="setDownloadThreads(n)"
                >
                  {{ n }}
                </button>
              </div>
            </div>
          </template>

          <h3 class="group">{{ t("传输配额") }}</h3>
          <p class="scope">{{ t("按自然月计算，每月 1 日 00:00（UTC+8）重置。") }}</p>

          <div v-if="!quota" class="late loading">
            <Orbit :size="18" :label="t('正在读取')" />
            {{ t("正在读取传输配额…") }}
          </div>
          <template v-else>
            <div class="card meters">
              <UsageMeter :label="t('云添加')" :usage="quota.base.cloudDownload" />
              <UsageMeter :label="t('下行')" :usage="quota.base.downstream" />
              <UsageMeter :label="t('上传')" :usage="quota.base.upload" />
              <UsageMeter v-if="quota.base.dailyDownstream.limit > 0" :label="t('每日下行')" :usage="quota.base.dailyDownstream" />
            </div>
            <div v-if="extra" class="card meters">
              <strong class="meters-title">{{ t("额外传输配额") }}</strong>
              <UsageMeter v-if="extra.cloudDownload.limit" :label="t('云添加')" :usage="extra.cloudDownload" />
              <UsageMeter v-if="extra.downstream.limit" :label="t('下行')" :usage="extra.downstream" />
              <UsageMeter v-if="extra.upload.limit" :label="t('上传')" :usage="extra.upload" />
            </div>
            <button class="link" @click="showAppsUsage">
              {{ t("已连接应用共享其中 25% · 查看各应用用量") }}
              <Icon name="chevronRight" :size="15" />
            </button>
          </template>
        </template>

        <!-- ---------- AI (Configure AI) ---------- -->
        <template v-else-if="tab === 'ai'">
          <p class="eyebrow">{{ t("设置 · AI") }}</p>
          <h2>{{ t("模型和发送范围，由你决定。") }}</h2>
          <p class="lead">{{ t("整理建议、本地搜索和入库建议都不需要模型。连接你自己的模型后，才能问 AI；API Key 存在本机{store}。", { store: keychainName }) }}</p>

          <div v-if="!ai" class="late loading">
            <Orbit :size="18" :label="t('正在读取')" />
            {{ t("正在读取 AI 设置…") }}
          </div>
          <div v-else class="ai-grid">
            <section class="card form conn" aria-labelledby="ai-conn">
              <header class="form-head">
                <strong id="ai-conn">{{ t("模型连接") }}</strong>
                <span class="state" :class="ai.configured ? 'ok' : 'off'"><i />{{ ai.configured ? t("已配置") : t("未配置") }}</span>
              </header>

              <label class="field">
                <span>{{ t("提供商预设") }}</span>
                <span class="select glass-well">
                  <SelectMenu
                    class="face"
                    :model-value="preset.id"
                    :options="PRESET_OPTIONS"
                    :label="t('提供商预设')"
                    :disabled="aiSettings.saving"
                    @update:model-value="choosePreset"
                  />
                  <Icon name="chevronDown" :size="16" />
                </span>
              </label>
              <label v-if="conn.provider === 'openai'" class="field">
                <span>Base URL</span>
                <SuggestMenu :options="BASE_URL_OPTIONS" label="Base URL">
                  <input
                    v-model="conn.baseUrl"
                    type="url"
                    spellcheck="false"
                    autocomplete="off"
                    placeholder="https://api.openai.com/v1"
                    @change="saveConnection"
                    @keydown.enter="($event.target as HTMLInputElement).blur()"
                  />
                </SuggestMenu>
              </label>
              <p class="hint">{{ t("更换提供商或地址后需要重新填写 API Key") }}</p>

              <div class="field">
                <div class="model-label">
                  <label for="ai-model">{{ t("模型名") }}</label>
                  <button class="pill" :disabled="modelList.loading || !canFetchModels" @click="refreshModels">
                    <Orbit v-if="modelList.loading" :size="14" :label="t('正在获取模型…')" />
                    <Icon v-else name="restore" :size="14" />
                    {{ t("刷新模型") }}
                  </button>
                </div>
                <SuggestMenu :options="MODEL_OPTIONS" :label="t('模型名')">
                  <input
                    id="ai-model"
                    v-model="conn.model"
                    spellcheck="false"
                    autocomplete="off"
                    :placeholder="presetOf(conn).model"
                    @change="saveConnection"
                    @keydown.enter="($event.target as HTMLInputElement).blur()"
                  />
                </SuggestMenu>
              </div>
              <p class="hint" :class="{ bad: sameModelConnection && modelList.error }" role="status">{{ modelHint }}</p>

              <div class="field">
                <span id="ai-key">API Key</span>
                <template v-if="!ai.keychain">
                  <input type="password" disabled aria-labelledby="ai-key" :placeholder="t('无法保存')" />
                  <p class="hint bad">{{ t("这台电脑没有可用的{store}，无法保存 API Key", { store: keychainName }) }}</p>
                </template>
                <div v-else-if="ai.keyHint && !keyEditing" class="key-line">
                  <span class="key-hint">{{ ai.keyHint }}</span>
                  <button class="pill" :disabled="aiSettings.saving" @click="editKey">{{ t("更换") }}</button>
                  <button class="pill danger" :disabled="aiSettings.saving" @click="saveAIKey('')">{{ t("清除") }}</button>
                </div>
                <form v-else class="key-line" @submit.prevent="saveKey">
                  <input
                    ref="keyField"
                    type="password"
                    autocomplete="off"
                    spellcheck="false"
                    aria-labelledby="ai-key"
                    :placeholder="t('粘贴 API Key')"
                    @input="keyTyped = !!($event.target as HTMLInputElement).value.trim()"
                    @keydown.esc="ai.keyHint && cancelKey()"
                  />
                  <button type="submit" class="pill primary" :disabled="!keyTyped || aiSettings.saving">{{ t("保存") }}</button>
                  <button v-if="ai.keyHint" type="button" class="pill" @click="cancelKey">{{ t("取消") }}</button>
                </form>
                <p v-if="preset.keyless" class="hint">{{ t("本机服务（如 Ollama）不需要 API Key，留空即可。") }}</p>
              </div>

              <div class="test-line">
                <button class="pill" :class="{ working: aiSettings.testing }" :disabled="aiSettings.testing" @click="testAIConnection">
                  <Orbit v-if="aiSettings.testing" :size="16" :label="t('正在测试')" />
                  {{ t("测试连接") }}
                </button>
                <p v-if="aiSettings.test" class="test-result" :class="aiSettings.test.ok ? 'ok' : 'bad'" role="status">
                  <strong>{{ testText }}</strong>
                  <span v-if="aiSettings.test.message">{{ aiSettings.test.message }}</span>
                </p>
              </div>
              <!-- What the service was found to do; a conversation adapts to it. -->
              <dl v-if="capabilities" class="caps">
                <div>
                  <dt>{{ t("工具调用") }}</dt>
                  <dd :class="capabilities.tools">{{ TOOLS_TEXT[capabilities.tools] ? t(TOOLS_TEXT[capabilities.tools]) : t("未测试") }}</dd>
                </div>
                <div>
                  <dt>{{ t("流式输出") }}</dt>
                  <dd :class="capabilities.stream">{{ STREAM_TEXT[capabilities.stream] ? t(STREAM_TEXT[capabilities.stream]) : t("未测试") }}</dd>
                </div>
              </dl>
            </section>

            <div class="ai-side">
              <section class="card form" aria-labelledby="ai-limit">
                <header class="form-head">
                  <strong id="ai-limit">{{ t("单个对话上限") }}</strong>
                  <span class="toggle-line">
                    <Toggle :model-value="unlimited" :label="t('不限制')" :disabled="aiSettings.saving" @update:model-value="setUnlimited" />
                    <span class="toggle-state" aria-hidden="true">{{ t("不限制") }}</span>
                  </span>
                </header>
                <label class="field">
                  <span>{{ t("Token 上限") }}</span>
                  <span class="unit-input">
                    <input
                      :value="limitDraft"
                      type="number"
                      inputmode="numeric"
                      min="1000"
                      max="2000000"
                      step="1000"
                      :aria-invalid="!!limitError"
                      :disabled="unlimited"
                      @input="limitDraft = ($event.target as HTMLInputElement).value"
                      @change="saveLimit"
                      @keydown.enter="($event.target as HTMLInputElement).blur()"
                    />
                    <em>tokens</em>
                  </span>
                </label>
                <p class="hint" :class="{ bad: !!limitError }">
                  {{
                    limitError ||
                    (unlimited
                      ? t("不限制：对话不会因为用量停下，授权卡片上仍会显示每轮的预计用量；费用按你的模型服务实际计费。")
                      : t("一段对话累计达到上限后停止请求，保留已经得到的结果。"))
                  }}
                </p>
              </section>

              <section class="card form" aria-labelledby="ai-consent">
                <header class="form-head">
                  <strong id="ai-consent">{{ currentAccount ? t("{account} · 发送给 AI 前", { account: accountLabel(currentAccount) }) : t("发送给 AI 前") }}</strong>
                </header>
                <template v-if="currentAccount">
                  <label v-for="level in CONSENT_LEVELS" :key="level.key" class="field">
                    <span>{{ t(level.label) }}</span>
                    <span class="select glass-well">
                      <SelectMenu
                        class="face"
                        :model-value="currentAccount.aiConsent ? 'always' : 'ask'"
                        :options="CONSENT_OPTIONS"
                        :label="t(level.label)"
                        @update:model-value="setAIConsent(currentId, level.key, $event === 'always')"
                      />
                      <Icon name="chevronDown" :size="16" />
                    </span>
                  </label>
                </template>
                <p class="hint shield-note">
                  <Icon name="shield" :size="15" />
                  <span>{{ t("元数据选「始终允许」不包括字幕；两个级别按账号分别设置。改文件仍需要逐项预览和确认。") }}</span>
                </p>
              </section>
            </div>

            <section class="card form wide" aria-labelledby="ai-naming">
              <header class="form-head">
                <span class="card-text">
                  <strong id="ai-naming">{{ t("影视命名模板") }}</strong>
                  <span>{{ t("剧集与电影分别设定路径。变量与可选段实时预览。") }}</span>
                </span>
                <span class="select glass-well naming-pick">
                  <SelectMenu
                    class="face"
                    :model-value="style"
                    :options="[
                      { value: 'plex', label: 'Plex / Infuse' },
                      { value: 'chinese', label: t('中文') },
                      { value: 'custom', label: t('自定义') },
                    ]"
                    :label="t('影视命名模板')"
                    :disabled="aiSettings.saving"
                    @update:model-value="chooseStyle($event as NamingStyle)"
                  />
                  <Icon name="chevronDown" :size="16" />
                </span>
              </header>

              <template v-if="style === 'custom'">
                <div class="templates">
                  <label class="field">
                    <span>{{ t("剧集模板") }}</span>
                    <input v-model="custom.series" spellcheck="false" autocomplete="off" :aria-invalid="!!seriesError" />
                    <small v-if="seriesError" class="bad">{{ seriesError }}</small>
                  </label>
                  <label class="field">
                    <span>{{ t("电影模板") }}</span>
                    <input v-model="custom.movie" spellcheck="false" autocomplete="off" :aria-invalid="!!movieError" />
                    <small v-if="movieError" class="bad">{{ movieError }}</small>
                  </label>
                </div>
                <p class="hint">{{ t("可用 {vars}；/ 分隔文件夹层级，<…> 内缺值时整段省略，{pad} 补零。", { vars: "{title} {year} {season} {episode} {version}", pad: "{season:02}" }) }}</p>
              </template>

              <dl class="previews">
                <div>
                  <dt>{{ t("剧集预览") }}</dt>
                  <dd>{{ previews.series || "—" }}</dd>
                </div>
                <div>
                  <dt>{{ t("电影预览") }}</dt>
                  <dd>{{ previews.movie || "—" }}</dd>
                </div>
              </dl>

              <div v-if="style === 'custom'" class="naming-save">
                <button class="pill primary" :disabled="!!seriesError || !!movieError || !customDirty || aiSettings.saving" @click="saveCustom">{{ t("保存模板") }}</button>
              </div>
            </section>

            <section class="card form wide" aria-labelledby="ai-tmdb">
              <header class="form-head">
                <strong id="ai-tmdb">{{ t("TMDB 精确匹配") }}</strong>
                <span v-if="ts" class="state" :class="ts.tmdbReady ? 'ok' : 'off'"><i />{{ tmdbState }}</span>
              </header>
              <p v-if="!ts" class="hint" :class="{ bad: !!tmdb.error }">
                {{ tmdb.error ? t("读取设置失败：{error}", { error: tmdb.error }) : t("正在读取") }}
              </p>
              <template v-else>
                <p class="hint">
                  {{ t("填写你自己的 TMDB API Key（v3）或 API 读访问令牌（v4），可在 themoviedb.org 的「设置 · API」页申请。只用于影视整理时核对片名和年份；不填时，影视整理只按规则和模型识别。") }}
                </p>

                <div class="field">
                  <span id="tmdb-key">{{ t("TMDB 凭据") }}</span>
                  <template v-if="!ts.keychain">
                    <input type="password" disabled aria-labelledby="tmdb-key" :placeholder="t('无法保存')" />
                    <p class="hint bad">{{ t("这台电脑没有可用的{store}，无法保存 TMDB 凭据", { store: keychainName }) }}</p>
                  </template>
                  <div v-else-if="ts.tmdbKeyHint && !tmdbEditing" class="key-line">
                    <span class="key-hint">{{ ts.tmdbKeyHint }}</span>
                    <button class="pill" :disabled="tmdb.saving" @click="editTmdb">{{ t("更换") }}</button>
                    <button class="pill danger" :disabled="tmdb.saving" @click="saveTMDBKey('')">{{ t("清除") }}</button>
                  </div>
                  <form v-else class="key-line" @submit.prevent="saveTmdb">
                    <input
                      ref="tmdbField"
                      type="password"
                      autocomplete="off"
                      spellcheck="false"
                      aria-labelledby="tmdb-key"
                      :placeholder="t('粘贴 API Key 或读访问令牌')"
                      @input="tmdbTyped = !!($event.target as HTMLInputElement).value.trim()"
                      @keydown.esc="ts.tmdbKeyHint && cancelTmdb()"
                    />
                    <button type="submit" class="pill primary" :disabled="!tmdbTyped || tmdb.saving">{{ t("保存") }}</button>
                    <button v-if="ts.tmdbKeyHint" type="button" class="pill" @click="cancelTmdb">{{ t("取消") }}</button>
                  </form>
                </div>

                <div class="test-line">
                  <button class="pill" :class="{ working: tmdb.testing }" :disabled="tmdb.testing || !ts.tmdbReady" @click="testTMDB">
                    <Orbit v-if="tmdb.testing" :size="16" :label="t('正在测试')" />
                    {{ t("测试连接") }}
                  </button>
                  <p v-if="tmdb.test" class="test-result" :class="tmdb.test.ok ? 'ok' : 'bad'" role="status">
                    <strong>{{ tmdbTestText }}</strong>
                    <span v-if="tmdb.test.message">{{ tmdb.test.message }}</span>
                  </p>
                </div>
              </template>

              <div class="credit">
                <button class="logo-link" :title="t('在浏览器中打开 themoviedb.org')" @click="openTMDBSite">
                  <img class="tmdb-logo" :src="tmdbLogo" alt="TMDB" />
                </button>
                <p>
                  This product uses the TMDB API but is not endorsed or certified by TMDB.
                  <span v-if="i18n.locale !== 'en'">{{ t("本产品使用 TMDB API，但未获 TMDB 认可或认证。") }}</span>
                </p>
              </div>
            </section>
          </div>
        </template>

        <!-- ---------- Network & proxy ---------- -->
        <template v-else-if="tab === 'network'">
          <p class="eyebrow">NETWORK · PKP</p>
          <div class="net-head">
            <h2>{{ t("网络与代理") }}</h2>
            <span class="scope-pill">{{ t("本机生效") }}</span>
          </div>
          <p class="lead">{{ t("选择 PKP 的联网方式，不会更改电脑上的 VPN 或系统代理。") }}</p>
          <ConnectivityPanel />

          <div v-if="!px" class="late loading">
            <Orbit :size="18" :label="t('正在读取')" />
            {{ t("正在读取网络设置…") }}
          </div>
          <template v-else>
            <div class="section-label">
              <h3 class="group">{{ t("连接方式") }}</h3>
              <span class="scope">{{ t("选择一种方式") }}</span>
            </div>
            <div class="mode-grid" role="radiogroup" :aria-label="t('连接方式')">
              <button
                v-for="(m, i) in PROXY_MODES"
                :key="m.key"
                ref="proxyModeButtons"
                class="mode-card"
                role="radio"
                :class="{ on: proxy.mode === m.key }"
                :aria-checked="proxy.mode === m.key"
                :tabindex="proxy.mode === m.key ? 0 : -1"
                @click="chooseProxyMode(m.key)"
                @keydown="onProxyModeKey($event, i)"
              >
                <span class="mode-top">
                  <Icon :name="m.icon" :size="20" />
                  <i class="dot" />
                </span>
                <strong>{{ m.label }}</strong>
                <small>{{ m.desc }}</small>
              </button>
            </div>

            <div class="ai-grid net-grid">
              <section class="card form" aria-labelledby="proxy-config">
                <header class="form-head">
                  <strong id="proxy-config">{{ proxy.mode === "custom" ? t("代理服务器") : t("这种方式的说明") }}</strong>
                  <span v-if="proxyDirty" class="draft-badge">{{ t("未应用的更改") }}</span>
                </header>

                <template v-if="proxy.mode === 'custom'">
                  <p class="hint">{{ t("填写代理服务提供的地址和端口。只有 PKP 自己的请求走它，电脑上其他应用不受影响。") }}</p>
                  <label class="field">
                    <span>{{ t("类型") }}</span>
                    <span class="select glass-well">
                      <SelectMenu v-model="proxy.scheme" class="face" :options="PROXY_SCHEMES" :label="t('代理类型')" :disabled="proxySettings.saving" />
                      <Icon name="chevronDown" :size="16" />
                    </span>
                  </label>
                  <div class="host-row">
                    <label class="field">
                      <span>{{ t("服务器地址") }}</span>
                      <input
                        v-model="proxy.host"
                        spellcheck="false"
                        autocomplete="off"
                        :placeholder="t('例如 {example}', { example: '127.0.0.1' })"
                        :disabled="proxySettings.saving"
                        :aria-invalid="!!proxyFieldError"
                        :aria-describedby="proxyFieldError ? 'proxy-field-error' : undefined"
                        @keydown.enter="($event.target as HTMLInputElement).blur()"
                      />
                    </label>
                    <label class="field">
                      <span>{{ t("端口") }}</span>
                      <input
                        v-model="proxy.port"
                        inputmode="numeric"
                        autocomplete="off"
                        placeholder="1–65535"
                        :disabled="proxySettings.saving"
                        :aria-invalid="!!proxyFieldError"
                        :aria-describedby="proxyFieldError ? 'proxy-field-error' : undefined"
                        @keydown.enter="($event.target as HTMLInputElement).blur()"
                      />
                    </label>
                  </div>
                  <p v-if="proxyFieldError" id="proxy-field-error" class="hint bad" role="alert">{{ proxyFieldError }}</p>

                  <div class="toggle-row">
                    <span class="card-text">
                      <strong>{{ t("需要账号密码") }}</strong>
                      <span>{{ t("仅在代理服务器要求时填写。") }}</span>
                    </span>
                    <Toggle v-model="proxy.auth" :label="t('需要账号密码')" :disabled="proxySettings.saving" />
                  </div>

                  <template v-if="proxy.auth">
                    <label class="field">
                      <span>{{ t("用户名") }}</span>
                      <input
                        v-model="proxy.username"
                        spellcheck="false"
                        autocomplete="off"
                        :placeholder="t('代理用户名')"
                        :disabled="proxySettings.saving"
                        @keydown.enter="($event.target as HTMLInputElement).blur()"
                      />
                    </label>
                    <div class="field">
                      <span id="proxy-pw">{{ t("密码") }}</span>
                      <template v-if="!px.keychain">
                        <input type="password" disabled aria-labelledby="proxy-pw" :placeholder="t('无法保存')" />
                        <p class="hint bad">{{ t("这台电脑没有可用的{store}，无法保存代理密码", { store: keychainName }) }}</p>
                      </template>
                      <template v-else>
                        <span class="pw-field">
                          <input
                            v-model="proxyPassword"
                            :type="proxyPasswordShown ? 'text' : 'password'"
                            autocomplete="new-password"
                            spellcheck="false"
                            aria-labelledby="proxy-pw"
                            :placeholder="px.passwordHint ? t('沿用已保存的密码（{hint}）', { hint: px.passwordHint }) : t('代理密码')"
                            :disabled="proxySettings.saving"
                          />
                          <button
                            type="button"
                            class="pw-eye"
                            :aria-label="proxyPasswordShown ? t('隐藏密码') : t('显示密码')"
                            @click="proxyPasswordShown = !proxyPasswordShown"
                          >
                            <Icon :name="proxyPasswordShown ? 'eyeOff' : 'eye'" :size="17" />
                          </button>
                        </span>
                        <p class="hint">
                          {{ t("密码保存在本机{store}，不写进配置文件。", { store: keychainName }) }}
                          <button v-if="px.passwordHint" class="pill mini-pill danger" :disabled="proxySettings.saving" @click="forgetProxyPassword">
                            {{ t("清除已保存的密码") }}
                          </button>
                        </p>
                      </template>
                    </div>
                  </template>
                </template>

                <template v-else-if="proxy.mode === 'system'">
                  <p class="hint">{{ t("使用电脑当前的网络和代理设置；没有设置代理时再看 http_proxy 等环境变量。系统里的 VPN 仍由操作系统管理。") }}</p>
                  <p class="hint">{{ px.system }}</p>
                  <!-- Not red alone: the sentence says it and the sign shows it. -->
                  <p v-if="px.systemPac" class="hint bad icon-note" role="alert">
                    <Icon name="alert" :size="15" />
                    <span>{{ t("电脑用的是 PAC 自动代理脚本，PKP 不执行脚本，这些请求会直接发出。需要确定的路径时请选「自定义代理」。") }}</span>
                  </p>
                </template>

                <template v-else>
                  <p class="hint">{{ t("PKP 不额外指定应用代理。电脑上已有的 VPN 或系统路由仍可能影响实际路径，「直连」不等于绕过 VPN。") }}</p>
                </template>

                <!-- Advanced proxy bypass addresses are shared by custom and
                     system proxy modes; direct mode sends traffic without a proxy. -->
                <template v-if="proxy.mode !== 'direct'">
                  <button class="link advanced" :aria-expanded="proxyAdvanced" aria-controls="proxy-bypass" @click="proxyAdvanced = !proxyAdvanced">
                    {{ t("高级选项") }}
                    <Icon :name="proxyAdvanced ? 'chevronUp' : 'chevronDown'" :size="15" />
                  </button>
                  <label v-if="proxyAdvanced" id="proxy-bypass" class="field">
                    <span>{{ t("不使用代理的地址") }}</span>
                    <textarea v-model="proxy.bypass" rows="2" spellcheck="false" :placeholder="t('例如 {example}', { example: 'localhost, *.example.com' })" :disabled="proxySettings.saving" />
                    <small>
                      {{ t("用逗号、空格或换行分隔；留空表示不设例外。本机地址（127.0.0.1、localhost）始终直连。") }}
                      {{ t("网段写法（如 192.168.0.0/16）只对应用自身的请求生效；网页里的图片请按主机名或 IP 填写。") }}
                    </small>
                  </label>
                </template>
              </section>

              <section class="card form status-card" aria-labelledby="proxy-status">
                <header class="status-head">
                  <span class="status-icon"><Icon name="globe" :size="19" /></span>
                  <span class="card-text">
                    <span>{{ t("连接概况") }}</span>
                    <strong id="proxy-status">{{ proxyStatus.title }}</strong>
                  </span>
                </header>
                <div class="route" aria-hidden="true">
                  <span class="route-node">PKP</span>
                  <i />
                  <span class="route-node mid">{{ proxyRouteNode }}</span>
                  <i />
                  <span class="route-node">{{ t("网络") }}</span>
                </div>
                <dl class="status-rows">
                  <div>
                    <dt>{{ t("当前正在使用") }}</dt>
                    <dd>{{ px.active }}</dd>
                  </div>
                  <div>
                    <dt>{{ t("准备应用") }}</dt>
                    <dd>{{ proxyPending }}</dd>
                  </div>
                </dl>
                <!-- A failure is read out at once; the rest is polite. -->
                <p class="test-state" :class="proxyStatus.tone" :role="proxyStatus.tone === 'bad' ? 'alert' : 'status'">
                  <Orbit v-if="proxySettings.testing" :size="15" :label="t('正在测试')" />
                  <Icon v-else :name="proxyStatus.tone === 'ok' ? 'check' : proxyStatus.tone === 'bad' ? 'alert' : 'info'" :size="15" />
                  <span>{{ proxyStatus.text }}</span>
                </p>
                <p v-if="proxyElapsed" class="hint">{{ proxyElapsed }}</p>
                <p class="hint">{{ t("测试会检查代理连接和 PKP 服务访问。") }}</p>
                <p v-if="px.webview" class="hint">{{ px.webview }}</p>
              </section>
            </div>

            <div class="action-row">
              <p class="hint icon-note save-note">
                <Icon name="info" :size="15" />
                <span>{{ t("设置保存在这台电脑上，切换账号时沿用；应用之后，登录、文件、上传下载、在线播放、AI 与 TMDB 的请求都按这里的方式发出。") }}</span>
              </p>
              <div class="actions">
                <button
                  class="pill"
                  :class="{ working: proxySettings.testing }"
                  :disabled="proxySettings.testing || proxySettings.saving || proxyIncomplete"
                  @click="testProxy(proxyInput)"
                >
                  <Orbit v-if="proxySettings.testing" :size="16" :label="t('正在测试')" />
                  {{ t("测试连接") }}
                </button>
                <!-- Applying mid-test would leave the finished test talking
                     about a draft that is no longer the one on screen. -->
                <button
                  class="pill primary"
                  :disabled="!proxyDirty || proxySettings.saving || proxySettings.testing || proxyIncomplete"
                  @click="applyProxyDraft"
                >
                  {{ proxySettings.saving ? t("正在应用…") : t("应用设置") }}
                </button>
              </div>
            </div>
          </template>
        </template>

        <UpdateSettings v-else-if="tab === 'updates'" />

        <!-- ---------- Connections & integrations ---------- -->
        <template v-else>
          <h2>{{ t("连接与集成") }}</h2>
          <p class="lead">{{ t("连接第三方服务与扩展能力，让你的使用更灵活高效。") }}</p>

          <div v-if="connections.loading && !connections.webdav" class="late loading">
            <Orbit :size="18" :label="t('正在读取')" />
            {{ t("正在读取连接状态…") }}
          </div>

          <div v-for="l in links" :key="l.key" class="card link-card" :class="{ on: panel === l.key }" @click="panel = l.key">
            <span class="badge"><Icon :name="l.icon" :size="24" /></span>
            <span class="card-text">
              <strong>{{ t(l.title) }}</strong>
              <span>{{ t(l.desc) }}</span>
            </span>
            <span class="actions">
              <span v-if="l.key === 'webdav' && webdavState" class="state" :class="webdavState.tone"><i />{{ webdavState.text }}</span>
              <button class="pill go" :aria-expanded="panel === l.key" @click.stop="panel = l.key">
                {{ t(l.action) }}
                <Icon name="chevronRight" :size="16" />
              </button>
            </span>
          </div>
        </template>
      </div>
    </div>
  </section>

  <WebDAVIsland v-if="tab === 'connect' && panel === 'webdav'" @close="panel = ''" />
  <AccessIsland v-else-if="tab === 'connect' && access" :key="access" :kind="access" @close="panel = ''" @open="panel = $event" />
</template>

<style scoped>
.settings {
  /* Named so the rail can fold to icons when an island or a small window
     leaves the canvas narrow. */
  container: settings / inline-size;
  flex: 1;
  min-width: 0;
  display: flex;
  flex-direction: column;
  padding: 26px 28px 12px;
  overflow: hidden;
}

.head h1 {
  margin: 0;
  font-size: 32px;
  font-weight: 600;
  line-height: 40px;
  letter-spacing: 0.01em;
}

.head p {
  margin: 4px 0 22px;
  font-size: 16px;
  color: var(--fg-dim);
}

.cols {
  flex: 1;
  min-height: 0;
  display: flex;
  gap: 26px;
}

.rail {
  width: 200px;
  flex: none;
  display: flex;
  flex-direction: column;
  gap: 6px;
  padding: 6px 12px 0 0;
  border-right: 1px solid var(--line);
}

.rail button {
  justify-content: flex-start;
  gap: 14px;
  min-height: 50px;
  padding: 0 18px;
  border-color: transparent;
  background: transparent;
  color: var(--fg);
  font-size: 16px;
  font-weight: 500;
}

.rail button:hover:not(.on) {
  background: color-mix(in srgb, var(--fg) 6%, transparent);
}

.rail::before {
  background: var(--primary);
  box-shadow: inset 0 1px 0 var(--primary-rim), 0 8px 20px var(--primary-glow);
}

.rail button.on {
  background: transparent;
  color: var(--on-primary);
  font-weight: 600;
}

.main {
  flex: 1;
  min-width: 0;
  overflow-y: auto;
  padding: 6px 8px 24px 0;
}

.main h2 {
  margin: 0;
  font-size: 25px;
  font-weight: 600;
  line-height: 34px;
}

.lead {
  margin: 6px 0 24px;
  font-size: 15px;
  color: var(--fg-dim);
}

.group {
  margin: 26px 0 0;
  font-size: 16px;
  font-weight: 600;
}

.lead + .group {
  margin-top: 0;
}

.scope {
  margin: 4px 0 12px;
  font-size: 13px;
  color: var(--fg-dim);
}

.loading {
  display: flex;
  align-items: center;
  gap: 10px;
  margin-bottom: 12px;
  color: var(--fg-dim);
  font-size: 14px;
}

/* A card's control moves under the text, right-aligned, once the canvas is
   too narrow for both on one line (an island open beside it, the default
   1100px window): the text keeps a readable measure instead of wrapping a
   word per line. */
.card {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 14px 18px;
  width: 100%;
  margin-bottom: 14px;
  padding: 20px 22px;
  border-radius: 16px;
  border: 1px solid var(--line);
  background: color-mix(in srgb, var(--fg) 3%, transparent);
  color: var(--fg);
}

.card:not(.meters):not(.form) > :last-child {
  margin-left: auto;
}

.actions {
  display: flex;
  align-items: center;
  gap: 18px;
  flex: none;
}

/* Beside its badge the text needs less room before the actions give way. */
.link-card .card-text {
  flex-basis: 160px;
}

.link-card {
  cursor: pointer;
  transition: background var(--dur) var(--ease), border-color var(--dur) var(--ease);
}

.link-card:hover {
  background: color-mix(in srgb, var(--fg) 6%, transparent);
}

.link-card.on {
  border-color: color-mix(in srgb, var(--accent) 70%, transparent);
  background: var(--accent-soft);
}

.card-text {
  flex: 1 1 240px;
  min-width: 0;
  display: flex;
  flex-direction: column;
  gap: 5px;
}

.card-text strong {
  font-size: 17px;
  font-weight: 600;
}

.card-text span {
  font-size: 14px;
  line-height: 1.5;
  color: var(--fg-dim);
}

/* A folder path: any break point will do. */
.card-text .mono {
  overflow-wrap: anywhere;
}

.badge {
  width: 46px;
  height: 46px;
  flex: none;
  border-radius: 50%;
  display: grid;
  place-items: center;
  background: var(--glass-control);
  border: 1px solid color-mix(in srgb, var(--glass-edge) 60%, transparent);
  color: var(--fg);
  box-shadow: inset 0 1px 0 var(--glass-rim);
}

.state {
  display: inline-flex;
  align-items: center;
  gap: 8px;
  flex: none;
  font-size: 15px;
  font-weight: 500;
  color: var(--fg-dim);
  white-space: nowrap;
}

.state.ok {
  color: var(--success);
}

.state i {
  width: 9px;
  height: 9px;
  border-radius: 50%;
  background: currentColor;
}

.state.ok i {
  box-shadow: 0 0 8px color-mix(in srgb, var(--success) 70%, transparent);
}

/* The mock's action pills: a glass control with a chevron, not a link. */
.pill {
  flex: none;
  min-height: 44px;
  padding: 0 18px;
  gap: 6px;
  border-color: color-mix(in srgb, var(--glass-edge) 70%, transparent);
  background: var(--glass-control);
  font-size: 15px;
  font-weight: 600;
  box-shadow: inset 0 1px 0 color-mix(in srgb, var(--glass-rim) 60%, transparent);
}

.pill.go {
  padding: 0 14px 0 22px;
}

.seg {
  display: flex;
  flex: none;
  padding: 4px;
  border-radius: 14px;
}

.seg button {
  min-height: 36px;
  padding: 0 15px;
  gap: 6px;
  border: none;
  border-radius: 10px;
  background: none;
  color: var(--fg-dim);
  font-size: 14px;
  font-weight: 500;
}

.seg::before {
  background: var(--primary);
}

.seg button.on {
  background: transparent;
  color: var(--on-primary);
  font-weight: 600;
}

/* Accent color. Each swatch carries its skin's attribute, so it paints that skin's
   lens (style.css · Skins) whichever skin is on. Its focus ring is drawn in
   the text colour: the swatch's own accent is set for the light theme. */
.swatches {
  display: flex;
  flex-wrap: wrap;
  gap: 2px;
}

.swatch {
  flex-direction: column;
  gap: 6px;
  min-width: 50px;
  min-height: 0;
  padding: 6px 4px 4px;
  border: none;
  border-radius: 12px;
  background: none;
  color: var(--fg-dim);
  font-size: 12px;
  font-weight: 500;
}

.swatch:hover:not(:disabled) {
  background: color-mix(in srgb, var(--fg) 6%, transparent);
}

.swatch:focus-visible {
  outline-color: color-mix(in srgb, var(--fg) 60%, transparent);
}

.swatch i {
  display: grid;
  place-items: center;
  width: 30px;
  height: 30px;
  border-radius: 50%;
  color: #ffffff;
  background: linear-gradient(180deg, var(--primary-hi) 0%, var(--primary-mid) 55%, var(--primary-deep) 100%);
  box-shadow: inset 0 1px 0 rgba(255, 255, 255, 0.45), 0 4px 10px var(--primary-glow);
}

.swatch.on {
  color: var(--fg);
  font-weight: 600;
}

.swatch.on i {
  box-shadow:
    0 0 0 2px var(--canvas-flat),
    0 0 0 4px var(--primary-mid),
    0 4px 10px var(--primary-glow);
}

.wall-preview {
  flex: none;
  width: 76px;
  height: 48px;
  border-radius: 10px;
  border: 1px solid var(--line-strong);
  object-fit: cover;
  box-shadow: 0 4px 12px rgba(0, 0, 0, 0.18);
}

.meters {
  flex-direction: column;
  align-items: stretch;
  gap: 18px;
}

.meters-title {
  font-size: 15px;
  font-weight: 600;
}

.link {
  min-height: 0;
  padding: 2px 0;
  gap: 4px;
  border: none;
  background: none;
  color: var(--accent);
  font-size: 14px;
  font-weight: 500;
}

.link:hover:not(:disabled) {
  background: none;
  text-decoration: underline;
}

/* Account management: one line per account on the reading
   surface — who it is, its state in words, and its verbs as separate
   buttons. When the canvas is too narrow for both, the buttons wrap under
   the identity instead of squeezing the name. */
.section-head {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  justify-content: space-between;
  gap: 10px 16px;
}

.section-head .group {
  margin: 0;
  display: inline-flex;
  align-items: center;
  gap: 8px;
}

.count {
  min-width: 22px;
  padding: 1px 7px;
  border-radius: 999px;
  background: color-mix(in srgb, var(--fg) 9%, transparent);
  color: var(--fg-dim);
  font-size: 12px;
  font-weight: 600;
  text-align: center;
}

.add {
  min-height: 44px;
  padding: 0 20px;
  gap: 8px;
  font-size: 15px;
  font-weight: 600;
}

.account-list {
  margin: 0 0 8px;
  padding: 0;
  list-style: none;
  border-top: 1px solid var(--line);
}

.account-row {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 10px 16px;
  min-height: 76px;
  padding: 12px 0;
  border-bottom: 1px solid var(--line);
}

.account-row .who {
  flex: 1 1 220px;
}

.account-row .membership {
  margin-top: 3px;
}

.row-side {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  justify-content: flex-end;
  gap: 8px 14px;
  margin-left: auto;
  min-width: 0;
}

.row-state {
  font-size: 13px;
  font-weight: 500;
  color: var(--fg-dim);
  white-space: nowrap;
}

.row-state.current {
  color: var(--accent);
}

.row-state.warning {
  color: var(--warning);
}

.row-actions {
  display: flex;
  flex-wrap: wrap;
  justify-content: flex-end;
  gap: 8px;
}

.row-actions .pill {
  min-height: 40px;
  padding: 0 14px;
  font-size: 14px;
}

.toggle-line {
  display: inline-flex;
  align-items: center;
  gap: 12px;
  flex: none;
}

.toggle-state {
  min-width: 3em;
  font-size: 14px;
  color: var(--fg-dim);
}

.info-note {
  margin: 4px 0 0;
  padding: 14px 18px;
  border-radius: 14px;
  border: 1px solid var(--line);
  background: color-mix(in srgb, var(--fg) 4%, transparent);
  font-size: 14px;
  line-height: 1.6;
  color: var(--fg-dim);
}

/* Account management · Before sending to AI: a compact choice per account, beside its state. */
.row-consent {
  display: inline-flex;
  align-items: center;
  gap: 8px;
  font-size: 13px;
  color: var(--fg-dim);
  white-space: nowrap;
}

.select {
  position: relative;
  display: flex;
  align-items: center;
  border-radius: 12px;
}

.face {
  flex: 1;
  min-width: 0;
  min-height: 44px;
  padding: 0 40px 0 14px;
  line-height: 44px;
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
  color: var(--fg);
  font-size: 15px;
  font-weight: 500;
}

.face[aria-disabled="true"] {
  cursor: default;
  opacity: 0.6;
}

.select :deep(svg) {
  position: absolute;
  right: 14px;
  pointer-events: none;
  color: var(--fg-dim);
}

.select.mini {
  border-radius: 10px;
}

.select.mini .face {
  min-height: 34px;
  padding: 0 30px 0 11px;
  line-height: 34px;
  font-size: 13px;
}

.language-pick {
  flex: none;
  width: 200px;
}

.select.mini :deep(svg) {
  right: 10px;
}

/* Settings · AI: the connection on the left, the two short
   cards beside it, naming across the bottom; one column once the canvas
   is too narrow for two readable forms. */
.eyebrow {
  margin: 0 0 4px;
  font-size: 12px;
  font-weight: 700;
  letter-spacing: 0.08em;
  color: var(--accent);
}

.ai-grid {
  display: grid;
  grid-template-columns: minmax(0, 1.2fr) minmax(0, 1fr);
  align-items: start;
  gap: 14px;
}

.ai-grid .card {
  margin: 0;
}

.ai-side {
  display: flex;
  flex-direction: column;
  gap: 14px;
  min-width: 0;
}

.ai-grid .wide {
  grid-column: 1 / -1;
}

/* Settings · AI: TMDB's approved logo and notice under the credential, the logo
   a link to the system browser. */
.credit {
  padding-top: 14px;
  border-top: 1px solid var(--line);
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 14px 20px;
}

.logo-link {
  flex: none;
  min-height: 0;
  padding: 6px;
  border: 0;
  border-radius: 8px;
  background: transparent;
}

.logo-link:hover:not(:disabled) {
  background: var(--accent-soft);
}

.tmdb-logo {
  display: block;
  width: 150px;
  height: auto;
}

.credit p {
  margin: 0;
  font-size: 14px;
  line-height: 1.6;
}

.credit span {
  display: block;
  color: var(--fg-dim);
}

.mini-pill {
  min-height: 26px;
  margin-left: 6px;
  padding: 0 10px;
  font-size: 12px;
}

.form {
  flex-direction: column;
  flex-wrap: nowrap;
  align-items: stretch;
  gap: 12px;
}

.form-head {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  justify-content: space-between;
  gap: 10px 16px;
}

.form-head > strong {
  font-size: 17px;
  font-weight: 600;
}

.form-head .state {
  font-size: 14px;
}

.field {
  display: flex;
  flex-direction: column;
  gap: 7px;
  min-width: 0;
}

.field > span:first-child {
  font-size: 14px;
  color: var(--fg-dim);
}

.field input {
  min-height: 44px;
  width: 100%;
  min-width: 0;
  font-size: 15px;
}

.model-label {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  justify-content: space-between;
  gap: 7px 12px;
  color: var(--fg-dim);
  font-size: 14px;
}

.model-label .pill {
  min-height: 30px;
  padding: 4px 10px;
  gap: 6px;
  font-size: 12px;
}

.field input[aria-invalid="true"] {
  border-color: var(--danger);
}

.field small {
  font-size: 13px;
  line-height: 1.5;
}

.hint {
  margin: -4px 0 0;
  font-size: 13px;
  line-height: 1.5;
  color: var(--fg-dim);
}

.bad,
.hint.bad {
  color: var(--danger);
}

.key-line {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 8px;
}

.key-line input {
  flex: 1 1 200px;
}

.key-hint {
  flex: 1 1 160px;
  min-height: 44px;
  display: flex;
  align-items: center;
  padding: 0 13px;
  border-radius: 10px;
  border: 1px solid var(--line-strong);
  background: color-mix(in srgb, var(--fg) 4%, transparent);
  font-size: 15px;
  letter-spacing: 0.06em;
}

.pill.primary {
  border-color: transparent;
  background: var(--primary) border-box;
  color: var(--on-primary);
}

.test-line {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 10px 14px;
  margin-top: 4px;
}

.test-result {
  flex: 1 1 220px;
  min-width: 0;
  margin: 0;
  display: flex;
  flex-direction: column;
  gap: 2px;
  font-size: 13px;
  line-height: 1.5;
}

.test-result strong {
  font-size: 14px;
  font-weight: 600;
}

.test-result.ok strong {
  color: var(--success);
}

.test-result.bad strong {
  color: var(--danger);
}

.test-result span {
  color: var(--fg-dim);
  overflow-wrap: anywhere;
  -webkit-user-select: text;
  user-select: text;
}

.unit-input {
  position: relative;
  display: flex;
}

.unit-input em {
  position: absolute;
  right: 14px;
  top: 50%;
  transform: translateY(-50%);
  font-style: normal;
  font-size: 13px;
  color: var(--fg-dim);
  pointer-events: none;
}

.unit-input input {
  padding-right: 70px;
}

/* Unlimited: the number stays visible (it comes back when the switch is off) but reads as inactive. */
.unit-input:has(input:disabled) {
  opacity: 0.45;
}

/* Test connection found: tools and streaming, quiet unless one is missing. */
.caps {
  display: flex;
  flex-wrap: wrap;
  gap: 6px 18px;
  margin: 0;
  font-size: 13px;
}

.caps div {
  display: flex;
  gap: 6px;
}

.caps dt {
  color: var(--fg-dim);
}

.caps dd {
  margin: 0;
}

.caps dd.yes {
  color: var(--success);
}

.caps dd.no {
  color: var(--warning);
}

.shield-note {
  display: flex;
  align-items: flex-start;
  gap: 8px;
}

.shield-note > svg {
  flex: none;
  margin-top: 2px;
  color: var(--success);
}

.form .seg {
  align-self: flex-start;
}

.naming-pick {
  flex: 0 1 200px;
}

.templates {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: 12px 14px;
}

.previews {
  margin: 0;
  padding-top: 12px;
  border-top: 1px solid var(--line);
  display: flex;
  flex-direction: column;
  gap: 10px;
}

.previews dt {
  font-size: 13px;
  color: var(--fg-dim);
}

.previews dd {
  margin: 3px 0 0;
  font-size: 14px;
  overflow-wrap: anywhere;
  -webkit-user-select: text;
  user-select: text;
}

.naming-save {
  display: flex;
  justify-content: flex-end;
}

/* Settings · Network & proxy: the Connection method cards, the draft's
   Connection overview beside them, and the Test connection / Apply settings row that ends the page. */
.net-head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 16px;
}

/* Applies locally: these settings never leave the computer and never follow the
   account. Green like every other "stays here" mark in Settings. */
.scope-pill {
  flex: none;
  padding: 5px 11px;
  border-radius: var(--radius-pill);
  border: 1px solid color-mix(in srgb, var(--success) 35%, transparent);
  color: var(--success);
  font-size: 13px;
  white-space: nowrap;
}

/* The form carries the fields, the Connection overview column only reads back: it
   takes what it needs and leaves the rest to the form. */
.net-grid {
  grid-template-columns: minmax(0, 1fr) minmax(0, 300px);
}

.section-label {
  display: flex;
  align-items: baseline;
  justify-content: space-between;
  gap: 12px;
}

.section-label .scope {
  margin: 0;
}

.mode-grid {
  display: grid;
  grid-template-columns: repeat(3, minmax(0, 1fr));
  gap: 12px;
  margin-bottom: 18px;
}

/* A radio as a card: the whole card is the target, and the dot on its top
   right is what a native radio would be. */
.mode-card {
  display: flex;
  flex-direction: column;
  align-items: flex-start;
  justify-content: flex-start;
  gap: 2px;
  min-height: 104px;
  padding: 14px 16px;
  border-radius: 16px;
  border: 1px solid var(--line);
  background: color-mix(in srgb, var(--fg) 3%, transparent);
  color: var(--fg);
  text-align: left;
  /* Buttons don't wrap by default; these say a sentence. */
  white-space: normal;
}

.mode-card:hover:not(.on) {
  background: color-mix(in srgb, var(--fg) 6%, transparent);
}

.mode-card.on {
  border-color: color-mix(in srgb, var(--accent) 70%, transparent);
  background: var(--accent-soft);
}

.mode-top {
  display: flex;
  align-items: center;
  justify-content: space-between;
  width: 100%;
  margin-bottom: 8px;
  color: var(--accent);
}

.mode-top .dot {
  width: 16px;
  height: 16px;
  border-radius: 50%;
  border: 1px solid var(--line-strong);
}

.mode-card.on .dot {
  border-color: var(--accent);
  background: radial-gradient(circle, var(--accent) 0 4px, transparent 5px);
}

.mode-card strong {
  font-size: 15px;
  font-weight: 600;
}

.mode-card small {
  font-size: 13px;
  color: var(--fg-dim);
}

.draft-badge {
  padding: 3px 9px;
  border-radius: 10px;
  background: color-mix(in srgb, var(--warning) 14%, transparent);
  color: var(--warning);
  font-size: 13px;
}

.host-row {
  display: grid;
  grid-template-columns: minmax(0, 1fr) 120px;
  gap: 12px;
}

.toggle-row {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 14px;
  padding-top: 12px;
  border-top: 1px solid var(--line);
}

/* The switch keeps the size it has everywhere else in Settings; only its hit
   area grows to the 40px this page asks for. */
.toggle-row .switch::before {
  content: "";
  position: absolute;
  inset: -6px 0;
}

.pw-field {
  position: relative;
  display: flex;
}

.pw-field input {
  padding-right: 46px;
}

.pw-eye {
  position: absolute;
  right: 2px;
  top: 2px;
  width: 40px;
  min-height: 40px;
  padding: 0;
  border-color: transparent;
  background: transparent;
  color: var(--fg-dim);
}

/* A disclosure is a target like any other: 40px tall, left-aligned. */
.advanced {
  align-self: flex-start;
  min-height: 40px;
  padding: 4px 0;
}

/* The global input styling already dresses it; it only needs the field's
   width, the type size of the inputs beside it and room for two lines. */
.field textarea {
  width: 100%;
  min-height: 62px;
  font-size: 15px;
}

.status-card {
  gap: 0;
}

.status-head {
  display: flex;
  align-items: center;
  gap: 10px;
}

.status-head .card-text strong {
  font-size: 17px;
}

.status-icon {
  display: grid;
  place-items: center;
  width: 38px;
  height: 38px;
  flex: none;
  border-radius: 50%;
  border: 1px solid color-mix(in srgb, var(--accent) 35%, transparent);
  background: var(--accent-soft);
  color: var(--accent);
}

/* PKP → Method → Network: where the requests of this app go, in three words. */
.route {
  display: flex;
  align-items: center;
  gap: 6px;
  margin: 16px 0;
}

.route-node {
  flex: none;
  padding: 6px 10px;
  border-radius: 10px;
  border: 1px solid var(--line);
  font-size: 13px;
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}

/* PKP and Network keep their words; only the middle gives way. */
.route-node.mid {
  flex: 0 1 auto;
  min-width: 0;
  border-color: color-mix(in srgb, var(--accent) 45%, transparent);
  background: var(--accent-soft);
  color: var(--accent);
}

.route i {
  flex: 1;
  min-width: 8px;
  height: 1px;
  background: color-mix(in srgb, var(--accent) 60%, transparent);
}

.status-rows {
  margin: 0;
  border-top: 1px solid var(--line);
}

.status-rows > div {
  display: flex;
  flex-direction: column;
  gap: 2px;
  padding: 10px 0;
  border-bottom: 1px solid var(--line);
}

.status-rows dt {
  font-size: 13px;
  color: var(--fg-dim);
}

.status-rows dd {
  margin: 0;
  font-size: 14px;
  overflow-wrap: anywhere;
}

.test-state {
  display: flex;
  align-items: flex-start;
  gap: 8px;
  margin: 14px 0 10px;
  padding: 10px 12px;
  border-radius: 12px;
  background: var(--accent-soft);
  color: var(--accent);
  font-size: 14px;
  line-height: 1.5;
}

.test-state.ok {
  background: color-mix(in srgb, var(--success) 14%, transparent);
  color: var(--success);
}

.test-state.bad {
  background: color-mix(in srgb, var(--danger) 14%, transparent);
  color: var(--danger);
}

/* The notes under the test result are separate thoughts, not a paragraph. */
.status-card .hint + .hint {
  margin-top: 8px;
}

.action-row {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 14px;
  margin-top: 18px;
  padding-top: 16px;
  border-top: 1px solid var(--line);
}

/* A note whose sign is part of the sentence, not a colour it is printed in. */
.icon-note {
  display: flex;
  align-items: flex-start;
  gap: 7px;
}

.icon-note svg {
  flex: none;
  margin-top: 2px;
}

.save-note {
  margin: 0;
  max-width: 64ch;
}

@container settings (max-width: 960px) {
  .ai-grid {
    grid-template-columns: minmax(0, 1fr);
  }
}

@container settings (max-width: 600px) {
  .templates {
    grid-template-columns: minmax(0, 1fr);
  }

  /* The heading needs the whole width here; Applies locally is repeated by the
     note above the buttons. */
  .scope-pill {
    display: none;
  }

  /* Connection method stands on end and the form goes to one column; the two
     buttons stay reachable by scrolling, never cut off. */
  .mode-grid {
    grid-template-columns: minmax(0, 1fr);
  }

  .mode-card {
    min-height: 0;
  }

  .host-row {
    grid-template-columns: minmax(0, 1fr);
  }

  .action-row {
    flex-direction: column;
    align-items: stretch;
  }

  .action-row .actions {
    justify-content: flex-end;
  }
}

/* Narrow canvases (an island beside, a small window): the rail keeps
   its icons, the labels go. */
@container settings (max-width: 720px) {
  .rail {
    width: auto;
  }

  .rail button {
    padding: 0 15px;
    font-size: 0;
    gap: 0;
  }
}

/* Short windows: the ten sections sit closer so all of them stay in
   reach; a window shorter still scrolls the rail instead of cutting it. */
@media (max-height: 820px) {
  .rail {
    gap: 2px;
    overflow-y: auto;
    scrollbar-width: none;
  }

  .rail button {
    flex: none;
    min-height: 40px;
  }
}

/* The narrowest canvas — an island open in a small window: the rail lies
   down as a row of icons above the list, and
   each card keeps its title and its action; the island beside it holds the
   details. */
@container settings (max-width: 360px) {
  .cols {
    flex-direction: column;
    gap: 14px;
  }

  .rail {
    flex-direction: row;
    padding: 0 0 10px;
    border-right: none;
    border-bottom: 1px solid var(--line);
  }

  .rail button {
    min-height: 40px;
    padding: 0 10px;
  }

  .main h2 {
    margin-bottom: 12px;
    font-size: 20px;
    line-height: 28px;
  }

  .lead,
  .badge,
  .card-text span {
    display: none;
  }

  /* Connection overview names what the headline under it is about; without it the
     card reads as a loose sentence. */
  .status-head .card-text span {
    display: revert;
  }

  .card {
    gap: 10px;
    padding: 14px;
  }

  .card-text strong {
    font-size: 15px;
  }

  /* A line of its own, wrapping inside it: as a fixed-size flex item it
     would keep its one-line width and overflow the card. */
  .actions {
    flex: 1 1 100%;
    min-width: 0;
    flex-wrap: wrap;
    justify-content: flex-end;
    gap: 8px 12px;
  }

  .pill.go {
    min-height: 36px;
    padding: 0 10px 0 14px;
    font-size: 14px;
  }
}
</style>
