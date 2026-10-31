<script setup lang="ts">
// The island behind three of the Connections & Integrations cards. MCP explains how an AI tool
// connects and lists the tools that did; Personal Access Tokens issues, regenerates and
// removes tokens, showing a new token exactly once; Connected Apps is the usage
// view — the share of the monthly allowance connected apps draw on, and what
// each app and session drew. Removing a connection ends its access at once.
import { computed, ref } from "vue";
import Icon from "./Icon.vue";
import ConfirmInline from "./ConfirmInline.vue";
import SettingsIsland from "./SettingsIsland.vue";
import TokenForm from "./TokenForm.vue";
import UsageMeter from "./UsageMeter.vue";
import { ago, bytes, day } from "../format";
import { N_, t } from "../i18n";
import { connections, copyText, regenerateAccessToken, revokeSessions } from "../store";
import type { AccessToken, AppSession, ConnectedApp, UsageAxes } from "../../bindings/github.com/tonyflux/pkp/internal/services";

type Kind = "mcp" | "pat" | "apps";

const props = defineProps<{ kind: Kind }>();
const emit = defineEmits<{ close: []; open: [panel: "webdav" | "pat"] }>();

const MCP_URL = "https://pikpak.ai/mcp";

const heads: Record<Kind, { title: string; icon: string; lead: string }> = {
  mcp: { title: N_("MCP 连接 AI 工具"), icon: "chip", lead: N_("让支持 MCP 的 AI 工具直接查找、整理和云添加你的 PikPak 文件。") },
  pat: { title: N_("island|个人访问令牌"), icon: "key", lead: N_("用于你的 CLI、脚本或无人值守自动化。令牌只在创建时显示一次。") },
  apps: { title: N_("已连接应用"), icon: "apps", lead: N_("已连接应用共享你每月传输配额的 25%，在这里按应用查看用量。") },
};
const head = computed(() => {
  const h = heads[props.kind];
  return { ...h, title: t(h.title), lead: t(h.lead) };
});

const scopeNames: Record<string, string> = {
  "files:read": N_("读取文件"),
  "files:write": N_("写入文件"),
  "files:manage": N_("管理文件"),
  "files:purge": N_("永久删除"),
  cloud_download: N_("云添加"),
  share: N_("分享"),
  invite: N_("邀请"),
  account: N_("账号设置"),
};

const typeIcons: Record<string, string> = { mcp: "chip", pat: "key", cli: "terminal", webdav: "globe" };
const typeOrder = ["mcp", "pat", "cli", "third_party"];

interface Row {
  app: ConnectedApp;
  session: AppSession;
}

const live = (app: ConnectedApp) => (app.sessions ?? []).filter((s) => !s.revoked);
const rowsOf = (type: string): Row[] =>
  connections.apps.filter((a) => a.type === type).flatMap((app) => live(app).map((session) => ({ app, session })));

const mcpRows = computed(() => rowsOf("mcp"));
const patRows = computed(() => rowsOf("pat"));
const groups = computed(() =>
  [...connections.apps]
    .filter((a) => live(a).length > 0)
    .sort((a, b) => order(a.type) - order(b.type)),
);
const order = (type: string) => (typeOrder.includes(type) ? typeOrder.indexOf(type) : typeOrder.length);
const webdavClients = computed(() => connections.webdav?.clients ?? []);

const confirming = ref<{ id: string; action: "remove" | "regenerate" } | null>(null);
const creating = ref(false);
const reveal = ref<{ token: AccessToken; title: string; label: string } | null>(null);
const shown = ref(false);
const how = ref<"env" | "cli" | "http">("env");

const nameOf = (row: Row) => row.session.label || row.app.name || t("未命名连接");
const scopesOf = (scopes: string) =>
  scopes
    .split(/\s+/)
    .filter(Boolean)
    .map((s) => (scopeNames[s] ? t(scopeNames[s]) : s));

