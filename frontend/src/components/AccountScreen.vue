<script setup lang="ts">
// The content area while the account on screen cannot show its files
// while switching accounts, or signed out by the server
// (expired). Nothing of another account stands in for it meanwhile.
import { computed } from "vue";
import AccountAvatar from "./AccountAvatar.vue";
import { maskLogin } from "../format";
import { accountById, accountLabel, accounts, currentAccount, currentId, drive, openSignIn, ui } from "../store";
import { t } from "../i18n";

const props = defineProps<{ state: "switching" | "expired" }>();

const account = computed(() => (props.state === "switching" ? accountById(accounts.switching) : currentAccount.value));
const name = computed(() => accountLabel(account.value));
const masked = computed(() => maskLogin(account.value?.login || drive.status?.account || ""));
</script>

<template>
  <section v-if="state === 'switching'" class="account-screen canvas" role="status">
    <AccountAvatar :account="account" :size="64" />
    <h1>{{ name ? t("正在切换到「{name}」…", { name }) : t("正在切换到此账号…") }}</h1>
    <p>{{ t("正在载入此账号的文件与概览") }}</p>
  </section>

  <section v-else class="account-screen canvas" aria-labelledby="account-screen-title">
    <AccountAvatar :account="account" :size="64" />
    <h1 id="account-screen-title">{{ name ? t("重新登录「{name}」", { name }) : t("重新登录此账号") }}</h1>
    <p>{{ t("此账号的登录已失效。重新登录后恢复文件访问；其他账号及后台任务不受影响。") }}</p>
    <small v-if="masked">{{ masked }}</small>
    <div class="actions">
      <button class="primary" @click="openSignIn(currentId)">{{ t("重新登录") }}</button>
      <button @click="ui.accountsOpen = true">{{ t("切换其他账号") }}</button>
    </div>
  </section>
</template>

<style scoped>
.account-screen {
  flex: 1;
  min-width: 0;
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  gap: 12px;
  padding: 32px 24px;
  overflow-y: auto;
  text-align: center;
}

h1 {
  margin: 8px 0 0;
  font-size: 24px;
  font-weight: 700;
  line-height: 1.3;
  text-wrap: balance;
}

p {
  margin: 0;
  max-width: 480px;
  font-size: 15px;
  color: var(--fg-dim);
  text-wrap: balance;
}

small {
  margin-top: 8px;
  font-size: 13px;
  color: var(--fg-dim);
}

.actions {
  display: flex;
  flex-wrap: wrap;
  justify-content: center;
  gap: 12px;
  margin-top: 16px;
}

.actions button {
  min-width: 112px;
  min-height: 44px;
  padding: 0 20px;
  font-size: 15px;
  font-weight: 600;
}
</style>
