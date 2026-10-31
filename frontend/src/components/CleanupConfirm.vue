<script setup lang="ts">
// Cleanup confirmations: Move to recycle bin, Permanently delete (danger), and
// Empty folders with what they hold.
// Permanently delete opens on Keep files, so Enter alone never deletes anything.
import { computed, nextTick, onBeforeUnmount, onMounted, ref, watch } from "vue";
import Icon from "./Icon.vue";
import { bytes } from "../format";
import { t } from "../i18n";
import {
  cleanup,
  closeCleanupConfirm,
  dupPicked,
  dupPickedBytes,
  emptyPicked,
  removeDuplicates,
  removeEmptyFolders,
} from "../store";

const open = computed(() => cleanup.confirming !== "");
const empty = computed(() => cleanup.confirming === "empty");
const danger = computed(() => !empty.value && cleanup.permanent);
const count = computed(() => (empty.value ? emptyPicked.value.length : dupPicked.value.length));
const nested = computed(() => emptyPicked.value.reduce((sum, f) => sum + f.nested, 0));
const space = computed(() => bytes(dupPickedBytes.value, "0 B"));

const title = computed(() => {
  const n = count.value;
  if (empty.value) return t("把 {n} 个空文件夹移到回收站？", { n });
  return danger.value ? t("彻底删除 {n} 项？", { n }) : t("把 {n} 项移到回收站？", { n });
});
const line = computed(() => {
  if (empty.value) return t("里面套着的 {n} 个空文件夹会一起进去，可以在回收站还原。", { n: nested.value });
  return danger.value ? t("无法撤销，立即释放 {size}。", { size: space.value }) : t("可以在回收站还原。空间要等彻底删除后才释放。");
});

const keepButton = ref<HTMLButtonElement | null>(null);
const goButton = ref<HTMLButtonElement | null>(null);

function confirm() {
  void (empty.value ? removeEmptyFolders() : removeDuplicates());
}

watch(open, async (now) => {
  if (!now) return;
  await nextTick();
  (danger.value ? keepButton.value : goButton.value)?.focus();
});

function onKeydown(event: KeyboardEvent) {
  if (!open.value || event.key !== "Escape") return;
  event.stopImmediatePropagation();
  closeCleanupConfirm();
}

onMounted(() => window.addEventListener("keydown", onKeydown, true));
onBeforeUnmount(() => {
  window.removeEventListener("keydown", onKeydown, true);
  closeCleanupConfirm();
});
</script>

<template>
  <Teleport to="body">
    <div v-if="open" class="scrim" @click.self="closeCleanupConfirm">
      <section class="sheet glass glass-sheet" role="alertdialog" aria-modal="true" aria-labelledby="cleanup-confirm-title" aria-describedby="cleanup-confirm-line">
        <header>
          <span class="mark" :class="{ danger }" aria-hidden="true"><Icon name="trash" :size="18" /></span>
          <button class="quiet icon" :aria-label="t('关闭')" :title="t('关闭')" @click="closeCleanupConfirm"><Icon name="close" :size="18" /></button>
        </header>
        <h2 id="cleanup-confirm-title">{{ title }}</h2>
        <p id="cleanup-confirm-line">{{ line }}</p>
        <dl class="preview">
          <dt>{{ t("本次选择") }}</dt>
          <dd class="tnum">{{ t("{n} 项", { n: count }) }}</dd>
          <template v-if="empty">
            <dt>{{ t("连同嵌套") }}</dt>
            <dd class="tnum">{{ t("{n} 个", { n: nested }) }}</dd>
          </template>
          <dt>{{ t("预计空间") }}</dt>
          <dd class="tnum">{{ empty ? t("不占空间") : space }}</dd>
        </dl>
        <footer>
          <button ref="keepButton" @click="closeCleanupConfirm">{{ t("保留文件") }}</button>
          <button ref="goButton" class="primary" :class="{ danger }" @click="confirm">
            {{ danger ? t("确认彻底删除") : t("确认移到回收站") }}
          </button>
        </footer>
      </section>
    </div>
  </Teleport>
</template>

<style scoped>
.sheet {
  width: min(440px, calc(100vw - 32px));
  padding: 24px;
  gap: 0;
}

header {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  margin-bottom: 18px;
}

.mark {
  display: grid;
  place-items: center;
  width: 44px;
  height: 44px;
  border-radius: 12px;
  background: color-mix(in srgb, var(--accent) 16%, transparent);
  color: var(--accent);
}

.mark.danger {
  background: color-mix(in srgb, var(--danger) 16%, transparent);
  color: var(--danger);
}

h2 {
  margin: 0 0 8px;
  font-size: 21px;
  line-height: 28px;
}

p {
  margin: 0 0 18px;
  font-size: 13px;
  color: var(--fg-dim);
}

.preview {
  display: grid;
  grid-template-columns: minmax(0, 1fr) auto;
  row-gap: 8px;
  margin: 0 0 22px;
  padding: 12px 14px;
  border-radius: 12px;
  border: 1px solid var(--line);
  background: color-mix(in srgb, var(--canvas) 60%, transparent);
  font-size: 13px;
}

.preview dt {
  color: var(--fg-dim);
}

.preview dd {
  margin: 0;
  font-weight: 650;
  text-align: right;
}

footer {
  display: flex;
  justify-content: flex-end;
  gap: 10px;
}

footer button {
  min-height: 40px;
  padding: 0 16px;
  border-radius: 12px;
  font-size: 13px;
  font-weight: 600;
}
</style>
