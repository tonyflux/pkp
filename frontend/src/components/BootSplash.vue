<script setup lang="ts">
// Shown between window open and the first Status() answer. It is a `.late`
// surface: a fast local backend never shows it; a slow one gets the brand,
// an orbit, and a sentence instead of an empty gradient. If Status() itself
// fails there is nothing else to navigate to, so the retry lives here.
import Icon from "./Icon.vue";
import Logo from "./Logo.vue";
import Orbit from "./Orbit.vue";
import { bootstrap, ui } from "../store";
import { t } from "../i18n";
</script>

<template>
  <div class="wrap" style="--wails-draggable: drag">
    <section class="splash glass glass-sheet late" :class="{ failed: ui.bootError }">
      <div class="mark"><Logo :size="64" /></div>
      <h1>PKP</h1>
      <p class="sub">for PikPak</p>

      <div v-if="ui.bootError" class="status error">
        <Icon name="alert" :size="16" />
        <span>{{ ui.bootError }}</span>
        <button class="quiet" style="--wails-draggable: no-drag" @click="bootstrap">{{ t("重试") }}</button>
      </div>
      <div v-else class="status">
        <Orbit :size="16" :label="t('正在连接')" />
        <span>{{ t("正在连接…") }}</span>
      </div>
    </section>
  </div>
</template>

<style scoped>
.wrap {
  position: relative;
  z-index: 1;
  height: 100%;
  display: grid;
  place-items: center;
}

.splash {
  display: flex;
  flex-direction: column;
  align-items: center;
  width: 280px;
  padding: 34px 28px 26px;
  border-radius: var(--radius-sheet);
  text-align: center;
}

.mark {
  display: grid;
  place-items: center;
  width: 64px;
  height: 64px;
  /* The mark bobs as if adrift: 1px either way, slow — ambience, not a
     spinner. The orbit below carries the actual "working" signal. */
  animation: adrift 3.2s ease-in-out infinite alternate;
}

@keyframes adrift {
  from {
    transform: translateY(-2px);
  }
  to {
    transform: translateY(2px);
  }
}

h1 {
  margin: 16px 0 0;
  font-size: 22px;
  line-height: 1.2;
  letter-spacing: -0.01em;
}

.sub {
  margin: 2px 0 0;
  font-size: 12px;
  color: var(--fg-dim);
}

.status {
  display: flex;
  align-items: center;
  gap: 9px;
  margin-top: 22px;
  min-height: 30px;
  font-size: 13px;
  color: var(--fg-dim);
}

.status.error {
  flex-wrap: wrap;
  justify-content: center;
  color: var(--danger);
}

.status.error span {
  flex: 1 1 100%;
  overflow-wrap: anywhere;
}

/* The failure is immediate information; it must not wait the 350ms. */
.failed.late {
  animation: none;
}
</style>
