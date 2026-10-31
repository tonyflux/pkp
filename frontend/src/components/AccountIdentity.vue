<script setup lang="ts">
// Who an account is, the same way everywhere (AccountRow /
// AccountIdentity): its mark, what it is called here and its masked login.
// Either line may be cut short; its title carries all of it. Several
// accounts may share a name; the login under it tells them apart.
import { computed } from "vue";
import AccountAvatar from "./AccountAvatar.vue";
import { maskLogin } from "../format";
import { accountLabel } from "../store";
import { t } from "../i18n";
import type { Account } from "../../bindings/github.com/tonyflux/pkp/internal/services";

const props = withDefaults(defineProps<{ account: Account | null; size?: number; dense?: boolean }>(), {
  size: 40,
  dense: false,
});

const label = computed(() => accountLabel(props.account) || t("已移除的账号"));
const masked = computed(() => maskLogin(props.account?.login ?? ""));
</script>

<template>
  <span class="account-identity" :class="{ dense }">
    <AccountAvatar :account="account" :size="size" />
    <span class="text">
      <strong :title="label">{{ label }}</strong>
      <small v-if="masked" :title="masked">{{ masked }}</small>
      <!-- A line about the account the page adds under it, e.g. its membership. -->
      <slot />
    </span>
  </span>
</template>

<style scoped>
.account-identity {
  display: flex;
  align-items: center;
  gap: 12px;
  min-width: 0;
}

.text {
  display: flex;
  flex-direction: column;
  min-width: 0;
  line-height: 1.3;
}

.text strong,
.text small {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.text strong {
  font-size: 15px;
  font-weight: 600;
  color: var(--fg);
}

.text small {
  font-size: 12px;
  color: var(--fg-dim);
}

.dense {
  gap: 10px;
}

.dense .text strong {
  font-size: 13px;
}

.dense .text small {
  font-size: 12px;
}
</style>
