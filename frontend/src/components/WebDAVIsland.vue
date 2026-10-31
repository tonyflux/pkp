<script setup lang="ts">
// WebDAV Client, the right island of Settings. The
// account switch and the mount points come first: without them nothing can
// connect. Each issued credential is a card, masked until asked for. Advanced options
// opens what the server keeps per credential — media optimisation, read-only
// and the listing size — and every change names the client it applied to.
import { computed, nextTick, ref, watch } from "vue";
import Icon from "./Icon.vue";
import ConfirmInline from "./ConfirmInline.vue";
import SettingsIsland from "./SettingsIsland.vue";
import Toggle from "./Toggle.vue";
import { ago, bytes, day } from "../format";
import { t } from "../i18n";
import {
  addWebDAVClient,
  connections,
  copyText,
  notify,
  removeWebDAVClient,
  renewWebDAVPassword,
  setWebDAVEnabled,
  updateWebDAVClient,
} from "../store";
import type { WebDAVClient, WebDAVClientOptions } from "../../bindings/github.com/tonyflux/pkp/internal/services";

defineEmits<{ close: [] }>();

const webdav = computed(() => connections.webdav);
const clients = computed(() => webdav.value?.clients ?? []);
const revealed = ref<Record<string, boolean>>({});
const menuFor = ref("");
const confirming = ref<{ username: string; action: "remove" | "renew" } | null>(null);
const renaming = ref("");
const renameText = ref("");
const adding = ref(false);
const newName = ref("");
// Open from the start, as in the mock: the options are per client, and the
// highlighted card says which one they act on.
const advanced = ref(true);
const legacyOpen = ref(false);
const entriesText = ref("");
const addInput = ref<HTMLInputElement | null>(null);

watch(adding, (on) => {
  if (on) void nextTick(() => addInput.value?.focus());
});

// The client Advanced settings acts on: the one picked, else the first.
const current = computed(
  () => clients.value.find((c) => c.username === connections.selectedClient) ?? clients.value[0] ?? null,
);

// The listing-size field shows the current client's value until edited.
watch(
  () => [current.value?.username, current.value?.maxEntries],
  () => (entriesText.value = String(current.value?.maxEntries || 10000)),
  { immediate: true },
);

