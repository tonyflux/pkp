<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, watch } from "vue";
import Icon from "./Icon.vue";
import Logo from "./Logo.vue";
import AccountAvatar from "./AccountAvatar.vue";
import AccountPopover from "./AccountPopover.vue";
import SavedSearches from "./SavedSearches.vue";
import { RAIL_QUERY, WORKSPACE_QUERY, modKey, shortcutText, useMedia, useOutsideClick } from "../dom";
import { bytes, maskLogin } from "../format";
import { t } from "../i18n";
import { syncAttention } from "../syncstore";
import {
  NAV_KEYS,
  accountLabel,
  accounts,
  activeTaskCount,
  currentAccount,
  drive,
  openNav,
  pickAndUpload,
  pendingArrivals,
  setNavCompact,
  ui,
  type NavKey,
} from "../store";

interface NavItem {
  key: NavKey;
  label: string;
  icon: string;
  badge?: number;
  /** The item's name with what the badge counts, for assistive tech: 「Transfer Center, 3 items in progress」. */
  badgeLabel?: string;
}

// Cleanup follows Starred: it works on the files of the
// account on screen and needs no model. AI Assistant follows it.
const groups = computed<{ title: string; items: NavItem[] }[]>(() => [
  {
    title: t("我的文件"),
    items: [
      // nav|: the rail's label is ~6 Japanese characters wide; these three are shorter there.
      { key: "all", label: t("nav|全部文件"), icon: "folder" },
      { key: "starred", label: t("星标"), icon: "star" },
      { key: "cleanup", label: t("nav|清理"), icon: "layers" },
      // Newly added, pending organization (organize on import): suggestions waiting, never something done.
      {
        key: "assistant",
        label: t("nav|AI 助手"),
        icon: "spark",
        badge: pendingArrivals.value.length,
        badgeLabel: t("AI 助手，{n} 项待整理", { n: pendingArrivals.value.length }),
      },
      { key: "history", label: t("nav|播放历史"), icon: "clock" },
    ],
  },
  {
    title: t("工具与服务"),
    items: [
      {
        key: "transfers",
        label: t("传输中心"),
        icon: "transfer",
        badge: activeTaskCount.value,
        badgeLabel: t("传输中心，{n} 项进行中", { n: activeTaskCount.value }),
      },
      {
        key: "sync",
        label: t("同步与备份"),
        icon: "repeat",
        badge: syncAttention.value,
        badgeLabel: t("同步与备份，{n} 个同步对需要处理", { n: syncAttention.value }),
      },
      { key: "shares", label: t("我的分享"), icon: "shares" },
      { key: "trash", label: t("回收站"), icon: "trash" },
    ],
  },
]);

const activeKey = computed(() => {
  switch (ui.route) {
    case "history":
    case "sync":
    case "transfers":
    case "shares":
    case "assistant":
    case "cleanup":
      return ui.route;
    case "settings":
      return "settings";
    default:
      return ui.view;
  }
});

/* ---------- the account trigger ---------- */

const accountName = computed(() => accountLabel(currentAccount.value) || maskLogin(drive.status?.account ?? "") || t("未登录"));
const accountLogin = computed(() => maskLogin(currentAccount.value?.login || drive.status?.account || ""));
const needsPassword = computed(() => !!(drive.status?.needsPassword || currentAccount.value?.needsPassword));
// The second line: what is happening to the account, else its masked login.
const accountLine = computed(() => {
  if (accounts.switching) return t("正在切换…");
  if (needsPassword.value) return t("nav|需要重新登录");
  return accountLogin.value;
});
const triggerLabel = computed(() => {
  const name = accountName.value;
  let text = accountLabel(currentAccount.value) ? t("切换账号，当前「{name}」", { name }) : t("切换账号，当前{name}", { name });
  if (accountLogin.value) text = t("{text}，{login}", { text, login: accountLogin.value });
  if (accounts.switching) return t("{text}，正在切换", { text });
  if (needsPassword.value) return t("{text}，需要重新登录", { text });
  return text;
});
const trigger = ref<HTMLElement | null>(null);

