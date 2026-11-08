<script setup lang="ts">
import { computed, ref } from "vue";
import type { Card, SearchResultGroup } from "../agent";
import { hasSearchResults, presentSearchGroups, querySummary, searchSources } from "../assistantresults";
import { t } from "../i18n";
import { assistant, cleanupAssistantResults, openAssistantSearch, organizeAssistantResults } from "../store";
import AssistantCard from "./AssistantCard.vue";

const props = defineProps<{
  cards: readonly Card[];
  resultGroups?: readonly SearchResultGroup[];
  live: boolean;
  turnId: string;
}>();
const processOpen = ref(false);
const openedSources = ref(new Set<string>());
const sources = computed(() => searchSources(props.cards));
const hasPlan = computed(() => props.cards.some((card) => card.kind === "plan"));
const groups = computed(() => hasPlan.value ? [] : presentSearchGroups(props.cards, props.resultGroups, props.live));
const unselected = computed(() => !props.live && !hasPlan.value && props.resultGroups === undefined && sources.value.length > 1);
const locked = computed(() => props.live || !!assistant.live || !!assistant.busy || !!assistant.consent || !!assistant.connect || assistant.running);
function toggleProcess(event: Event) {
  processOpen.value = (event.target as HTMLDetailsElement).open;
  if (!processOpen.value) openedSources.value.clear();
}
function toggleSource(id: string, event: Event) {
  if ((event.target as HTMLDetailsElement).open) openedSources.value.add(id);
  else openedSources.value.delete(id);
}
</script>

<template>
  <div v-if="sources.length" class="search-results">
    <details class="search-process" @toggle="toggleProcess">
      <summary>{{ t('搜索过程 · {n} 次搜索', { n: sources.length }) }}</summary>
      <ol v-if="processOpen" class="attempts">
        <li v-for="(source, index) in sources" :key="source.id">
          <div class="attempt-heading"><strong>{{ t('搜索 {n}', { n: index + 1 }) }}</strong><span>{{ t('命中 {n} 项', { n: source.total }) }}</span></div>
          <p class="conditions">{{ querySummary(source.query, source.title) }}</p>
          <p v-if="source.note" class="source-note">{{ source.note }}</p>
          <button v-if="source.query" class="quiet query-action" type="button" :disabled="locked" @click="openAssistantSearch(source.query)">{{ t('在文件页查看匹配结果') }}</button>
          <details v-if="hasSearchResults(source)" class="source-files" @toggle="toggleSource(source.id, $event)">
            <summary>{{ t('查看本次结果') }}</summary>
            <AssistantCard v-if="openedSources.has(source.id)" :card="source" :turn-id="turnId" :number="1" :live="live">
              <template v-if="!live && source.rows.length" #actions>
                <div class="result-actions">
                  <p class="result-note">{{ t('清理仅生成预览，不会立即删除文件。') }}</p>
                  <button class="danger" type="button" :disabled="locked" @click="cleanupAssistantResults(source.rows)">{{ t('清理展示的 {n} 项', { n: source.rows.length }) }}</button>
                </div>
              </template>
            </AssistantCard>
          </details>
        </li>
      </ol>
    </details>
    <p v-if="unselected" class="selection-note">{{ t('未选择最终结果，可展开搜索过程查看各次命中。') }}</p>
    <div v-for="(group, index) in groups" :key="index" class="result-group">
      <AssistantCard :card="group.card" :turn-id="turnId" :number="1" :live="live" :displayed-count="group.unique ? group.card.rows.length : undefined">
        <template #metadata>
          <div class="group-sources">
            <div v-for="source in group.sources" :key="source.id">
              <strong>{{ t('搜索 {n}', { n: sources.findIndex(item => item.id === source.id) + 1 }) }} · {{ t('命中 {n} 项', { n: source.total }) }}</strong>
              <p>{{ querySummary(source.query, source.title) }}</p>
              <p v-if="source.note">{{ source.note }}</p>
              <button v-if="source.query" class="quiet query-action" type="button" :disabled="locked" @click="openAssistantSearch(source.query)">{{ t('在文件页查看匹配结果') }}</button>
            </div>
          </div>
        </template>
        <template v-if="!live && group.card.rows.length" #actions>
          <div class="result-actions">
            <p class="result-note">{{ t('清理仅生成预览，不会立即删除文件。') }}</p>
            <button class="primary" type="button" :disabled="locked" @click="organizeAssistantResults(group.card.rows)">{{ t('整理命名 {n} 项', { n: group.card.rows.length }) }}</button>
            <button class="danger" type="button" :disabled="locked" @click="cleanupAssistantResults(group.card.rows)">{{ t('清理展示的 {n} 项', { n: group.card.rows.length }) }}</button>
          </div>
        </template>
      </AssistantCard>
    </div>
  </div>
</template>

<style scoped>
.search-results { min-width: 0; margin-bottom: 14px; }
.search-process { margin: 0 0 12px; border: 1px solid var(--line-strong); border-radius: 10px; font-size: 12px; }
summary { padding: 10px 12px; cursor: pointer; color: var(--fg-dim); }
summary:focus-visible { outline: 2px solid var(--accent); outline-offset: -2px; border-radius: 8px; }
.attempts { list-style: none; margin: 0; padding: 0 12px 4px; }
.attempts > li { border-top: 1px solid var(--line-strong); padding: 10px 0; }
.attempt-heading { display: flex; flex-wrap: wrap; gap: 8px 16px; }
.attempt-heading span, .conditions, .source-note, .selection-note, .group-sources { color: var(--fg-dim); }
.conditions, .source-note, .group-sources p { margin: 5px 0 0; line-height: 1.6; white-space: pre-wrap; overflow-wrap: anywhere; }
.source-files > summary { padding: 8px 0; }
.source-files :deep(.ai-card) { margin: 4px 0 0; }
.selection-note { margin: 0 0 12px; font-size: 12px; line-height: 1.6; }
.group-sources { padding: 0 20px 14px; font-size: 11px; }
.group-sources > div + div { margin-top: 8px; }
.group-sources strong { font-weight: 500; }
.query-action { max-width: 100%; margin-top: 8px; text-align: start; white-space: normal; overflow-wrap: anywhere; }
.result-actions { display: flex; justify-content: flex-end; flex-wrap: wrap; gap: 8px; padding: 12px 20px; }
.result-note { flex-basis: 100%; margin: 0; color: var(--fg-dim); font-size: 11px; line-height: 1.6; }
.result-actions button { min-height: 39px; max-width: 100%; padding: 8px 15px; border-radius: 12px; font-size: 12px; white-space: normal; overflow-wrap: anywhere; }
.query-action:focus-visible, .result-actions button:focus-visible { outline: 2px solid var(--accent); outline-offset: 2px; }
.attempt-heading, .conditions, .source-note, .selection-note, .group-sources { -webkit-user-select: text; user-select: text; --default-contextmenu: show; }
</style>
