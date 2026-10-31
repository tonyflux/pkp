<script setup lang="ts">
// Create personal access token, laid out like the official form: a required label, a
// lifetime from 30 days to a year, and the permissions. File access is one
// level — each includes the ones before it — while services and the account
// are picked one by one. Permanent deletion and invitations are left to OAuth
// apps: the server does not grant them to a personal token.
import { computed, onMounted, ref } from "vue";
import { createAccessToken } from "../store";
import type { AccessToken } from "../../bindings/github.com/tonyflux/pkp/internal/services";
import { t, N_ } from "../i18n";

const props = defineProps<{ activeCount: number }>();
const emit = defineEmits<{ created: [token: AccessToken, label: string]; cancel: [] }>();

const MAX_TOKENS = 20;

const lifetimes = [
  { seconds: 30 * 86400, label: N_("30 天") },
  { seconds: 60 * 86400, label: N_("60 天") },
  { seconds: 90 * 86400, label: N_("90 天") },
  { seconds: 180 * 86400, label: N_("180 天") },
  { seconds: 365 * 86400, label: N_("1 年") },
];

const levels = [
  { scopes: ["files:read"], label: N_("读取文件"), desc: N_("查看并下载你的文件") },
  { scopes: ["files:read", "files:write"], label: N_("读取和写入文件"), desc: N_("查看、下载、上传并整理你的文件") },
  { scopes: ["files:read", "files:write", "files:manage"], label: N_("管理文件"), desc: N_("完整文件管理（含移入回收站）") },
];

const extras = [
  { scope: "cloud_download", label: N_("云添加"), desc: N_("创建云添加任务") },
  { scope: "share", label: N_("分享"), desc: N_("创建并管理分享链接") },
  { scope: "account", label: N_("账号设置"), desc: N_("查看并修改你的资料与偏好设置") },
];

const label = ref("");
const lifetime = ref(90 * 86400);
const level = ref(0);
const picked = ref<string[]>([]);
const busy = ref(false);
const labelInput = ref<HTMLInputElement | null>(null);

const full = computed(() => props.activeCount >= MAX_TOKENS);
const ready = computed(() => label.value.trim() !== "" && !full.value && !busy.value);

function togglePick(scope: string) {
  picked.value = picked.value.includes(scope) ? picked.value.filter((s) => s !== scope) : [...picked.value, scope];
}

async function submit() {
  if (!ready.value) return;
  busy.value = true;
  try {
    const scopes = [...levels[level.value].scopes, ...extras.map((e) => e.scope).filter((s) => picked.value.includes(s))];
    const token = await createAccessToken({ label: label.value.trim(), scopes, lifetime: lifetime.value });
    if (token) emit("created", token, label.value.trim());
  } finally {
    busy.value = false;
  }
}

onMounted(() => labelInput.value?.focus());
</script>

