<script setup lang="ts">
// Create share: the file comes first, then
// the lifetime and the access code, and only then the create button. No
// link is shown before the server minted one; once it has, the sheet turns
// into the link with its copy action. Closing before that creates nothing.
import { computed, ref, watch } from "vue";
import Icon from "./Icon.vue";
import Orbit from "./Orbit.vue";
import SelectMenu, { type SelectOption } from "./SelectMenu.vue";
import Thumb from "./Thumb.vue";
import Toggle from "./Toggle.vue";
import { bytes } from "../format";
import { t } from "../i18n";
import { closeCreateShare, copyShareLink, createShare, shares } from "../store";

type Lifetime = "forever" | "7" | "14" | "30" | "custom";

const lifetimes: SelectOption<Lifetime>[] = [
  { value: "forever", get label() { return t("永久有效"); } },
  { value: "7", get label() { return t("{n} 天", { n: 7 }); } },
  { value: "14", get label() { return t("{n} 天", { n: 14 }); } },
  { value: "30", get label() { return t("{n} 天", { n: 30 }); } },
  { value: "custom", get label() { return t("自定义日期"); } },
];

const lifetime = ref<Lifetime>("forever");
const customDate = ref("");
const passCode = ref(false);
const customCode = ref("");

// Every open starts from the same choices: these are not remembered
// preferences, they are decisions about this one link.
watch(
  () => shares.targets,
  () => {
    lifetime.value = "forever";
    customDate.value = "";
    passCode.value = false;
    customCode.value = "";
  },
);

const targets = computed(() => shares.targets);
// The first entry stands for the set in the header; a set says how many.
const entry = computed(() => targets.value[0] ?? null);
const title = computed(() =>
  targets.value.length > 1 ? t("{name} 等 {n} 项", { name: entry.value?.name ?? "", n: targets.value.length }) : (entry.value?.name ?? ""),
);
const caption = computed(() => {
  const list = targets.value;
  if (list.length === 1) return list[0].folder ? t("文件夹") : bytes(list[0].size);
  const folders = list.filter((e) => e.folder).length;
  const files = list.length - folders;
  const parts: string[] = [];
  if (folders) parts.push(t("{n} 个文件夹", { n: folders }));
  if (files) parts.push(t("{n} 个文件 · {size}", { n: files, size: bytes(list.filter((e) => !e.folder).reduce((sum, e) => sum + e.size, 0)) }));
  return parts.join(" · ");
});

// Tomorrow is the earliest custom date that makes sense.
const minDate = computed(() => {
  const d = new Date();
  d.setDate(d.getDate() + 1);
  return d.toISOString().slice(0, 10);
});

const customValid = computed(() => lifetime.value !== "custom" || (customDate.value !== "" && customDate.value >= minDate.value));

// Empty lets the server mint one. A typed code must be 4 letters or digits,
// the shape of every code the server has returned.
const typedCode = computed(() => (passCode.value ? customCode.value.trim() : ""));
const codeOk = computed(() => typedCode.value === "" || /^[0-9A-Za-z]{4}$/.test(typedCode.value));
// A short letter-or-digit prefix is still being typed. A bad character, or
// four characters that still don't fit, is an error.
const codeBad = computed(() => typedCode.value !== "" && !/^[0-9A-Za-z]{0,3}$/.test(typedCode.value) && !codeOk.value);
const codeHint = computed(() => {
  if (!passCode.value) return t("任何人打开链接都可以访问");
  if (!typedCode.value) return t("留空则由 PikPak 生成 4 位提取码");
  if (!codeOk.value) return t("提取码须为 4 位字母或数字");
  return t("访问者需要输入提取码");
});

const summary = computed(() => {
  const when =
    lifetime.value === "forever"
      ? t("链接永久有效")
      : lifetime.value === "custom"
        ? customDate.value
          ? t("链接到 {date} 失效", { date: customDate.value })
          : t("请选择失效日期")
        : t("链接 {n} 天后失效", { n: Number(lifetime.value) });
  const access = !passCode.value
    ? t("任何人打开链接都可以访问。")
    : typedCode.value && codeOk.value
      ? t("访问者需输入提取码 {code}。", { code: typedCode.value })
      : t("访问者需输入提取码。");
  return t("创建后生成链接，{when}；{access}", { when, access });
});

