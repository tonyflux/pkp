<script setup lang="ts">
// The playlist's videos, the one playing marked; a click plays one. The full
// player's playlist island shows it, and so does the separate window's
// drawer, compact. The list opens on the video playing.
import { onMounted, ref } from "vue";
import Icon from "./Icon.vue";
import Thumb from "./Thumb.vue";
import { clock } from "../format";
import { playAt, player } from "../store";
import { t } from "../i18n";

defineProps<{ compact?: boolean }>();

const list = ref<HTMLElement>();

onMounted(() => list.value?.querySelector(".on")?.scrollIntoView({ block: "nearest" }));
</script>

<template>
  <div ref="list" class="queue" :class="{ compact }">
    <button
      v-for="(item, i) in player.playlist"
      :key="item.id"
      class="queue-item"
      :class="{ on: i === player.index }"
      @click="playAt(i)"
    >
      <span class="queue-thumb">
        <Thumb :src="item.thumbnail" :alt="item.name" lazy>
          <Icon name="video" :size="20" />
        </Thumb>
        <Icon v-if="i === player.index" class="queue-play" name="play" :size="15" filled />
      </span>
      <span class="queue-text">
        <span class="queue-name" :title="item.name">{{ item.name }}</span>
        <small v-if="item.duration">{{ clock(item.duration) }}</small>
      </span>
    </button>
    <p v-if="!player.playlist.length" class="queue-empty">{{ t("从文件页选一个视频开始播放") }}</p>
  </div>
</template>

<style scoped>
.queue {
  flex: 1;
  overflow-y: auto;
  padding: 4px 12px 12px;
  display: flex;
  flex-direction: column;
  gap: 10px;
}

/* flex: none — in a list that scrolls, shrinking items squeezed two-line
   names into the padding, up against the hover fill's edge. */
.queue-item {
  flex: none;
  justify-content: flex-start;
  gap: 16px;
  padding: 10px;
  min-height: 100px;
  border-radius: 18px;
  border: 1px solid transparent;
  background: transparent;
  text-align: left;
}

.queue-item:hover {
  background: color-mix(in srgb, var(--glass-control) 55%, transparent);
}

.queue-item.on {
  border-color: var(--accent);
  background: var(--accent-soft);
  box-shadow: 0 0 0 1px color-mix(in srgb, var(--accent) 35%, transparent);
}

.queue-thumb {
  position: relative;
  width: 124px;
  height: 78px;
  flex: none;
  border-radius: 10px;
  overflow: hidden;
  background: color-mix(in srgb, var(--fg) 12%, transparent);
  display: grid;
  place-items: center;
  color: var(--fg-dim);
}

.queue-play {
  position: absolute;
  left: 8px;
  bottom: 8px;
  color: #fff;
  filter: drop-shadow(0 1px 3px rgba(0, 0, 0, 0.6));
}

.queue-text {
  flex: 1;
  min-width: 0;
  display: flex;
  flex-direction: column;
  gap: 4px;
}

.queue-name {
  font-size: 17px;
  font-weight: 600;
  line-height: 1.3;
  /* Buttons do not wrap; a name takes up to two lines. */
  white-space: normal;
  display: -webkit-box;
  -webkit-line-clamp: 2;
  -webkit-box-orient: vertical;
  overflow: hidden;
  overflow-wrap: anywhere;
}

.queue-text small {
  font-size: 14px;
  font-variant-numeric: tabular-nums;
  color: var(--fg-dim);
}

.queue-empty {
  margin: 30px 10px;
  text-align: center;
  font-size: 13px;
  color: var(--fg-dim);
}

/* The drawer over the picture is narrower: smaller pictures and type. */
.compact {
  gap: 6px;
  padding: 2px 8px 10px;
}

.compact .queue-item {
  gap: 12px;
  padding: 7px 12px 7px 7px;
  min-height: 66px;
  border-radius: 14px;
}

.compact .queue-thumb {
  width: 88px;
  height: 50px;
  border-radius: 8px;
}

.compact .queue-name {
  font-size: 14px;
}

.compact .queue-text small {
  font-size: 12px;
}
</style>
