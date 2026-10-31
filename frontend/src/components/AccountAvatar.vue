<script setup lang="ts">
// An account's mark (AccountIdentity): its profile picture,
// or the first character of what it is called on a tint of its own. The
// tint only helps tell accounts apart at a glance; the name beside the mark
// always says which account it is, so the mark itself is hidden from
// assistive tech.
import { computed, ref, watch } from "vue";
import { accountLabel, accounts } from "../store";
import type { Account } from "../../bindings/github.com/tonyflux/pkp/internal/services";

const props = withDefaults(defineProps<{ account: Account | null; size?: number }>(), { size: 40 });

// A picture that does not load falls back to the letter until another arrives.
const broken = ref(false);
watch(
  () => props.account?.avatar,
  () => (broken.value = false),
);

// The tint follows the account's place in the list: the first account is
// always the same colour, whichever is on screen.
const tint = computed(() => {
  const i = accounts.list.findIndex((a) => a.id === props.account?.id);
  return `var(--account-${(Math.max(i, 0) % 5) + 1})`;
});

const mark = computed(() => ([...accountLabel(props.account)][0] ?? "?").toUpperCase());
</script>

<template>
  <span
    class="account-avatar"
    :style="{ width: `${size}px`, height: `${size}px`, fontSize: `${Math.round(size * 0.42)}px`, '--tint': tint }"
    aria-hidden="true"
  >
    <img v-if="account?.avatar && !broken" :src="account.avatar" alt="" @error="broken = true" />
    <template v-else>{{ mark }}</template>
  </span>
</template>

<style scoped>
.account-avatar {
  flex: none;
  display: grid;
  place-items: center;
  border-radius: 50%;
  overflow: hidden;
  background: var(--tint);
  color: var(--on-account);
  font-weight: 700;
  line-height: 1;
  box-shadow: inset 0 0 0 1px color-mix(in srgb, var(--glass-rim) 45%, transparent);
  user-select: none;
}

.account-avatar img {
  width: 100%;
  height: 100%;
  object-fit: cover;
}
</style>