async function submit() {
  if (!targets.value.length || !customValid.value || !codeOk.value || shares.creating) return;
  const req = {
    fileIds: targets.value.map((e) => e.id),
    expirationDays: lifetime.value === "forever" || lifetime.value === "custom" ? 0 : Number(lifetime.value),
    // End of the chosen day, local time.
    expiresAt: lifetime.value === "custom" ? new Date(`${customDate.value}T23:59:59`).toISOString() : "",
    passCode: passCode.value,
    customPassCode: typedCode.value,
  };
  await createShare(req);
}
</script>

<template>
  <Teleport to="body">
    <div v-if="entry" class="scrim" @click.self="closeCreateShare">
      <section class="sheet glass glass-sheet" role="dialog" :aria-label="t('创建分享')">
        <header>
          <h2>{{ shares.created ? t("分享已创建") : t("创建分享") }}</h2>
          <button class="icon close" :title="t('关闭')" @click="closeCreateShare"><Icon name="close" :size="16" /></button>
        </header>

        <div class="file">
          <span class="thumb">
            <Thumb v-if="entry.thumbnail" :src="entry.thumbnail" :alt="entry.name" />
            <Icon v-else :name="entry.folder ? 'folder' : entry.category" :size="34" :filled="entry.folder" />
            <b v-if="targets.length > 1" class="stack-count">{{ targets.length }}</b>
          </span>
          <div class="file-text">
            <strong :title="title">{{ title }}</strong>
            <span>{{ caption }}</span>
          </div>
        </div>

        <template v-if="!shares.created">
          <div class="fields">
            <div class="field">
              <label>{{ t("有效期") }}</label>
              <div class="control">
                <div class="select glass-well">
                  <SelectMenu v-model="lifetime" class="face" :options="lifetimes" :label="t('有效期')" />
                  <Icon name="chevronDown" :size="16" />
                </div>
                <input v-if="lifetime === 'custom'" v-model="customDate" class="date" type="date" :min="minDate" :aria-label="t('失效日期')" />
                <small>{{ t("可选永久有效、7 天、14 天、30 天或自定义日期") }}</small>
              </div>
            </div>

            <div class="field">
              <label>{{ t("提取码") }}</label>
              <div class="control">
                <Toggle v-model="passCode" :label="t('提取码')" />
                <input
                  v-if="passCode"
                  v-model="customCode"
                  class="code-input glass-well"
                  type="text"
                  maxlength="4"
                  autocomplete="off"
                  autocapitalize="off"
                  spellcheck="false"
                  :placeholder="t('留空则自动生成')"
                  :aria-label="t('自定义提取码')"
                  aria-describedby="share-code-hint"
                  :aria-invalid="codeBad"
                  :disabled="shares.creating"
                  @keydown.enter.prevent="submit"
                />
                <small id="share-code-hint" :class="{ bad: codeBad }">{{ codeHint }}</small>
              </div>
            </div>
          </div>

          <div class="hint glass-well">
            <Icon name="info" :size="17" />
            <span>{{ summary }}</span>
          </div>

          <footer>
            <button @click="closeCreateShare">{{ t("取消") }}</button>
            <button class="primary" :class="{ working: shares.creating }" :disabled="!customValid || !codeOk || shares.creating" @click="submit">
              <Orbit v-if="shares.creating" :size="15" :label="t('创建中')" />
              {{ shares.creating ? t("创建中…") : t("创建分享") }}
            </button>
          </footer>
        </template>

        <template v-else>
          <div class="result">
            <div class="link glass-well">
              <Icon name="link" :size="18" />
              <span class="mono" :title="shares.created.url">{{ shares.created.url }}</span>
            </div>
            <div v-if="shares.created.passCode" class="code glass-well">
              <Icon name="lock" :size="18" />
              <span>{{ t("提取码") }}</span>
              <strong class="mono">{{ shares.created.passCode }}</strong>
            </div>
            <p class="note">
              {{ shares.created.passCode ? t("访问者打开链接后还需输入提取码。") : t("任何人打开链接都可以访问。") }}
              {{ t("可在「我的分享」里查看访问情况或取消分享。") }}
            </p>
          </div>
          <footer>
            <button @click="closeCreateShare">{{ t("完成") }}</button>
            <button class="primary" @click="copyShareLink(shares.created)"><Icon name="copy" :size="16" />{{ shares.created.passCode ? t("复制链接与提取码") : t("复制链接") }}</button>
          </footer>
        </template>
      </section>
    </div>
  </Teleport>