// The storage chart lives in the context column's Overview.
// Where that column is not on screen — put away, or another page — one
// line of it stays here above the account, never a second chart.
const workspaceNarrow = useMedia(WORKSPACE_QUERY);
const overviewOnScreen = computed(
  () =>
    (ui.route === "files" || ui.route === "viewer") &&
    (workspaceNarrow.value ? ui.workspace === "info" : ui.panelOpen),
);

const quotaText = computed(() => {
  const q = drive.quota;
  if (!q) return "";
  const used = bytes(q.usage, "0 B", 1);
  return q.isUnlimited ? t("已用 {used} / 不限", { used }) : t("已用 {used} / {limit}", { used, limit: bytes(q.limit, "0 B", 1) });
});

/* ---------- the Create menu and the account panel ---------- */

// Create makes things in the folder on screen; Cloud Add has its own button in
// the toolbar and is not repeated here.
const createOpen = ref(false);
const savedSearchPopover = ref(false);

function menuAction(fn: () => unknown) {
  createOpen.value = false;
  void fn();
}

useOutsideClick("[data-create-menu]", () => createOpen.value, () => (createOpen.value = false));

// Switch Account opens from here from 960px up; below that the toolbar has it.
const accountsHere = computed(() => ui.accountsOpen && !workspaceNarrow.value);

// An open menu owns Esc, ahead of the page behind it (which would close the
// details island on the same key). The account panel handles its own.
function onMenuEscape(event: KeyboardEvent) {
  if (event.key !== "Escape" || !createOpen.value) return;
  event.stopImmediatePropagation();
  createOpen.value = false;
}

/* ---------- icon rail ---------- */

// Content narrower than 1080px gets the rail whatever the
// choice: the labels would take the grid's second column. Above it, the user
// picks (the brand button, ⌘\ or Settings · General), and the choice is kept.
const narrowQuery = window.matchMedia(RAIL_QUERY);
const narrow = ref(narrowQuery.matches);
const compact = computed(() => narrow.value || ui.navCompact);
const toggleTitle = computed(() =>
  ui.navCompact ? t("展开侧栏（{key}\\）", { key: modKey }) : t("只显示图标（{key}\\）", { key: modKey }),
);
// The rail has no room beside the logo for the toggle, so the logo itself is
// the way back to the full nav — unless the window is too narrow for one.
const markToggles = computed(() => ui.navCompact && !narrow.value);

// A menu opened in one layout would fly out from the wrong place in the other.
watch([compact, workspaceNarrow], () => {
  createOpen.value = false;
  ui.accountsOpen = false;
});

function onNarrow(event: MediaQueryListEvent) {
  narrow.value = event.matches;
}

onMounted(() => {
  narrowQuery.addEventListener("change", onNarrow);
  window.addEventListener("keydown", onMenuEscape, true);
});
onBeforeUnmount(() => {
  narrowQuery.removeEventListener("change", onNarrow);
  window.removeEventListener("keydown", onMenuEscape, true);
});
</script>

