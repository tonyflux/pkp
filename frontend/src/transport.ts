/*
 * The playback transport shared by the full player and the browse dock:
 * progress bookkeeping around the scrub bar, the "up next" notice, the play
 * modes and the keyboard map. Both views render the same session, so they
 * must agree on every one of these.
 */
import { computed, ref, watch } from "vue";
import { isTyping } from "./dom";
import { useScrub } from "./scrub";
import { cancelNextOnce, nextUp, player, playerCommands } from "./store";
import type { PlayMode } from "./store";
import { t } from "./i18n";

export const modes: { value: PlayMode; readonly label: string; icon: string }[] = [
  { value: "sequential", get label() { return t("顺序播放"); }, icon: "list" },
  { value: "one", get label() { return t("单集循环"); }, icon: "repeatOne" },
  { value: "shuffle", get label() { return t("随机播放"); }, icon: "shuffle" },
  { value: "stop", get label() { return t("播完停止"); }, icon: "stopAfter" },
];

export function useTransport() {
  const scrubEl = ref<HTMLElement>();
  const committed = ref<number | null>(null);
  const nextDismissed = ref(false);

  const state = computed(() => player.state);
  const position = computed(() =>
    scrub.scrubbing.value ? scrub.value.value : (committed.value ?? state.value.position),
  );
  const progress = computed(() => percent(position.value));
  const cached = computed(() => percent(state.value.position + state.value.cacheSeconds));

  // The state in hand still describes where playback was before the seek; the
  // service reports the target from the next event on, so the committed value
  // stands in until that event arrives rather than snapping back.
  const scrub = useScrub(
    scrubEl,
    () => state.value.duration,
    () => position.value,
    (target) => {
      committed.value = target;
      void playerCommands.seekTo(target);
    },
  );

  function percent(seconds: number): number {
    const d = state.value.duration;
    return d > 0 ? Math.min(100, Math.max(0, (seconds / d) * 100)) : 0;
  }

  watch(
    () => player.state,
    () => {
      committed.value = null;
    },
  );

  // "Up next" shows in the last ten seconds, when it is still actionable.
  const showNext = computed(() => {
    const s = state.value;
    return (
      !nextDismissed.value &&
      !!nextUp.value &&
      s.duration > 0 &&
      s.duration - s.position <= 10 &&
      s.duration - s.position > 0
    );
  });

  function dismissNext() {
    nextDismissed.value = true;
    cancelNextOnce();
  }

  watch(
    () => player.index,
    () => {
      nextDismissed.value = false;
    },
  );

  return { scrubEl, scrub, state, position, percent, progress, cached, showNext, dismissNext };
}

export interface PlayerKeyOptions {
  /** What `f` does in this view. */
  fullscreen: () => void;
  /** What `s` does: save a screenshot. */
  screenshot: () => void;
  /** What Escape does; false leaves the key to the browser. */
  escape: () => boolean;
  /** Runs after a handled key. */
  after?: () => void;
}

/**
 * usePlayerKeys returns the keydown handler for the player's
 * keyboard map. It stays out of the way while something is being typed.
 */
export function usePlayerKeys(opts: PlayerKeyOptions): (event: KeyboardEvent) => void {
  return (event) => {
    if (isTyping(event)) return;
    if (event.metaKey || event.ctrlKey || event.altKey) return;
    const s = player.state;

    switch (event.key) {
      case " ":
        void playerCommands.toggle();
        break;
      case "ArrowLeft":
        void playerCommands.seek(-15);
        break;
      case "ArrowRight":
        void playerCommands.seek(15);
        break;
      case "ArrowUp":
        void playerCommands.volume(Math.min(100, Math.round(s.volume) + 5));
        break;
      case "ArrowDown":
        void playerCommands.volume(Math.max(0, Math.round(s.volume) - 5));
        break;
      case "m":
      case "M":
        void playerCommands.mute(!s.muted);
        break;
      case "f":
      case "F":
        opts.fullscreen();
        break;
      case "s":
      case "S":
        opts.screenshot();
        break;
      case "Escape":
        if (!opts.escape()) return;
        break;
      default:
        return;
    }
    event.preventDefault();
    opts.after?.();
  };
}
