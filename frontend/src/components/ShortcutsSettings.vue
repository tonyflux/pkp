<script setup lang="ts">
// Settings · Keyboard Shortcuts: every shortcut of the main window, for this platform, read
// from shortcuts.ts so the list cannot drift from what the keys do.
import { computed } from "vue";
import { t } from "../i18n";
import { shortcutGroups } from "../shortcuts";

const groups = computed(() => shortcutGroups());
</script>

<template>
  <div class="shortcuts">
    <h2>{{ t("快捷键") }}</h2>
    <p class="lead">{{ t("在输入框里打字时，方向键和单个字母留给输入框；带修饰键的快捷键照常可用。") }}</p>

    <div class="groups">
      <section v-for="group in groups" :key="group.title" class="group" :aria-label="group.title">
        <h3>{{ group.title }}</h3>
        <dl>
          <div v-for="row in group.rows" :key="row.label" class="row">
            <dt>{{ row.label }}</dt>
            <dd>
              <template v-for="(chord, i) in row.keys" :key="i">
                <span v-if="i" class="or">/</span>
                <span class="chord">
                  <kbd v-for="k in chord" :key="k">{{ k }}</kbd>
                </span>
              </template>
            </dd>
          </div>
        </dl>
      </section>
    </div>
  </div>
</template>

<style scoped>
.shortcuts {
  container: shortcuts / inline-size;
}

h2 {
  margin: 0;
  font-size: 25px;
  font-weight: 600;
  line-height: 34px;
}

.lead {
  margin: 6px 0 24px;
  font-size: 15px;
  color: var(--fg-dim);
}

/* Columns, not a grid: the groups differ in length and stack without gaps. */
@container shortcuts (min-width: 760px) {
  .groups {
    columns: 2;
    column-gap: 14px;
  }
}

.group {
  break-inside: avoid;
  margin-bottom: 14px;
  padding: 16px 20px 8px;
  border-radius: 16px;
  border: 1px solid var(--line);
  background: color-mix(in srgb, var(--fg) 3%, transparent);
}

h3 {
  margin: 0 0 6px;
  font-size: 16px;
  font-weight: 600;
}

dl {
  margin: 0;
}

.row {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  min-height: 40px;
  padding: 6px 0;
  border-top: 1px solid color-mix(in srgb, var(--line) 70%, transparent);
}

.row:first-child {
  border-top: 0;
}

dt {
  min-width: 0;
  font-size: 14px;
}

dd {
  display: flex;
  flex-wrap: wrap;
  justify-content: flex-end;
  align-items: center;
  gap: 6px;
  margin: 0;
  flex: none;
  max-width: 60%;
}

.chord {
  display: flex;
  gap: 3px;
}

.or {
  font-size: 12px;
  color: var(--fg-dim);
}

kbd {
  display: grid;
  place-items: center;
  min-width: 24px;
  height: 24px;
  padding: 0 6px;
  font-family: inherit;
  font-size: 12px;
  font-weight: 500;
  white-space: nowrap;
  border-radius: 6px;
  border: 1px solid color-mix(in srgb, var(--glass-rim) 30%, transparent);
  background: color-mix(in srgb, var(--glass-control) 70%, transparent);
  color: var(--fg-dim);
}
</style>
