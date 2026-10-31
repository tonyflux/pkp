<script setup lang="ts">
// An account's PikPak membership in one line (GET /vip/v1/vip/info): which
// one runs and until when, that it ended, or that there is none. It reads
// the membership the first time the account is shown; nothing is drawn
// until the answer is in, so a member is never shown as Not enabled first.
import { computed, onMounted } from "vue";
import Icon from "./Icon.vue";
import { loadMembership, memberships } from "../store";
import { membershipLine } from "../membership";
import { t } from "../i18n";

const props = defineProps<{ account: string }>();

const read = computed(() => memberships[props.account]);
const line = computed(() => (read.value?.value ? membershipLine(read.value.value) : null));
const notice = computed(() => read.value?.value?.notice ?? "");

onMounted(() => {
  if (!memberships[props.account]) void loadMembership(props.account);
});
</script>

<template>
  <span v-if="line" class="member" :class="line.tone" :title="[line.title, line.detail, notice].filter(Boolean).join(' · ')">
    <Icon name="crown" :size="14" />
    <b>{{ line.title }}</b>
    <span v-if="line.detail" class="detail">{{ line.detail }}</span>
    <span v-if="notice" class="notice">{{ notice }}</span>
  </span>
  <span v-else-if="read?.error" class="member none" :title="read.error">
    <Icon name="crown" :size="14" />
    {{ t("会员信息暂不可用") }}
  </span>
</template>

<style scoped>
.member {
  display: inline-flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 2px 6px;
  min-width: 0;
  font-size: 12px;
  line-height: 18px;
  color: var(--fg-dim);
}

.member b {
  font-weight: 600;
}

.member.active {
  color: var(--accent);
}

.member.active .detail {
  color: var(--fg-dim);
}

.member.ended b {
  color: var(--warning);
}

.notice {
  flex-basis: 100%;
  color: var(--warning);
}

.icon {
  flex: none;
}
</style>
