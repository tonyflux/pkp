<script setup lang="ts">
import { computed } from "vue";
import type { AISubtitleState } from "../../bindings/github.com/tonyflux/pkp/internal/services";
import { clock, stamp } from "../format";
import { t } from "../i18n";

const props = defineProps<{
  state: AISubtitleState;
  account: string;
  access: "ready" | "loading" | "error" | "nonmember";
}>();
const emit = defineEmits<{ retry: [] }>();
const quota = computed(() => props.account && props.state.quota.account === props.account && props.state.quota.known
  ? props.state.quota : null);
</script>

<template>
  <div class="ai-status" role="status">
    <p v-if="state.requiresMembership" class="membership">
      <strong>{{ t("会员专享") }}</strong>
      <span v-if="access === 'loading'">{{ t("正在读取会员状态…") }}</span>
      <span v-else-if="access === 'nonmember'">{{ t("AI 字幕需要 PikPak 会员") }}</span>
      <template v-else-if="access === 'error'">
        <span>{{ t("会员状态读取失败") }}</span>
        <button type="button" @keydown.space.stop @click="emit('retry')">{{ t("重试") }}</button>
      </template>
    </p>
    <p v-if="quota" class="quota">
      {{ t("AI 剩余 {remaining} / {total}", { remaining: clock(quota.exceeded ? 0 : Math.max(0, quota.limit - quota.usage)), total: clock(quota.limit) }) }}
    </p>
    <p v-else class="quota">{{ t("AI 配额：开启后获取") }}</p>
    <p v-if="quota?.nextResetTime" class="reset">{{ t("配额重置：{time}", { time: stamp(quota.nextResetTime) }) }}</p>
  </div>
</template>

<style scoped>
.ai-status {
  margin: 6px 3px;
  color: inherit;
  font-size: 11px;
  line-height: 1.5;
}
p { margin: 3px 0; }
.membership { display: flex; flex-wrap: wrap; align-items: baseline; gap: 4px 8px; }
strong { font-weight: 600; }
button { padding: 0; border: 0; background: none; color: inherit; text-decoration: underline; cursor: pointer; }
</style>