function usageOf(id: string): string {
  const u: UsageAxes | undefined = connections.quota?.sessions?.[id];
  if (!u) return "";
  const parts: [string, number][] = [
    [N_("下行 {size}"), u.downstream.used],
    [N_("云添加 {size}"), u.cloudDownload.used],
    [N_("上传 {size}"), u.upload.used],
  ];
  return parts
    .filter(([, n]) => n > 0)
    .map(([key, n]) => t(key, { size: bytes(n, "0 B", 1) }))
    .join(" · ");
}

function webdavUsage(username: string): string {
  return usageOf(username);
}

function expiry(s: AppSession): string {
  if (!s.expiresAt) return "";
  return new Date(s.expiresAt).getTime() < Date.now() ? t("已过期") : t("到期 {date}", { date: day(s.expiresAt) });
}

async function remove(row: Row) {
  confirming.value = null;
  await revokeSessions([row.session.id], nameOf(row));
}

async function regenerate(row: Row) {
  confirming.value = null;
  const token = await regenerateAccessToken(row.session.id);
  if (token) open(token, N_("令牌已重新生成"), nameOf(row));
}

function open(token: AccessToken, title: string, label: string) {
  creating.value = false;
  shown.value = false;
  how.value = "env";
  reveal.value = { token, title, label };
}

const masked = (token: string) => `${token.slice(0, 8)}••••••••••••${token.slice(-6)}`;
const snippet = (token: string) =>
  how.value === "cli" ? `pikpak auth token ${token}` : how.value === "env" ? `export PIKPAK_TOKEN="${token}"` : `Authorization: Bearer ${token}`;
</script>