const host = (url: string) => url.replace(/^https?:\/\//, "");
const mask = (secret: string) => "•".repeat(Math.min(10, Math.max(7, secret.length)));

function downstream(client: WebDAVClient): number {
  return connections.quota?.sessions?.[client.username]?.downstream.used ?? 0;
}

function pick(client: WebDAVClient) {
  connections.selectedClient = client.username;
}

function closeMenus() {
  menuFor.value = "";
}

function startRename(client: WebDAVClient) {
  closeMenus();
  renaming.value = client.username;
  renameText.value = client.name;
  void nextTick(() => (document.querySelector("[data-rename]") as HTMLInputElement | null)?.select());
}

async function commitRename(client: WebDAVClient) {
  const name = renameText.value.trim();
  renaming.value = "";
  if (!name || name === client.name) return;
  if (await update(client, { name })) notify(t("客户端已改名为「{name}」", { name }), "success");
}

async function submitAdd() {
  const name = newName.value.trim();
  if (!name) return;
  const client = await addWebDAVClient(name);
  if (!client) return;
  adding.value = false;
  newName.value = "";
  // The new password is the one thing the user came for.
  revealed.value[client.username] = true;
}

function update(client: WebDAVClient, change: Partial<WebDAVClientOptions>) {
  return updateWebDAVClient(client, {
    name: client.name,
    readOnly: client.readOnly,
    mediaOptimized: client.mediaOptimized,
    maxEntries: client.maxEntries || 10000,
    ...change,
  });
}

async function setOption(key: "readOnly" | "mediaOptimized", on: boolean) {
  const client = current.value;
  if (!client) return;
  if (await update(client, { [key]: on })) {
    const name = client.name;
    const text =
      key === "readOnly"
        ? on
          ? t("已为「{name}」开启只读模式", { name })
          : t("已为「{name}」关闭只读模式", { name })
        : on
          ? t("已为「{name}」开启媒体播放优化", { name })
          : t("已为「{name}」关闭媒体播放优化", { name });
    notify(text, "success");
  }
}

async function commitEntries() {
  const client = current.value;
  if (!client) return;
  const value = Number(entriesText.value);
  if (value === (client.maxEntries || 10000)) return;
  if (!Number.isInteger(value) || value < 1000 || value > 999999) {
    notify(t("回复的最大条数要在 1000 到 999999 之间"), "error");
    entriesText.value = String(client.maxEntries || 10000);
    return;
  }
  if (await update(client, { maxEntries: value })) {
    notify(t("「{name}」每次最多返回 {n} 条", { name: client.name, n: value }), "success");
  } else {
    entriesText.value = String(client.maxEntries || 10000);
  }
}

function toggleAdvanced() {
  advanced.value = !advanced.value;
}

async function confirmAction(client: WebDAVClient) {
  const action = confirming.value?.action;
  confirming.value = null;
  if (action === "remove") await removeWebDAVClient(client);
  if (action === "renew") {
    await renewWebDAVPassword(client);
    revealed.value[client.username] = true;
  }
}
</script>

<template>
  <SettingsIsland
    :title="t('WebDAV 客户端')"
    icon="globe"
    :lead="t('管理已连接的 WebDAV 客户端，控制其访问权限和使用情况。')"
    @close="$emit('close')"
    @click="closeMenus"
  >
    <template v-if="webdav">
      <div class="well glass-well">
        <div class="well-row">
          <div class="well-text">
            <strong>{{ t("启用 WebDAV") }}</strong>
            <small>{{ !webdav.premium ? t("会员功能，当前账号无法启用") : webdav.enabled ? t("客户端可以用下面的地址连接") : t("已停用，所有客户端暂时无法连接") }}</small>
          </div>
          <Toggle :model-value="webdav.enabled" :disabled="!webdav.premium" :label="t('启用 WebDAV')" @update:model-value="setWebDAVEnabled" />
        </div>
        <template v-if="webdav.enabled">
          <div class="well-row">
            <div class="well-text">
              <small>{{ t("连接点") }}</small>
              <span class="mono">{{ host(webdav.endpoint) }}</span>
            </div>
            <button class="quiet icon" :title="t('复制连接点')" @click="copyText(webdav.endpoint, t('连接点'))"><Icon name="copy" :size="16" /></button>
          </div>
          <button class="disclose" :aria-expanded="legacyOpen" @click="legacyOpen = !legacyOpen">
            {{ t("备用接入点") }}
            <Icon :name="legacyOpen ? 'chevronUp' : 'chevronDown'" :size="15" />
          </button>
          <div v-if="legacyOpen" class="well-row">
            <div class="well-text">
              <small>{{ t("旧版接入点 · 连不上主接入点时试试这个") }}</small>
              <span class="mono">{{ host(webdav.legacyEndpoint) }}</span>
            </div>
            <button class="quiet icon" :title="t('复制旧版接入点')" @click="copyText(webdav.legacyEndpoint, t('旧版接入点'))"><Icon name="copy" :size="16" /></button>
          </div>
        </template>
      </div>
      <p class="hint">{{ t("支持 HTTP（80 端口）和 HTTPS（443 端口），暂不支持上传。") }}</p>
    </template>

    <div
      v-for="client in clients"
      :key="client.username"
      class="client"
      :class="{ on: advanced && current?.username === client.username }"
      @click="pick(client)"
    >
      <span class="device"><Icon name="laptop" :size="24" /></span>
      <div class="client-text">
        <input
          v-if="renaming === client.username"
          v-model="renameText"
          data-rename
          class="rename"
          :aria-label="t('客户端名称')"
          @click.stop
          @keydown.enter="commitRename(client)"
          @keydown.esc.prevent="renaming = ''"
          @blur="commitRename(client)"
        />
        <strong v-else :title="client.name">{{ client.name || client.username }}</strong>
        <small :title="client.lastActiveAt ? t('最后活跃：{time}', { time: day(client.lastActiveAt) }) : t('从未连接')">
          {{ t("创建于 {date}", { date: day(client.createdAt) }) }}<template v-if="downstream(client)"> · {{ t("本月下行 {size}", { size: bytes(downstream(client), "0 B", 1) }) }}</template>
        </small>
        <div v-if="!revealed[client.username]" class="secret">
          <span class="dots">{{ mask(client.password) }}</span>
          <button class="quiet icon tiny" :title="t('查看账号密码')" @click.stop="revealed[client.username] = true"><Icon name="eye" :size="16" /></button>
        </div>
        <div v-else class="creds" @click.stop>
          <div class="cred">
            <small>{{ t("账号") }}</small>
            <span class="mono">{{ client.username }}</span>
            <button class="quiet icon tiny" :title="t('复制账号')" @click="copyText(client.username, t('账号'))"><Icon name="copy" :size="14" /></button>
          </div>
          <div class="cred">
            <small>{{ t("密码") }}</small>
            <span class="mono">{{ client.password }}</span>
            <button class="quiet icon tiny" :title="t('复制密码')" @click="copyText(client.password, t('密码'))"><Icon name="copy" :size="14" /></button>
          </div>
          <button class="quiet hide" @click="revealed[client.username] = false"><Icon name="eyeOff" :size="14" />{{ t("隐藏") }}</button>
        </div>
      </div>
      <div class="menu-anchor" @click.stop>
        <button class="quiet icon more" :title="t('更多')" :aria-expanded="menuFor === client.username" @click="menuFor = menuFor === client.username ? '' : client.username">
          <Icon name="more" :size="22" />
        </button>
        <div v-if="menuFor === client.username" class="menu glass glass-sheet">
          <button class="quiet" @click="startRename(client)">{{ t("重命名") }}</button>
          <button class="quiet" @click="closeMenus(); confirming = { username: client.username, action: 'renew' }">{{ t("更换密码") }}</button>
          <button class="quiet danger" @click="closeMenus(); confirming = { username: client.username, action: 'remove' }">{{ t("删除客户端") }}</button>
        </div>
      </div>
      <ConfirmInline
        v-if="confirming?.username === client.username"
        :message="confirming.action === 'remove' ? t('删除后，用这组账号密码登录的客户端将无法连接。') : t('旧密码立即失效，记着它的客户端要换成新密码才能再连接。')"
        :confirm-label="confirming.action === 'remove' ? t('删除客户端') : t('更换密码')"
        :danger="confirming.action === 'remove'"
        @cancel="confirming = null"
        @confirm="confirmAction(client)"
      />
    </div>

    <p v-if="webdav && !clients.length" class="empty">{{ t("还没有客户端。添加一个，就能拿到连接用的账号和密码。") }}</p>

    <form v-if="adding" class="add-form" @submit.prevent="submitAdd">
      <input ref="addInput" v-model="newName" type="text" :placeholder="t('客户端名称，如 Infuse')" :aria-label="t('客户端名称')" @keydown.esc.prevent="adding = false" />
      <div class="add-actions">
        <button type="button" class="quiet" @click="adding = false">{{ t("取消") }}</button>
        <button type="submit" class="primary" :disabled="!newName.trim()">{{ t("添加") }}</button>
      </div>
    </form>
    <button v-else class="primary big" :disabled="!webdav?.premium" @click="adding = true; newName = ''">
      <Icon name="plus" :size="20" />
      {{ t("添加客户端") }}
    </button>

    <button v-if="clients.length" class="secondary big" :aria-expanded="advanced" @click="toggleAdvanced">
      <Icon name="settings" :size="20" />
      {{ t("高级选项") }}
    </button>

    <section v-if="advanced && current" class="advanced">
      <h3>{{ t("高级设置") }}</h3>
      <p class="scope">{{ t("只作用于「{name}」，点上面的客户端可以切换。", { name: current.name || current.username }) }}</p>

      <div class="opt">
        <Icon name="play" :size="22" />
        <div class="opt-text">
          <div class="opt-head">
            <strong>{{ t("优化媒体播放") }}</strong>
            <Toggle :model-value="current.mediaOptimized" :label="t('优化媒体播放')" @update:model-value="setOption('mediaOptimized', $event)" />
          </div>
          <span>{{ t("针对视频播放进行连接优化，提升流畅度。") }}</span>
        </div>
      </div>

      <div class="opt">
        <Icon name="lock" :size="22" />
        <div class="opt-text">
          <div class="opt-head">
            <strong>{{ t("只读模式") }}</strong>
            <Toggle :model-value="current.readOnly" :label="t('只读模式')" @update:model-value="setOption('readOnly', $event)" />
          </div>
          <span>{{ t("客户端仅可读取文件，无法进行修改或删除。") }}</span>
        </div>
      </div>

      <div class="opt">
        <Icon name="list" :size="22" />
        <div class="opt-text">
          <div class="opt-head">
            <strong>{{ t("回复的最大条数") }}</strong>
            <input
              v-model="entriesText"
              class="entries mono"
              type="text"
              inputmode="numeric"
              :aria-label="t('回复的最大条数')"
              @keydown.enter="($event.target as HTMLInputElement).blur()"
              @blur="commitEntries"
            />
          </div>
          <span>{{ t("一次列目录最多返回的条目，1000–999999，默认 10000。设得太大可能让请求超时。") }}</span>
        </div>
      </div>
      <p v-if="current.lastActiveAt" class="scope">{{ t("最后连接：{time}", { time: ago(current.lastActiveAt) }) }}<template v-if="current.lastActiveIp"> · {{ current.lastActiveIp }}</template></p>
    </section>
  </SettingsIsland>
</template>

<style scoped>
.well {
  display: flex;
  flex-direction: column;
  padding: 4px 6px 4px 14px;
  border-radius: 16px;
}

/* The padding only shows once a longer language wraps the text to more
   lines than the row's height holds. */
.well-row {
  display: flex;
  align-items: center;
  gap: 10px;
  min-height: 52px;
  padding: 6px 0;
}

.well-row + .well-row,
.disclose + .well-row {
  border-top: 1px solid var(--line);
}

.well-text {
  flex: 1;
  min-width: 0;
  display: flex;
  flex-direction: column;
  gap: 2px;
}

.well-text strong {
  font-size: 14px;
  font-weight: 600;
}

.well-text small {
  font-size: 12px;
  color: var(--fg-dim);
}

.well-text .mono {
  font-size: 13px;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.disclose {
  justify-content: flex-start;
  min-height: 34px;
  padding: 0;
  gap: 4px;
  border: none;
  border-top: 1px solid var(--line);
  border-radius: 0;
  background: none;
  color: var(--fg-dim);
  font-size: 12px;
}

.disclose:hover:not(:disabled) {
  background: none;
  color: var(--fg);
}

.hint,
.empty {
  margin: -2px 2px 0;
  font-size: 12px;
  line-height: 1.5;
  color: var(--fg-dim);
}

/* ---------- client cards ---------- */

.client {
  position: relative;
  display: flex;
  align-items: flex-start;
  flex-wrap: wrap;
  gap: 14px;
  padding: 16px 12px 16px 16px;
  border-radius: 16px;
  border: 1px solid var(--line);
  background: color-mix(in srgb, var(--fg) 4%, transparent);
  cursor: default;
  transition: border-color var(--dur) var(--ease), background var(--dur) var(--ease);
}

.client.on {
  border-color: color-mix(in srgb, var(--accent) 70%, transparent);
  background: var(--accent-soft);
}

.device {
  flex: none;
  padding-top: 1px;
  color: var(--accent);
}

.client-text {
  flex: 1;
  min-width: 0;
  display: flex;
  flex-direction: column;
  gap: 6px;
}

.client-text strong {
  font-size: 15px;
  font-weight: 600;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.client-text small {
  font-size: 13px;
  color: var(--fg-dim);
}

.rename {
  min-height: 30px;
  padding: 3px 8px;
  border-radius: 8px;
  font-size: 14px;
  font-weight: 600;
}

.secret {
  display: flex;
  align-items: center;
  gap: 8px;
  margin-top: 2px;
}

.dots {
  font-size: 15px;
  letter-spacing: 0.12em;
  color: var(--fg);
}

.tiny {
  width: 28px;
  height: 28px;
  min-height: 28px;
}

.creds {
  display: flex;
  flex-direction: column;
  gap: 6px;
  margin-top: 2px;
}

/* The label over its value: the card's text column is too narrow for a
   label column beside a username in any language. */
.cred {
  display: grid;
  grid-template-columns: minmax(0, 1fr) auto;
  align-items: center;
  column-gap: 8px;
}

.cred small {
  grid-column: 1 / -1;
  font-size: 12px;
}

.cred .mono {
  min-width: 0;
  font-size: 13px;
  -webkit-user-select: text;
  user-select: text;
  overflow-wrap: anywhere;
}

.hide {
  align-self: flex-start;
  min-height: 26px;
  margin-right: -2px;
  padding: 0 10px 0 6px;
  gap: 4px;
  font-size: 12px;
}

.menu-anchor {
  position: relative;
  flex: none;
  margin-top: -6px;
}

.more {
  width: 32px;
  height: 32px;
  min-height: 32px;
  color: var(--fg);
}

/* The mock's ··· reads as three solid dots, not hairline specks. */
.more .icon {
  stroke-width: 3;
}

.menu {
  position: absolute;
  right: 0;
  top: calc(100% + 4px);
  min-width: 136px;
}

/* The confirm sits under the whole card row, not beside the credentials. */
.confirm {
  flex-basis: 100%;
}

/* ---------- actions ---------- */

.big {
  min-height: 54px;
  gap: 10px;
  font-size: 16px;
  font-weight: 600;
}

.secondary {
  border-color: color-mix(in srgb, var(--glass-edge) 70%, transparent);
  background: var(--glass-control);
  box-shadow: inset 0 1px 0 color-mix(in srgb, var(--glass-rim) 60%, transparent);
}

.secondary[aria-expanded="true"] {
  border-color: color-mix(in srgb, var(--accent) 60%, transparent);
}

.add-form {
  display: flex;
  flex-direction: column;
  gap: 10px;
  padding: 14px;
  border-radius: 16px;
  border: 1px solid var(--line);
  background: color-mix(in srgb, var(--fg) 4%, transparent);
}

.add-form input {
  min-height: 42px;
}

.add-actions {
  display: flex;
  justify-content: flex-end;
  gap: 8px;
}

/* ---------- Advanced settings ---------- */

.advanced {
  display: flex;
  flex-direction: column;
  margin-top: 6px;
  padding-top: 18px;
  border-top: 1px solid var(--line);
}

.advanced h3 {
  margin: 0;
  font-size: 16px;
  font-weight: 600;
}

.scope {
  margin: 4px 0 6px;
  font-size: 12px;
  line-height: 1.5;
  color: var(--fg-dim);
}

.opt {
  display: flex;
  align-items: flex-start;
  gap: 14px;
  padding: 16px 0;
}

.opt + .opt {
  border-top: 1px solid var(--line);
}

.opt > .icon {
  margin-top: 1px;
  color: var(--fg);
}

.opt-text {
  flex: 1;
  min-width: 0;
  display: flex;
  flex-direction: column;
  gap: 5px;
}

.opt-text strong {
  font-size: 15px;
  font-weight: 600;
}

.opt-text span {
  font-size: 13px;
  line-height: 1.55;
  color: var(--fg-dim);
}

.opt-head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 10px;
}

.entries {
  flex: none;
  width: 84px;
  min-height: 34px;
  padding: 4px 8px;
  border-radius: 10px;
  font-size: 13px;
  text-align: right;
}
</style>