<template>
  <aside class="nav glass glass-nav" :class="{ compact, raised: createOpen || accountsHere || savedSearchPopover }">
    <!-- The brand block doubles as the window drag handle: the title bar is
         hidden so the island has to provide one. -->
    <header class="brand" style="--wails-draggable: drag">
      <!-- Clicks pass through to the drag handle, except in the rail, where
           the logo turns into the expand button under the pointer. -->
      <button
        class="mark"
        :class="{ toggles: markToggles }"
        :tabindex="markToggles ? 0 : -1"
        :title="markToggles ? toggleTitle : undefined"
        :aria-label="markToggles ? toggleTitle : undefined"
        :aria-hidden="markToggles ? undefined : 'true'"
        :aria-expanded="markToggles ? false : undefined"
        @click="markToggles && setNavCompact(false)"
      >
        <Logo :size="44" />
        <Icon class="mark-icon" name="sidebar" :size="20" />
      </button>
      <div class="wordmark">
        <strong>PKP</strong>
        <small>for PikPak</small>
      </div>
      <button
        v-if="!narrow"
        class="quiet icon rail-toggle"
        style="--wails-draggable: no-drag"
        :title="toggleTitle"
        :aria-label="toggleTitle"
        :aria-expanded="!compact"
        @click="setNavCompact(!ui.navCompact)"
      >
        <Icon name="sidebar" :size="18" />
      </button>
    </header>

    <div class="create" data-create-menu>
      <button
        class="primary new"
        :title="t('新建')"
        :aria-label="t('新建')"
        aria-haspopup="menu"
        :aria-expanded="createOpen"
        @click="createOpen = !createOpen"
      >
        <Icon name="plus" :size="20" />
        <span class="label">{{ t("新建") }}</span>
        <Icon class="caret" name="chevronDown" :size="16" />
      </button>
      <div v-if="createOpen" class="menu glass glass-sheet" role="menu" :aria-label="t('新建')">
        <button class="quiet" role="menuitem" @click="menuAction(() => (ui.newFolderOpen = true))">
          <Icon name="folderPlus" :size="18" />
          {{ t("新建文件夹") }}
        </button>
        <button class="quiet" role="menuitem" @click="menuAction(() => pickAndUpload('files'))">
          <Icon name="upload" :size="18" />
          {{ t("上传文件") }}
        </button>
        <button class="quiet" role="menuitem" @click="menuAction(() => pickAndUpload('folders'))">
          <Icon name="folderUpload" :size="18" />
          {{ t("上传文件夹") }}
        </button>
      </div>
    </div>

    <nav class="groups">
      <section v-for="group in groups" :key="group.title">
        <h3>{{ group.title }}</h3>
        <button
          v-for="item in group.items"
          :key="item.key"
          class="item"
          :class="{ active: activeKey === item.key }"
          :title="t('{name}（{note}）', { name: item.label, note: shortcutText(String(NAV_KEYS.indexOf(item.key) + 1)) })"
          :aria-label="item.badge ? item.badgeLabel : item.label"
          :aria-current="activeKey === item.key ? 'page' : undefined"
          @click="openNav(item.key)"
        >
          <Icon :name="item.icon" :size="20" />
          <span class="label">{{ item.label }}</span>
          <em v-if="item.badge">{{ item.badge }}</em>
        </button>
      </section>
      <SavedSearches :compact="compact" @popover="savedSearchPopover = $event" />
    </nav>

    <footer class="foot">
      <p v-if="drive.quota && !overviewOnScreen" class="quota tnum" :title="quotaText">{{ quotaText }}</p>

      <!-- On Settings the account row becomes the active lens. The whole row
           is the account-switch trigger; the panel opens above it beside the rail. -->
      <div
        class="account"
        :class="{ current: activeKey === 'settings', warn: needsPassword && !accounts.switching }"
      >
        <button
          ref="trigger"
          class="trigger"
          data-account-trigger
          :title="`${accountName}${accountLogin ? ` · ${accountLogin}` : ''}`"
          :aria-label="triggerLabel"
          aria-haspopup="dialog"
          :aria-expanded="ui.accountsOpen"
          @click="ui.accountsOpen = !ui.accountsOpen"
        >
          <AccountAvatar :account="currentAccount" :size="40" />
          <span v-if="activeKey === 'settings'" class="who here"><Icon name="settings" :size="20" />{{ t("设置") }}</span>
          <span v-else class="who">
            <strong>{{ accountName }}</strong>
            <small v-if="accountLine" :class="{ busy: !!accounts.switching }">{{ accountLine }}</small>
          </span>
          <Icon class="more" :name="activeKey === 'settings' ? 'chevronRight' : 'chevronUp'" :size="16" />
        </button>
        <AccountPopover v-if="accountsHere" :placement="compact ? 'rail' : 'nav'" :trigger="trigger" />
      </div>
    </footer>
  </aside>