<template>
  <SettingsIsland :title="head.title" :icon="head.icon" :lead="head.lead" @close="emit('close')">
    <!-- ---------- MCP ---------- -->
    <template v-if="kind === 'mcp'">
      <div class="well glass-well">
        <div class="well-text">
          <small>{{ t("PikPak MCP 服务器") }}</small>
          <span class="mono">{{ MCP_URL }}</span>
        </div>
        <button class="quiet icon" :title="t('复制服务器 URL')" @click="copyText(MCP_URL, t('MCP 服务器 URL'))"><Icon name="copy" :size="16" /></button>
      </div>

      <ol class="steps">
        <li><b>1</b><span>{{ t("复制上面的服务器 URL。") }}</span></li>
        <li><b>2</b><span>{{ t("在 AI 工具的 MCP 设置里添加它，例如 Claude、Cursor 或 Cherry Studio。") }}</span></li>
        <li><b>3</b><span>{{ t("工具会打开浏览器跳转到 PikPak 授权页面，点 Authorize 允许访问。") }}</span></li>
      </ol>
      <button class="link" @click="emit('open', 'pat')">
        {{ t("工具不支持浏览器授权？改用个人访问令牌") }}
        <Icon name="chevronRight" :size="14" />
      </button>

      <h3 class="section">{{ t("已连接的工具") }} <span>{{ mcpRows.length }}</span></h3>
      <p v-if="!mcpRows.length" class="empty">{{ t("还没有 AI 工具连接。按上面的步骤添加后，会出现在这里。") }}</p>
    </template>

    <!-- ---------- Personal Access Tokens ---------- -->
    <template v-else-if="kind === 'pat'">
      <div v-if="reveal" class="reveal">
        <strong><Icon name="check" :size="18" />{{ t(reveal.title) }}</strong>
        <p>{{ t("「{name}」的令牌只显示这一次，关掉后无法再查看，请妥善保存。", { name: reveal.label }) }}</p>
        <div class="token glass-well">
          <span class="mono">{{ shown ? reveal.token.token : masked(reveal.token.token) }}</span>
          <div class="token-actions">
            <button class="quiet icon tiny" :title="shown ? t('隐藏令牌') : t('查看令牌')" @click="shown = !shown">
              <Icon :name="shown ? 'eyeOff' : 'eye'" :size="16" />
            </button>
            <button class="quiet icon tiny" :title="t('复制令牌')" @click="copyText(reveal.token.token, t('令牌'))"><Icon name="copy" :size="16" /></button>
          </div>
        </div>
        <small v-if="reveal.token.expiresAt" class="meta">{{ t("到期 {date}", { date: day(reveal.token.expiresAt) }) }}<template v-if="reveal.token.scopes"> · {{ scopesOf(reveal.token.scopes).join(t("、")) }}</template></small>

        <div class="how">
          <span>{{ t("如何使用") }}</span>
          <div class="how-tabs" role="tablist">
            <button role="tab" :aria-selected="how === 'env'" :class="{ on: how === 'env' }" @click="how = 'env'">{{ t("环境变量") }}</button>
            <button role="tab" :aria-selected="how === 'cli'" :class="{ on: how === 'cli' }" @click="how = 'cli'">CLI</button>
            <button role="tab" :aria-selected="how === 'http'" :class="{ on: how === 'http' }" @click="how = 'http'">HTTP</button>
          </div>
          <div class="snippet glass-well">
            <code class="mono">{{ snippet(shown ? reveal.token.token : masked(reveal.token.token)) }}</code>
            <button class="quiet icon tiny" :title="t('复制')" @click="copyText(snippet(reveal.token.token), t('用法'))"><Icon name="copy" :size="15" /></button>
          </div>
        </div>
        <button class="primary" @click="reveal = null">{{ t("完成") }}</button>
      </div>

      <TokenForm v-else-if="creating" :active-count="patRows.length" @created="(token, l) => open(token, N_('令牌已创建'), l)" @cancel="creating = false" />
      <button v-else class="primary big" @click="creating = true">
        <Icon name="plus" :size="20" />
        {{ t("button|创建个人访问令牌") }}
      </button>

      <h3 class="section">{{ t("有效令牌") }} <span>{{ patRows.length }} / 20</span></h3>
      <p v-if="!patRows.length" class="empty">{{ t("还没有令牌。") }}</p>
    </template>

    <!-- ---------- Connected Apps ---------- -->
    <template v-else>
      <div v-if="connections.quota" class="quota glass-well">
        <small>{{ t("本月共享配额 · 每月 1 日重置") }}</small>
        <UsageMeter :label="t('云添加')" :usage="connections.quota.apps.cloudDownload" />
        <UsageMeter :label="t('下行')" :usage="connections.quota.apps.downstream" />
        <UsageMeter :label="t('上传')" :usage="connections.quota.apps.upload" />
        <UsageMeter v-if="connections.quota.apps.dailyDownstream.limit > 0" :label="t('每日下行')" :usage="connections.quota.apps.dailyDownstream" />
      </div>
      <p v-if="!groups.length && !webdavClients.length" class="empty">
        {{ t("还没有已连接的应用。可以通过 WebDAV、MCP 或个人访问令牌把其他应用接入你的 PikPak。") }}
      </p>
    </template>

    <!-- ---------- sessions ---------- -->
    <template v-for="group in kind === 'apps' ? groups : []" :key="group.id">
      <h3 class="section">
        <Icon :name="typeIcons[group.type] ?? 'apps'" :size="16" />
        {{ group.name || group.type }}
        <span>{{ live(group).length }}</span>
      </h3>
      <div v-for="session in live(group)" :key="session.id" class="session">
        <div class="session-head">
          <strong :title="session.userAgent">{{ session.label || group.name }}</strong>
          <button class="quiet remove danger-action" @click="confirming = { id: session.id, action: 'remove' }">{{ t("移除") }}</button>
        </div>
        <small>{{ t("最后活跃 {time}", { time: ago(session.lastActiveAt, t("从未")) }) }}<template v-if="session.lastActiveIp"> · {{ session.lastActiveIp }}</template></small>
        <small class="usage">{{ t("本月 {usage}", { usage: usageOf(session.id) || t("未产生流量") }) }}</small>
        <ConfirmInline
          v-if="confirming?.id === session.id"
          :message="t('移除后「{name}」立即无法访问你的 PikPak，要再用需在应用里重新连接。', { name: session.label || group.name })"
          :confirm-label="t('移除连接')"
          danger
          @cancel="confirming = null"
          @confirm="remove({ app: group, session })"
        />
      </div>
    </template>

    <template v-if="kind === 'apps' && webdavClients.length">
      <h3 class="section">
        <Icon name="globe" :size="16" />
        WebDAV
        <span>{{ webdavClients.length }}</span>
      </h3>
      <div v-for="client in webdavClients" :key="client.username" class="session">
        <div class="session-head">
          <strong>{{ client.name || client.username }}</strong>
        </div>
        <small>{{ t("最后活跃 {time}", { time: ago(client.lastActiveAt, t("从未")) }) }}</small>
        <small class="usage">{{ t("本月 {usage}", { usage: webdavUsage(client.username) || t("未产生流量") }) }}</small>
      </div>
      <button class="link" @click="emit('open', 'webdav')">
        {{ t("管理 WebDAV 凭证") }}
        <Icon name="chevronRight" :size="14" />
      </button>
    </template>

    <div v-for="row in kind === 'mcp' ? mcpRows : kind === 'pat' ? patRows : []" :key="row.session.id" class="session">
      <div class="session-head">
        <strong :title="row.session.userAgent">{{ nameOf(row) }}</strong>
        <span class="acts">
          <button v-if="kind === 'pat'" class="quiet remove" @click="confirming = { id: row.session.id, action: 'regenerate' }">{{ t("重新生成") }}</button>
          <button class="quiet remove danger-action" @click="confirming = { id: row.session.id, action: 'remove' }">{{ t("移除") }}</button>
        </span>
      </div>
      <small>
        {{ t("最后活跃 {time}", { time: ago(row.session.lastActiveAt, t("从未")) }) }}
        <template v-if="kind === 'pat'"> · {{ t("创建于 {date}", { date: day(row.session.createdAt) }) }}<template v-if="expiry(row.session)"> · {{ expiry(row.session) }}</template></template>
      </small>
      <div v-if="row.session.scopes" class="scopes">
        <span v-for="s in scopesOf(row.session.scopes)" :key="s">{{ s }}</span>
      </div>
      <small v-if="usageOf(row.session.id)" class="usage">{{ t("本月 {usage}", { usage: usageOf(row.session.id) }) }}</small>
      <ConfirmInline
        v-if="confirming?.id === row.session.id"
        :message="confirming.action === 'remove' ? t('移除后「{name}」立即无法访问你的 PikPak，要再用需重新连接。', { name: nameOf(row) }) : t('旧令牌会立即失效，用到它的地方都要换成新令牌。')"
        :confirm-label="confirming.action === 'remove' ? t('移除连接') : t('重新生成')"
        :danger="confirming.action === 'remove'"
        @cancel="confirming = null"
        @confirm="confirming.action === 'remove' ? remove(row) : regenerate(row)"
      />
    </div>
  </SettingsIsland>
