<script setup lang="ts">
// The 64px row under the page while the picture is not up:
// the session carries on — pause, progress (seekable, like the player's
// scrub bar), Restore video back to the immersive player, and stopping stays an
// explicit choice. It is a row of its own, so the page above loses height,
// never width or its last row. While the separate player window has the
// picture, the same bar brings that window forward or the picture back.
import { computed, ref } from "vue";
import Icon from "./Icon.vue";
import { clock, maskLogin } from "../format";
import { useTransport } from "../transport";
import {
  accountLabel,
  accounts,
  expandPlayer,
  nowPlaying,
  player,
  playerCommands,
  playingElsewhere,
  playingOwner,
  popOut,
  returnToMain,
  returnToOwner,
  stopPlayback,
} from "../store";
import { t } from "../i18n";

const { scrubEl, scrub, state, position, progress, percent } = useTransport();

// The time under the pointer, shown above the bar before a click commits it.
const hoverTime = ref<number | null>(null);
function onScrubMove(event: PointerEvent) {
  scrub.onPointerMove(event);
  hoverTime.value = scrub.valueAt(event);
}
const popped = computed(() => player.presentation === "window");
// Music has no picture to restore: the bar opens the music page instead.
const expandLabel = computed(() => (nowPlaying.value?.category === "audio" ? t("展开播放器") : t("恢复画面")));
// Whose video this is: shown when several accounts exist or it is not the
// account on screen.
const showOwner = computed(() => !!playingOwner.value && (accounts.list.length > 1 || playingElsewhere.value));
const ownerLabel = computed(() => accountLabel(playingOwner.value) || t("已移除的账号"));
const backLabel = computed(() => {
  const label = accountLabel(playingOwner.value);
  return label ? t("返回「{name}」", { name: label }) : t("返回已移除的账号");
});
const ownerMasked = computed(() => maskLogin(playingOwner.value?.login ?? ""));
const ownerTitle = computed(() =>
  ownerMasked.value
    ? t("所属账号 · {name}（{login}）", { name: ownerLabel.value, login: ownerMasked.value })
    : t("所属账号 · {name}", { name: ownerLabel.value }),
);
</script>

<template>
  <div class="bar glass" data-playback role="region" :aria-label="t('正在播放')">
    <button class="main" :title="state.paused ? t('播放') : t('暂停')" @click="playerCommands.toggle()">
      <Icon :name="state.paused ? 'play' : 'pause'" :size="18" filled />
    </button>

    <button class="restore" :title="popped ? t('显示播放窗口') : expandLabel" @click="popped ? popOut() : expandPlayer()">
      <span class="label">
        <small>{{ popped ? (state.paused ? t("在独立窗口播放 · 已暂停") : t("在独立窗口播放")) : state.paused ? t("正在播放 · 已暂停") : t("正在播放") }}</small>
        <strong :title="nowPlaying?.name">{{ nowPlaying?.name ?? "—" }}</strong>
      </span>
    </button>

    <div
      ref="scrubEl"
      class="scrub"
      :class="{ scrubbing: scrub.scrubbing.value }"
      role="slider"
      tabindex="0"
      :aria-label="t('播放进度')"
      :aria-valuemin="0"
      :aria-valuemax="Math.round(state.duration)"
      :aria-valuenow="Math.round(position)"
      :aria-valuetext="clock(position)"
      :aria-disabled="state.duration <= 0"
      @pointerdown="scrub.onPointerDown"
      @pointermove="onScrubMove"
      @pointerup="scrub.onPointerUp"
      @pointercancel="scrub.onPointerUp"
      @pointerleave="hoverTime = null"
      @keydown="scrub.onKeyDown"
    >
      <div class="track"><i :style="{ width: progress + '%' }" /></div>
      <i class="knob" :style="{ left: progress + '%' }" />
      <span v-if="hoverTime !== null" class="hover mono" :style="{ left: percent(hoverTime) + '%' }">{{ clock(hoverTime) }}</span>
    </div>
    <span class="time mono">{{ clock(position) }} / {{ clock(state.duration) }}</span>

    <!-- Beside Switch to this account the chip is the account itself: the button says what it is for. -->
    <div v-if="showOwner" class="owner" :title="ownerTitle">
      <span class="badge"><span v-if="!playingElsewhere" class="long">{{ t("所属账号") }} · </span>{{ ownerLabel }}</span>
      <small v-if="ownerMasked" class="masked">{{ ownerMasked }}</small>
    </div>
    <button v-if="showOwner && playingElsewhere" class="quiet back" :title="backLabel" @click="returnToOwner">
      {{ t("bar|切回该账号") }}
    </button>

    <template v-if="popped">
      <button class="quiet icon" :title="t('显示播放窗口')" @click="popOut">
        <Icon name="popout" :size="17" />
      </button>
      <button class="quiet icon" :title="t('回到主窗口播放')" @click="returnToMain">
        <Icon name="popin" :size="17" />
      </button>
    </template>
    <template v-else>
      <button class="quiet icon" :title="expandLabel" @click="expandPlayer">
        <Icon name="expand" :size="17" />
      </button>
      <button class="quiet icon" :title="t('独立窗口播放')" :aria-label="t('独立窗口播放')" @click="popOut">
        <Icon name="popout" :size="17" />
      </button>
    </template>
    <button class="quiet icon stop" :title="t('停止播放')" @click="stopPlayback">
      <Icon name="stop" :size="17" />
    </button>
  </div>