</template>

<style scoped>
.nav {
  /* Room for translated labels and two-digit badges; the icon rail stays 72px. */
  width: 248px;
  flex: none;
  display: flex;
  flex-direction: column;
  gap: 18px;
  /* Top space keeps the brand clear of the traffic lights when the window
     is short enough for them to sit level with the island's top edge. */
  padding: 30px 18px 22px;
  overflow: hidden;
  /* Collapsing and expanding only move things sideways. Every row keeps
     its height and place in both widths, and text never wraps: it is
     clipped and faded. So nothing below the brand jumps while the island
     narrows. Text leaves at once and comes back once there is room. */
  --fade-in: opacity 0.2s var(--ease) 0.1s, visibility 0s;
  --fade-out: opacity 0.12s ease-out, visibility 0s linear 0.12s;
  transition:
    width var(--dur) var(--ease),
    padding var(--dur) var(--ease);
}

.nav:lang(en),
.nav:lang(ko) {
  width: 284px;
}

.nav:lang(ja) {
  width: 308px;
}

/* While a menu is open the island must sit above the canvas, which follows
   it in the document and would otherwise cover the menu. */
.nav.raised {
  z-index: 30;
}

.brand {
  display: flex;
  align-items: center;
  gap: 14px;
  padding: 0 4px;
  transition: padding var(--dur) var(--ease);
}

.mark {
  display: grid;
  place-items: center;
  flex: none;
  width: 44px;
  height: 44px;
  min-height: 0;
  padding: 0;
  border: 0;
  border-radius: 14px;
  background: transparent;
  pointer-events: none;
}

.mark > * {
  grid-area: 1 / 1;
  transition: opacity 0.15s var(--ease);
}

.mark-icon {
  opacity: 0;
  color: var(--fg);
}

.mark.toggles {
  --wails-draggable: no-drag;
  pointer-events: auto;
}

.mark.toggles:hover,
.mark.toggles:focus-visible {
  background: var(--accent-soft);
}

.mark.toggles:hover > :deep(.logo),
.mark.toggles:focus-visible > :deep(.logo) {
  opacity: 0;
}

.mark.toggles:hover > .mark-icon,
.mark.toggles:focus-visible > .mark-icon {
  opacity: 1;
}

.rail-toggle {
  width: 32px;
  height: 32px;
  min-height: 32px;
  margin-left: auto;
  margin-right: -6px;
}

.wordmark {
  display: flex;
  flex-direction: column;
  line-height: 1.2;
  min-width: 0;
  overflow: hidden;
  white-space: nowrap;
}

/* Three capitals, set bold and tight as in the design's PKP brand block. */
.wordmark strong {
  font-size: 23px;
  font-weight: 700;
  letter-spacing: -0.04em;
}

.wordmark small {
  font-size: 12px;
  color: var(--fg-dim);
}

.new {
  width: 100%;
  min-height: 48px;
  padding: 0 20px 0 24px;
  gap: 14px;
  font-size: 17px;
  justify-content: flex-start;
  overflow: hidden;
  transition:
    padding var(--dur) var(--ease),
    background var(--dur) var(--ease),
    border-color var(--dur) var(--ease),
    box-shadow var(--dur) var(--ease),
    transform 0.12s var(--ease);
}

.new .label {
  flex: 1;
  text-align: left;
}

.new .caret {
  opacity: 0.85;
}

.new[aria-expanded="true"] .caret {
  transform: rotate(180deg);
}

/* The Create menu drops under the button, as wide as it. It lies over the
   nav's own rows, so it is the near-opaque reading surface rather than a
   second glass on the island's glass, or the rows show through. */
.create {
  position: relative;
}

/* At least as wide as the button; wider when a label needs it (ja
   ファイルをアップロード), reaching past the island over the canvas. */
.create .menu {
  position: absolute;
  top: calc(100% + 8px);
  left: 0;
  min-width: 100%;
  width: max-content;
  background: var(--canvas);
}