</template>

<style scoped>
.well {
  display: flex;
  align-items: center;
  gap: 10px;
  min-height: 56px;
  padding: 8px 6px 8px 14px;
  border-radius: 16px;
}

.well-text {
  flex: 1;
  min-width: 0;
  display: flex;
  flex-direction: column;
  gap: 2px;
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

.steps {
  margin: 0;
  padding: 0;
  list-style: none;
  display: flex;
  flex-direction: column;
  gap: 10px;
}

.steps li {
  display: flex;
  gap: 10px;
  font-size: 13px;
  line-height: 1.55;
  color: var(--fg);
}

.steps b {
  flex: none;
  width: 22px;
  height: 22px;
  border-radius: 50%;
  display: grid;
  place-items: center;
  background: var(--accent-soft);
  color: var(--accent);
  font-size: 12px;
  font-weight: 700;
}

.link {
  justify-content: flex-start;
  min-height: 0;
  padding: 2px 0;
  gap: 2px;
  border: none;
  background: none;
  color: var(--accent);
  font-size: 13px;
  font-weight: 500;
  white-space: normal;
  text-align: left;
}

.link:hover:not(:disabled) {
  background: none;
  text-decoration: underline;
}

.section {
  display: flex;
  align-items: center;
  gap: 7px;
  margin: 8px 0 -2px;
  padding-top: 14px;
  border-top: 1px solid var(--line);
  font-size: 15px;
  font-weight: 600;
}

.section span {
  margin-left: auto;
  font-size: 12px;
  font-weight: 500;
  color: var(--fg-dim);
  font-variant-numeric: tabular-nums;
}

.empty {
  margin: 0;
  font-size: 13px;
  line-height: 1.55;
  color: var(--fg-dim);
}

.big {
  min-height: 54px;
  gap: 10px;
  font-size: 16px;
  font-weight: 600;
}

.quota {
  display: flex;
  flex-direction: column;
  gap: 14px;
  padding: 14px;
  border-radius: 16px;
}

.quota > small {
  font-size: 12px;
  color: var(--fg-dim);
}

/* ---------- a connection ---------- */

.session {
  display: flex;
  flex-direction: column;
  gap: 5px;
  padding: 13px 14px;
  border-radius: 14px;
  border: 1px solid var(--line);
  background: color-mix(in srgb, var(--fg) 4%, transparent);
}

/* The name keeps a readable start: where two actions in a longer language
   leave it less, they move under it, still on the right. */
.session-head {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 4px;
}

.session-head strong {
  flex: 1 1 6em;
  min-width: 0;
  font-size: 14px;
  font-weight: 600;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.remove {
  flex: none;
  min-height: 28px;
  margin: 0 -2px;
  padding: 0 10px;
  font-size: 12px;
}

.session-head strong + .remove,
.acts {
  margin-left: auto;
}

.acts {
  display: flex;
  flex: none;
  gap: 4px;
}

.session small {
  font-size: 12px;
  line-height: 1.5;
  color: var(--fg-dim);
}

.usage {
  font-variant-numeric: tabular-nums;
}

.scopes {
  display: flex;
  flex-wrap: wrap;
  gap: 4px;
}

.scopes span {
  padding: 2px 8px;
  border-radius: var(--radius-pill);
  background: var(--accent-soft);
  color: var(--accent);
  font-size: 11px;
  font-weight: 600;
}

/* ---------- a new token, shown once ---------- */

.reveal {
  display: flex;
  flex-direction: column;
  gap: 10px;
  padding: 16px;
  border-radius: 16px;
  border: 1px solid color-mix(in srgb, var(--success) 45%, transparent);
  background: color-mix(in srgb, var(--success) 8%, transparent);
}

.reveal > strong {
  display: flex;
  align-items: center;
  gap: 6px;
  font-size: 15px;
  color: var(--success);
}

.reveal > p {
  margin: 0;
  font-size: 12px;
  line-height: 1.55;
  color: var(--fg-dim);
}

.token,
.snippet {
  display: flex;
  align-items: flex-start;
  gap: 6px;
  padding: 8px 6px 8px 12px;
  border-radius: 12px;
}

.token .mono,
.snippet code {
  flex: 1;
  min-width: 0;
  max-height: 96px;
  overflow-y: auto;
  padding-top: 5px;
  font-size: 12px;
  line-height: 1.5;
  overflow-wrap: anywhere;
  -webkit-user-select: text;
  user-select: text;
}

.token-actions {
  display: flex;
  flex: none;
}

.tiny {
  width: 28px;
  height: 28px;
  min-height: 28px;
}

.meta {
  font-size: 12px;
  color: var(--fg-dim);
}

.how {
  display: flex;
  flex-direction: column;
  gap: 8px;
}

.how > span {
  font-size: 12px;
  font-weight: 600;
  color: var(--fg-dim);
}

.how-tabs {
  display: flex;
  flex-wrap: wrap;
  gap: 4px;
}

.how-tabs button {
  min-height: 28px;
  padding: 0 10px;
  border-radius: var(--radius-pill);
  font-size: 12px;
}

.how-tabs button.on {
  border-color: transparent;
  background: var(--primary) border-box;
  color: var(--on-primary);
  font-weight: 600;
}
</style>
