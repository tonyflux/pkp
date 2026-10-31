<script setup lang="ts">
// Rename by rules: rules picked here rename the file page's
// picks. The sheet only previews — the first names before → after and how
// many change; Generate plan hands the rules to the assistant, whose plan is
// confirmed item by item before anything on the drive is renamed. No model.
import { computed, nextTick, onBeforeUnmount, onMounted, ref, watch } from "vue";
import Icon from "./Icon.vue";
import SelectMenu, { type SelectOption } from "./SelectMenu.vue";
import { inSelectMenu } from "../dom";
import { t } from "../i18n";
import { applyRules, type RenameRule } from "../renamerules";
import { currentId, openRenamePlan, renameSheet } from "../store";

const PREVIEW = 8;

const strip = ref(true);
const pad = ref(false);
const width = ref(2);
const widths = computed<SelectOption<number>[]>(() => [2, 3, 4].map((n) => ({ value: n, label: t("{n} 位", { n }) })));
const replace = ref(false);
const find = ref("");
const replaceWith = ref("");
const sheet = ref<HTMLElement>();

const open = computed(() => renameSheet.entries.length > 0);

const rules = computed<RenameRule[]>(() => {
  const out: RenameRule[] = [];
  if (strip.value) out.push({ kind: "stripTags" });
  if (pad.value) out.push({ kind: "padNumbers", width: width.value });
  if (replace.value && find.value) out.push({ kind: "replace", find: find.value, with: replaceWith.value });
  return out;
});

const renamed = computed(() => renameSheet.entries.map((e) => ({ entry: e, name: applyRules(e.name, rules.value, e.folder) })));
const changed = computed(() => renamed.value.filter((r) => r.name !== r.entry.name).length);
const preview = computed(() => renamed.value.slice(0, PREVIEW));

let returnFocus: HTMLElement | null = null;
watch(open, async (now) => {
  if (!now) {
    returnFocus?.focus();
    returnFocus = null;
    return;
  }
  returnFocus = document.activeElement instanceof HTMLElement ? document.activeElement : null;
  await nextTick();
  sheet.value?.querySelector<HTMLElement>("input")?.focus();
});

// The picks were this account's; another account's page has others.
watch(currentId, () => (renameSheet.entries = []));

function close() {
  renameSheet.entries = [];
}

function generate() {
  if (!changed.value) return;
  const entries = renameSheet.entries;
  const picked = rules.value;
  renameSheet.entries = [];
  void openRenamePlan(entries, picked);
}

// The sheet owns Esc while it is up, ahead of the page behind it; an open
// list (Pad to how many digits) takes it first and closes only itself.
function onKeydown(ev: KeyboardEvent) {
  if (!open.value || ev.key !== "Escape" || inSelectMenu(ev)) return;
  ev.stopImmediatePropagation();
  close();
}

onMounted(() => window.addEventListener("keydown", onKeydown, true));
onBeforeUnmount(() => window.removeEventListener("keydown", onKeydown, true));
</script>

<template>
  <Teleport to="body">
    <div v-if="open" class="scrim" @click.self="close">
      <section ref="sheet" class="sheet glass glass-sheet rename-sheet" role="dialog" aria-modal="true" aria-labelledby="rename-rules-title" @keydown.stop>
        <header>
          <div class="titles">
            <span class="rr-eyebrow">{{ t("按规则改名 · 本地规则") }}</span>
            <h2 id="rename-rules-title">{{ t("给 {n} 项统一改名", { n: renameSheet.entries.length }) }}</h2>
            <p>{{ t("只改名字，不移动位置；生成计划后逐项确认才会执行。") }}</p>
          </div>
          <button class="icon close" :title="t('关闭')" @click="close"><Icon name="close" :size="16" /></button>
        </header>

        <div class="rr-rules">
          <label class="rr-rule">
            <input v-model="strip" type="checkbox" />
            <span><strong>{{ t("去掉标签") }}</strong><small>{{ t("名字两端的字幕组、清晰度等方括号标签；集数和年份保留") }}</small></span>
          </label>
          <div class="rr-rule">
            <input id="rr-pad" v-model="pad" type="checkbox" />
            <label for="rr-pad"><strong>{{ t("集数补零") }}</strong><small>{{ t("E3 → E03，第3集 → 第03集；年份和分辨率不动") }}</small></label>
            <SelectMenu v-slot="{ label }" v-model="width" class="rr-width" :options="widths" :label="t('补到几位')" :disabled="!pad">
              <span>{{ label }}</span>
              <Icon name="chevronDown" :size="14" />
            </SelectMenu>
          </div>
          <div class="rr-rule rr-replace">
            <input id="rr-replace" v-model="replace" type="checkbox" />
            <label for="rr-replace"><strong>{{ t("查找替换") }}</strong><small>{{ t("区分大小写，按原文匹配") }}</small></label>
            <div class="rr-fields">
              <input v-model="find" :disabled="!replace" :placeholder="t('查找')" :aria-label="t('查找')" spellcheck="false" autocomplete="off" />
              <Icon name="arrowRight" :size="14" />
              <input v-model="replaceWith" :disabled="!replace" :placeholder="t('替换为（可留空）')" :aria-label="t('替换为')" spellcheck="false" autocomplete="off" />
            </div>
          </div>
        </div>

        <div class="rr-preview" aria-live="polite">
          <div class="rr-preview-head">
            <strong>{{ t("预览") }}</strong>
            <span>{{ changed ? t("{n} 项会改名", { n: changed }) : t("没有名字会改变") }}<template v-if="changed < renameSheet.entries.length"> · {{ t("{n} 项不变", { n: renameSheet.entries.length - changed }) }}</template></span>
          </div>
          <ul>
            <li v-for="r in preview" :key="r.entry.id" :class="{ same: r.name === r.entry.name }">
              <span class="rr-before" :title="r.entry.name">{{ r.entry.name }}</span>
              <span class="rr-after" :title="r.name">{{ r.name === r.entry.name ? t("不变") : `→ ${r.name}` }}</span>
            </li>
          </ul>
          <p v-if="renameSheet.entries.length > PREVIEW" class="rr-more">{{ t("其余 {n} 项在计划里查看", { n: renameSheet.entries.length - PREVIEW }) }}</p>
        </div>

        <footer>
          <button @click="close">{{ t("取消") }}</button>
          <button class="primary" :disabled="!changed" @click="generate">{{ t("生成计划") }}</button>
        </footer>
      </section>
    </div>
  </Teleport>
