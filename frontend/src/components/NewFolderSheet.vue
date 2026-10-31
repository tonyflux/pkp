<script setup lang="ts">
// New · New Folder: names a folder for the folder on screen. The name starts
// as New Folder, selected, so typing replaces it and Enter alone accepts it.
import { computed, nextTick, onBeforeUnmount, onMounted, ref, watch } from "vue";
import Icon from "./Icon.vue";
import Orbit from "./Orbit.vue";
import { createFolder, currentFolderName, ui } from "../store";
import { t } from "../i18n";

const name = ref("");
const working = ref(false);
const field = ref<HTMLInputElement>();
const trimmed = computed(() => name.value.trim());

watch(
  () => ui.newFolderOpen,
  async (open) => {
    if (!open) return;
    name.value = t("新建文件夹");
    await nextTick();
    field.value?.select();
  },
);

function close() {
  if (!working.value) ui.newFolderOpen = false;
}

async function submit() {
  if (!trimmed.value || working.value) return;
  working.value = true;
  try {
    await createFolder(trimmed.value);
  } finally {
    working.value = false;
  }
  // Still open: the server refused the name. Back to it, to fix it.
  if (ui.newFolderOpen) field.value?.focus();
}

// The sheet owns Esc while it is up, ahead of the page behind it.
function onKeydown(ev: KeyboardEvent) {
  if (!ui.newFolderOpen || ev.key !== "Escape") return;
  ev.stopImmediatePropagation();
  close();
}

onMounted(() => window.addEventListener("keydown", onKeydown, true));
onBeforeUnmount(() => window.removeEventListener("keydown", onKeydown, true));
</script>

<template>
  <Teleport to="body">
    <div v-if="ui.newFolderOpen" class="scrim" @click.self="close">
      <!-- Keys stay in the sheet: Enter on a focused button must not also
           open whatever the page behind has selected. -->
      <section class="sheet glass glass-sheet" role="dialog" :aria-label="t('新建文件夹')" @keydown.stop>
        <header>
          <div class="titles">
            <h2>{{ t("新建文件夹") }}</h2>
            <p :title="currentFolderName">{{ t("新建到「{name}」", { name: currentFolderName }) }}</p>
          </div>
          <button class="icon close" :title="t('关闭')" @click="close"><Icon name="close" :size="16" /></button>
        </header>

        <label class="field">
          <Icon name="folder" :size="20" filled />
          <input
            ref="field"
            v-model="name"
            :aria-label="t('文件夹名称')"
            :placeholder="t('文件夹名称')"
            autocomplete="off"
            spellcheck="false"
            @keydown.enter.prevent="submit"
          />
        </label>

        <footer>
          <button @click="close">{{ t("取消") }}</button>
          <button class="primary" :class="{ working }" :disabled="!trimmed || working" @click="submit">
            <Orbit v-if="working" :size="15" />
            {{ working ? t("创建中…") : t("创建") }}
          </button>
        </footer>
      </section>
    </div>
  </Teleport>
</template>

<style scoped>
.sheet {
  width: min(460px, calc(100vw - 80px));
  gap: 18px;
  padding: 28px 32px;
}

header {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: 12px;
}

.titles {
  min-width: 0;
}

header h2 {
  margin: 0;
  font-size: 26px;
  font-weight: 600;
  line-height: 34px;
}

header p {
  margin: 4px 0 0;
  font-size: 14px;
  color: var(--fg-dim);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.close {
  flex: none;
  width: 40px;
  height: 40px;
  min-height: 40px;
}

/* One field with the folder glyph inside it; the frame takes the focus
   colour, the input itself stays bare. */
.field {
  display: flex;
  align-items: center;
  gap: 12px;
  min-height: 50px;
  padding: 0 16px;
  border-radius: 14px;
  border: 1px solid var(--line-strong);
  background: var(--canvas);
  transition: border-color var(--dur) var(--ease);
}

.field:focus-within {
  border-color: var(--accent);
}

.field > svg {
  color: var(--accent);
}

.field input {
  flex: 1;
  min-width: 0;
  padding: 0;
  border: 0;
  border-radius: 0;
  background: transparent;
  font-size: 16px;
  font-weight: 500;
}

footer {
  display: flex;
  justify-content: flex-end;
  gap: 10px;
}

footer button {
  min-width: 112px;
  min-height: 46px;
  padding: 0 24px;
  font-size: 15px;
  font-weight: 600;
}
</style>