</template>

<style scoped>
.bar {
  flex: none;
  height: 64px;
  display: flex;
  align-items: center;
  gap: 10px;
  padding: 0 12px 0 10px;
  border-radius: 20px;
}

/* Hover keeps the fill and rim (the plain button:hover outranks this rule);
   border-box: see button.primary in style.css. */
.main,
.main:hover:not(:disabled) {
  width: 44px;
  height: 44px;
  min-height: 44px;
  padding: 0;
  border-radius: 50%;
  border: 1px solid rgba(255, 255, 255, 0.35);
  color: #ffffff;
  background: var(--primary) border-box;
  box-shadow: inset 0 1px 0 var(--primary-rim), var(--primary-shadow);
}

/* A bounded title slot leaves room for the timeline and playback actions. */
.restore {
  flex: 0 1 300px;
  min-width: 132px;
  height: 46px;
  justify-content: flex-start;
  padding: 6px 10px;
  border: 0;
  border-radius: 14px;
  background: transparent;
  text-align: left;
}

.restore:hover:not(:disabled) {
  background: color-mix(in srgb, var(--fg) 6%, transparent);
}

.label {
  flex: 1;
  display: flex;
  flex-direction: column;
  min-width: 0;
  line-height: 1.25;
}

.label small {
  font-size: 12px;
  color: var(--fg-dim);
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}

.label strong {
  font-size: 15px;
  font-weight: 600;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

/* The player's scrub bar at the bar's scale: a 28px hit row around a 4px
   track; the knob comes up on hover, focus and while dragging. */
.scrub {
  position: relative;
  flex: 1;
  min-width: 90px;
  height: 28px;
  display: flex;
  align-items: center;
  cursor: pointer;
  touch-action: none;
  outline: none;
}

.scrub[aria-disabled="true"] {
  cursor: default;
}

.track {
  position: absolute;
  left: 0;
  right: 0;
  height: 4px;
  border-radius: 999px;
  background: var(--line-strong);
  overflow: hidden;
  transition: height var(--dur) var(--ease);
}

.track i {
  display: block;
  height: 100%;
  border-radius: inherit;
  background: var(--progress);
  transition: width 0.2s linear;
}

/* While dragging the bar follows the pointer, not an eased catch-up. */
.scrubbing .track i,
.scrubbing .knob {
  transition: none;
}

.scrub:hover .track,
.scrub.scrubbing .track {
  height: 6px;
}

.knob {
  position: absolute;
  top: 50%;
  width: 12px;
  height: 12px;
  margin: -6px 0 0 -6px;
  border-radius: 50%;
  background: #ffffff;
  box-shadow:
    0 0 0 3px color-mix(in srgb, var(--progress-from) 35%, transparent),
    0 1px 3px rgba(0, 0, 0, 0.4);
  opacity: 0;
  transform: scale(0.6);
  transition:
    left 0.2s linear,
    opacity var(--dur) var(--ease),
    transform var(--dur) var(--ease);
  pointer-events: none;
}

.scrub:hover .knob,
.scrub:focus-visible .knob,
.scrub.scrubbing .knob {
  opacity: 1;
  transform: none;
}

.scrub:focus-visible .knob {
  box-shadow: 0 0 0 3px color-mix(in srgb, var(--progress-from) 70%, transparent);
}

.scrub[aria-disabled="true"] .knob {
  display: none;
}

.hover {
  position: absolute;
  bottom: 24px;
  transform: translateX(-50%);
  padding: 2px 7px;
  border-radius: 7px;
  font-size: 11px;
  white-space: nowrap;
  color: var(--fg);
  background: var(--glass-panel);
  border: 1px solid var(--glass-edge);
  box-shadow: 0 4px 12px rgba(0, 0, 0, 0.25);
  pointer-events: none;
}

.time {
  color: var(--fg-dim);
  font-size: 12px;
  white-space: nowrap;
}

/* The file name gives way, not the account (200px at most, then ellipsis). */
.owner {
  flex: none;
  min-width: 0;
  max-width: 200px;
  display: flex;
  flex-direction: column;
  align-items: flex-start;
  gap: 2px;
  line-height: 1.25;
}

.badge {
  max-width: 100%;
  padding: 2px 8px;
  border-radius: 999px;
  font-size: 12px;
  font-weight: 600;
  color: var(--accent);
  background: var(--accent-soft);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.masked {
  max-width: 100%;
  font-size: 12px;
  color: var(--fg-dim);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.back {
  flex: none;
  white-space: nowrap;
}

.main,
.stop,
.bar > .icon {
  flex: none;
}

@media (max-width: 900px) {
  .masked {
    display: none;
  }
}

@media (max-width: 720px) {
  .badge .long {
    display: none;
  }

  .owner {
    max-width: 110px;
  }
}

.stop:hover:not(:disabled) {
  color: var(--danger);
}
</style>