</template>

<style scoped>
.rename-sheet {
  width: min(600px, calc(100vw - 48px));
  max-height: calc(100dvh - 48px);
  overflow-y: auto;
  gap: 18px;
  padding: 26px 30px;
}

header {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: 12px;
}

.titles {
  min-width: 0;
}

.rr-eyebrow {
  color: var(--accent);
  font-size: 12px;
  font-weight: 650;
}

header h2 {
  margin: 6px 0 0;
  font-size: 24px;
  font-weight: 650;
  line-height: 1.3;
}

header p {
  margin: 6px 0 0;
  color: var(--fg-dim);
  font-size: 13px;
}

.close {
  flex: none;
  width: 40px;
  height: 40px;
  min-height: 40px;
}

.rr-rules {
  display: grid;
  gap: 8px;
}

.rr-rule {
  display: grid;
  grid-template-columns: auto minmax(0, 1fr) auto;
  align-items: center;
  gap: 12px;
  padding: 12px 14px;
  border: 1px solid var(--line);
  border-radius: var(--radius-card);
  background: color-mix(in srgb, var(--canvas) 60%, transparent);
}

.rr-rule > span,
.rr-rule > label {
  min-width: 0;
}

.rr-rule strong,
.rr-rule small {
  display: block;
}

.rr-rule strong {
  font-size: 14px;
  font-weight: 600;
}

.rr-rule small {
  margin-top: 2px;
  color: var(--fg-dim);
  font-size: 12px;
}

.rr-rule input[type="checkbox"] {
  width: 16px;
  height: 16px;
  margin: 0;
  padding: 0;
  accent-color: var(--accent);
}

.rr-width {
  display: inline-flex;
  align-items: center;
  gap: 8px;
  min-height: 32px;
  padding: 0 8px 0 10px;
  border: 1px solid var(--line-strong);
  border-radius: 8px;
  background: var(--canvas);
  font-size: 13px;
  white-space: nowrap;
}

.rr-width[aria-disabled="true"] {
  opacity: 0.5;
}

.rr-width > svg {
  flex: none;
  opacity: 0.8;
}

.rr-fields {
  grid-column: 2 / -1;
  display: flex;
  align-items: center;
  gap: 8px;
  color: var(--fg-dim);
}

.rr-fields input {
  flex: 1;
  min-width: 0;
  padding: 7px 10px;
  font-size: 13px;
}

.rr-preview {
  border: 1px solid var(--line);
  border-radius: var(--radius-card);
  background: var(--canvas);
  overflow: hidden;
}

.rr-preview-head {
  display: flex;
  align-items: baseline;
  justify-content: space-between;
  gap: 12px;
  padding: 10px 14px;
  border-bottom: 1px solid var(--line);
  font-size: 13px;
}

.rr-preview-head span {
  color: var(--fg-dim);
  font-size: 12px;
}

.rr-preview ul {
  margin: 0;
  padding: 4px 0;
  list-style: none;
}

.rr-preview li {
  display: grid;
  grid-template-columns: minmax(0, 1fr) minmax(0, 1fr);
  gap: 12px;
  padding: 6px 14px;
  font-size: 12px;
}

.rr-before,
.rr-after {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.rr-before {
  color: var(--fg-dim);
}

.rr-after {
  color: var(--fg);
  font-weight: 600;
}

li.same .rr-after {
  color: var(--fg-dim);
  font-weight: 400;
}

.rr-more {
  margin: 0;
  padding: 6px 14px 10px;
  color: var(--fg-dim);
  font-size: 12px;
}

footer {
  display: flex;
  justify-content: flex-end;
  gap: 10px;
}

footer button {
  min-width: 112px;
  min-height: 44px;
  padding: 0 22px;
  font-size: 15px;
  font-weight: 600;
}

@media (max-width: 550px) {
  .rename-sheet {
    padding: 22px 18px;
  }

  .rr-preview li {
    grid-template-columns: minmax(0, 1fr);
    gap: 2px;
  }
}
</style>