.create .menu button {
  gap: 12px;
}

.groups {
  flex: 1;
  overflow-y: auto;
  scrollbar-width: none;
  display: flex;
  flex-direction: column;
  gap: 28px;
  /* Items run 8px from the island edge, wider than the brand and Create. */
  margin: 0 -10px;
  padding: 0 10px;
}

.groups::-webkit-scrollbar {
  width: 0;
}

.groups section {
  display: flex;
  flex-direction: column;
  gap: 8px;
}

/* In the rail the heading fades but keeps its line: the empty line is the
   gap between the groups. */
.groups h3 {
  margin: 0 0 4px 10px;
  font-size: 13px;
  font-weight: 500;
  color: var(--fg-dim);
  white-space: nowrap;
  overflow: hidden;
}

.item {
  position: relative;
  width: 100%;
  min-height: 46px;
  justify-content: flex-start;
  gap: 12px;
  padding: 0 20px 0 26px;
  border: 1px solid transparent;
  border-radius: var(--radius-pill);
  background: transparent;
  color: var(--fg);
  font-size: 16px;
  font-weight: 500;
  overflow: hidden;
  transition:
    padding var(--dur) var(--ease),
    background var(--dur) var(--ease),
    border-color var(--dur) var(--ease),
    transform 0.12s var(--ease);
}

.item > svg {
  flex: none;
  color: var(--fg);
}

.item .label {
  flex: 1;
  text-align: left;
  overflow: hidden;
  text-overflow: ellipsis;
}

.item:hover:not(.active) {
  background: color-mix(in srgb, var(--glass-control) 55%, transparent);
}

/* The active item is a lens: a local lift of the same glass, not a second
   pane stacked on top of it. */
.item.active {
  font-weight: 600;
  background: var(--glass-control);
  border-color: var(--glass-edge);
  box-shadow:
    inset 0 1px 0 var(--glass-rim),
    inset 0 -1px 0 color-mix(in srgb, var(--glass-rim) 20%, transparent),
    0 8px 20px rgba(10, 30, 60, 0.16);
}

/* Badges reserve their width beside the label; in the rail they sit on the icon. */
.item em {
  flex: none;
  font-style: normal;
  font-size: 13px;
  font-weight: 600;
  min-width: 22px;
  height: 22px;
  padding: 0 6px;
  border-radius: 999px;
  display: grid;
  place-items: center;
  background: var(--primary);
  color: var(--on-primary);
  box-shadow: inset 0 1px 0 var(--primary-rim);
  transition:
    top var(--dur) var(--ease),
    right var(--dur) var(--ease),
    min-width var(--dur) var(--ease),
    height var(--dur) var(--ease),
    padding var(--dur) var(--ease),
    font-size var(--dur) var(--ease);
}

.foot {
  display: flex;
  flex-direction: column;
  gap: 22px;
  padding: 4px 2px 0;
  transition: padding var(--dur) var(--ease);
}

.quota {
  margin: 0;
  padding: 0 4px;
  font-size: 13px;
  color: var(--fg-dim);
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}

.account {
  position: relative;
  display: flex;
  align-items: center;
  gap: 14px;
  transition:
    margin var(--dur) var(--ease),
    padding var(--dur) var(--ease);
}

/* The trigger keeps the old row's geometry: a 40px avatar, the name, the
   chevron at the row's end. In the rail it is clipped to the avatar. */
.trigger {
  flex: 1;
  min-width: 0;
  height: 40px;
  min-height: 40px;
  padding: 0;
  gap: 14px;
  justify-content: flex-start;
  overflow: hidden;
  border: 0;
  background: transparent;
  color: var(--fg);
  text-align: left;
}

.trigger:hover:not(:disabled) {
  border-color: transparent;
  background: transparent;
}

.trigger:hover .more,
.trigger[aria-expanded="true"] .more {
  color: var(--fg);
}