</template>

<style scoped>
.sheet {
  width: min(620px, calc(100vw - 80px));
  max-height: calc(100vh - 100px);
  gap: 18px;
}

header {
  display: flex;
  align-items: center;
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

.close {
  width: 40px;
  height: 40px;
  min-height: 40px;
}

.file {
  display: flex;
  align-items: center;
  gap: 18px;
  padding-bottom: 18px;
  border-bottom: 1px solid var(--line);
}

.thumb {
  position: relative;
  width: 140px;
  aspect-ratio: 16 / 10;
  flex: none;
  border-radius: 12px;
  overflow: hidden;
  display: grid;
  place-items: center;
  background: var(--glass-inset);
  color: var(--accent);
}

.thumb :deep(img) {
  width: 100%;
  height: 100%;
  object-fit: cover;
}

.stack-count {
  position: absolute;
  right: 8px;
  bottom: 8px;
  min-width: 26px;
  height: 26px;
  padding: 0 8px;
  display: grid;
  place-items: center;
  border-radius: 13px;
  background: var(--accent);
  color: #fff;
  font-size: 13px;
  font-weight: 600;
  box-shadow: 0 2px 8px rgba(0, 0, 0, 0.25);
}

.file-text {
  display: flex;
  flex-direction: column;
  gap: 6px;
  min-width: 0;
}

.file-text strong {
  font-size: 17px;
  font-weight: 600;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.file-text span {
  font-size: 14px;
  color: var(--fg-dim);
}

/* One label column for both rows: as wide as the longest label
   (アクセスコード), never narrower than the design's 96px. */
.fields {
  display: grid;
  grid-template-columns: minmax(96px, max-content) minmax(0, 1fr);
  align-items: start;
  gap: 18px 16px;
}

.field {
  display: contents;
}

.field label {
  padding-top: 11px;
  font-size: 15px;
  font-weight: 600;
}

.control {
  display: flex;
  flex-direction: column;
  min-width: 0;
  gap: 8px;
}

.control small {
  font-size: 13px;
  color: var(--fg-dim);
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
  min-height: 46px;
  padding: 0 40px 0 16px;
  line-height: 46px;
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
  color: var(--fg);
  font-size: 15px;
  font-weight: 500;
}

.select :deep(svg) {
  position: absolute;
  right: 14px;
  pointer-events: none;
  color: var(--fg-dim);
}

.date {
  min-height: 42px;
  padding: 0 14px;
  border-radius: 12px;
  border: 1px solid var(--line-strong);
  background: var(--glass-control);
  color: var(--fg);
  font: inherit;
  font-size: 15px;
}

.code-input {
  width: 100%;
  min-width: 0;
  min-height: 48px;
  padding: 0 16px;
  border-radius: 12px;
  font-size: 15px;
}

.code-input::placeholder {
  color: var(--fg-dim);
  opacity: 1;
}

.code-input:not(:placeholder-shown) {
  letter-spacing: 0.16em;
  font-family: ui-monospace, "SF Mono", "Cascadia Mono", Menlo, monospace;
}

.code-input:focus {
  border-color: var(--accent);
}

.code-input[aria-invalid="true"] {
  border-color: var(--danger);
}

.control small.bad {
  color: var(--danger);
}

.hint {
  display: flex;
  align-items: center;
  gap: 12px;
  padding: 14px 16px;
  border-radius: 12px;
  font-size: 14px;
  color: var(--fg-dim);
  text-wrap: pretty;
}

footer {
  display: flex;
  justify-content: flex-end;
  gap: 10px;
  padding-top: 4px;
}

footer button {
  min-height: 46px;
  padding: 0 26px;
  font-size: 15px;
  font-weight: 600;
}

.result {
  display: flex;
  flex-direction: column;
  gap: 10px;
}

.link,
.code {
  display: flex;
  align-items: center;
  gap: 12px;
  padding: 14px 16px;
  border-radius: 12px;
  font-size: 14px;
}

.link span {
  flex: 1;
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.code strong {
  margin-left: auto;
  font-size: 16px;
  letter-spacing: 0.08em;
}

.note {
  margin: 4px 0 0;
  font-size: 13px;
  line-height: 1.5;
  color: var(--fg-dim);
  text-wrap: pretty;
}
</style>