<template>
  <form class="form" @submit.prevent="submit" @keydown.esc.prevent="emit('cancel')">
    <h3>{{ t("创建个人访问令牌") }}</h3>
    <p v-if="full" class="warn">{{ t("你已有 {n} 个有效令牌。先移除一个不再使用的，再创建新的。", { n: MAX_TOKENS }) }}</p>

    <label class="field">
      <span>{{ t("标签") }} <em>*</em></span>
      <input ref="labelInput" v-model="label" type="text" maxlength="100" :placeholder="t('用途，如备份脚本')" />
    </label>

    <div class="field">
      <span>{{ t("过期时间") }}</span>
      <div class="chips" role="radiogroup" :aria-label="t('过期时间')">
        <button
          v-for="l in lifetimes"
          :key="l.seconds"
          type="button"
          role="radio"
          :aria-checked="lifetime === l.seconds"
          :class="{ on: lifetime === l.seconds }"
          @click="lifetime = l.seconds"
        >
          {{ t(l.label) }}
        </button>
      </div>
    </div>

    <div class="field">
      <span>{{ t("文件") }}</span>
      <div class="options" role="radiogroup" :aria-label="t('文件权限')">
        <button
          v-for="(l, i) in levels"
          :key="l.label"
          type="button"
          class="option"
          role="radio"
          :aria-checked="level === i"
          :class="{ on: level === i }"
          @click="level = i"
        >
          <i class="mark radio" />
          <span class="option-text">
            <strong>{{ t(l.label) }}</strong>
            <small>{{ t(l.desc) }}</small>
          </span>
        </button>
      </div>
    </div>

    <div class="field">
      <span>{{ t("服务与账号") }}</span>
      <div class="options">
        <button
          v-for="e in extras"
          :key="e.scope"
          type="button"
          class="option"
          role="checkbox"
          :aria-checked="picked.includes(e.scope)"
          :class="{ on: picked.includes(e.scope) }"
          @click="togglePick(e.scope)"
        >
          <i class="mark box" />
          <span class="option-text">
            <strong>{{ t(e.label) }}</strong>
            <small>{{ t(e.desc) }}</small>
          </span>
        </button>
      </div>
    </div>

    <p class="note">{{ t("需要永久删除或邀请？请改用通过浏览器授权的应用。") }}</p>

    <div class="actions">
      <button type="button" class="quiet" @click="emit('cancel')">{{ t("取消") }}</button>
      <button type="submit" class="primary" :class="{ working: busy }" :disabled="!ready">{{ busy ? t("正在创建…") : t("button|创建令牌") }}</button>
    </div>
  </form>
</template>

<style scoped>
.form {
  display: flex;
  flex-direction: column;
  gap: 14px;
  padding: 16px;
  border-radius: 16px;
  border: 1px solid var(--line);
  background: color-mix(in srgb, var(--fg) 4%, transparent);
}

h3 {
  margin: 0;
  font-size: 16px;
  font-weight: 600;
}

.warn {
  margin: 0;
  font-size: 13px;
  line-height: 1.5;
  color: var(--warning);
}

.field {
  display: flex;
  flex-direction: column;
  gap: 8px;
}

.field > span {
  font-size: 13px;
  font-weight: 600;
  color: var(--fg-dim);
}

.field em {
  font-style: normal;
  color: var(--danger);
}

.field input {
  min-height: 40px;
}

.chips {
  display: flex;
  flex-wrap: wrap;
  gap: 6px;
}

.chips button {
  min-height: 32px;
  padding: 0 12px;
  border-radius: var(--radius-pill);
  font-size: 13px;
}

.chips button.on {
  border-color: transparent;
  background: var(--primary) border-box;
  color: var(--on-primary);
  font-weight: 600;
}

.options {
  display: flex;
  flex-direction: column;
  gap: 2px;
}

.option {
  justify-content: flex-start;
  align-items: flex-start;
  gap: 10px;
  min-height: 0;
  padding: 8px 10px;
  border-color: transparent;
  border-radius: 12px;
  background: none;
  text-align: left;
  white-space: normal;
}

.option:hover:not(:disabled) {
  border-color: transparent;
  background: color-mix(in srgb, var(--fg) 6%, transparent);
}

.option-text {
  display: flex;
  flex-direction: column;
  gap: 2px;
}

.option-text strong {
  font-size: 14px;
  font-weight: 600;
}

.option-text small {
  font-size: 12px;
  color: var(--fg-dim);
}

.mark {
  flex: none;
  width: 18px;
  height: 18px;
  margin-top: 1px;
  border: 1.5px solid var(--line-strong);
  transition: background var(--dur) var(--ease), border-color var(--dur) var(--ease);
}

.mark.radio {
  border-radius: 50%;
}

.mark.box {
  border-radius: 5px;
}

.option.on .mark {
  border-color: transparent;
  background: var(--primary) border-box;
  box-shadow: inset 0 0 0 4px color-mix(in srgb, var(--canvas-flat) 85%, transparent);
}

.option.on .mark.box {
  box-shadow: inset 0 0 0 3px color-mix(in srgb, var(--canvas-flat) 85%, transparent);
}

.note {
  margin: 0;
  font-size: 12px;
  line-height: 1.5;
  color: var(--fg-dim);
}

.actions {
  display: flex;
  flex-wrap: wrap;
  justify-content: flex-end;
  gap: 8px;
}
</style>