.who {
  flex: 1;
  min-width: 0;
  display: flex;
  flex-direction: column;
  line-height: 1.3;
  overflow: hidden;
  white-space: nowrap;
}

.who strong,
.who small {
  overflow: hidden;
  text-overflow: ellipsis;
}

.who strong {
  font-size: 15px;
  font-weight: 600;
}

.who small {
  font-size: 12px;
  color: var(--fg-dim);
}

.account.warn .who small {
  color: var(--warning);
  font-weight: 600;
}

.account.current {
  margin: 0 -10px;
  padding: 6px 14px 6px 6px;
  border-radius: var(--radius-pill);
  border: 1px solid var(--glass-edge);
  background: var(--glass-control);
  box-shadow:
    inset 0 1px 0 var(--glass-rim),
    inset 0 -1px 0 color-mix(in srgb, var(--glass-rim) 20%, transparent),
    0 8px 20px rgba(10, 30, 60, 0.16);
}

.who.here {
  flex-direction: row;
  align-items: center;
  justify-content: center;
  /* 6px, not 10: English "Settings" plus its icon is 2px wider than the row allows with 10. */
  gap: 6px;
  font-size: 16px;
  font-weight: 600;
}

.more {
  flex: none;
  margin-right: 2px;
  color: var(--fg-dim);
}

.menu button {
  font-size: 15px;
}

/* What only the full nav shows. */
.wordmark,
.new .label,
.new .caret,
.groups h3,
.item .label,
.quota,
.who {
  transition: var(--fade-in);
}

.rail-toggle,
.more {
  transition:
    var(--fade-in),
    background var(--dur) var(--ease),
    border-color var(--dur) var(--ease),
    transform 0.12s var(--ease);
}

/* The icon rail: labels go, titles and accessible names stay. It is the
   user's choice from 1080px up and the only layout below that. Only widths
   and side paddings differ from the full nav; the side paddings centre each
   icon in the 72px island (1px glass border + 8px padding). */
.nav.compact {
  width: 72px;
  padding-inline: 8px;
}

/* The menus fly out beside the rail, and the account panel (360 wide) out of
   the full nav too. Nothing animates its width while one is open: a layout
   change closes them. */
.nav.raised {
  overflow: visible;
}

.compact .wordmark,
.compact .new .label,
.compact .new .caret,
.compact .groups h3,
.compact .item .label,
.compact .who {
  opacity: 0;
  transition: var(--fade-out);
}

.compact .rail-toggle,
.compact .quota,
.compact .more {
  opacity: 0;
  visibility: hidden;
  transition: var(--fade-out);
}

.compact .brand {
  padding: 0 5px;
}

.compact .new,
.compact .item {
  padding: 0 0 0 16px;
}

.compact .item em {
  position: absolute;
  top: 4px;
  right: 4px;
  min-width: 18px;
  height: 18px;
  padding: 0 4px;
  font-size: 11px;
}

.compact .foot {
  padding: 4px 7px 0;
}

/* On Settings the lens around the avatar is the rail's active lens: as wide as
   the others (54px) and a circle with an even ring, at the full nav's 54px
   height so collapsing moves nothing up or down. */
.compact .account.current {
  margin: 0 -7px;
  padding: 6px;
}

/* Level with the button; 21px clears the island's padding and edge by the
   same 10px as the account panel beside the rail. */
.compact .create .menu {
  top: 0;
  left: calc(100% + 21px);
  right: auto;
  min-width: 180px;
}

/* The list is seven items: a short window (the 1100×760
   default) keeps all of them in view by closing up the gaps — in the rail
   and the full nav alike, so every row keeps its y across the two. */
@media (max-height: 820px) {
  .groups {
    gap: 14px;
  }

  .groups section {
    gap: 4px;
  }

  .foot {
    gap: 14px;
  }
}

/* The global rule shortens durations; the fade-in delay has to go too. */
@media (prefers-reduced-motion: reduce) {
  .nav,
  .nav * {
    transition-delay: 0s !important;
  }
}
</style>
