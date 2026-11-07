const clamp = (value, min, max) => Math.min(max, Math.max(min, value));
const controls = 'button,input,select,textarea,a,label,summary,[contenteditable],[data-select-menu],[data-player-action],[role="button"],[role="slider"],[role="menu"],[role="menuitem"]';
const touchPointer = event => event.pointerType === 'touch' || event.pointerType === 'pen';

export function mountPlayerGestures({ stage, media, onPreview, onCommitSeek, onBrightness, getBrightness, onVolume, onTap, onActiveChange, signal }) {
  const controller = new AbortController();
  const view = stage.ownerDocument.defaultView;
  const pointers = new Set();
  let current = null, pendingTap = null, tapTimer, destroyed = false;
  let suppressUntil = 0, suppressX = 0, suppressY = 0;
  const listen = (target, type, handler, options = {}) => target.addEventListener(type, handler, { ...options, signal: controller.signal });
  const isControl = target => Boolean(target?.closest?.(controls));
  const logicalPoint = (event, rect, rotated) => rotated
    ? { x: event.clientY - rect.top, y: rect.right - event.clientX }
    : { x: event.clientX - rect.left, y: event.clientY - rect.top };
  const clearTap = () => {
    clearTimeout(tapTimer);
    pendingTap = null;
  };
  const suppressClick = event => {
    suppressUntil = Date.now() + 800;
    suppressX = event.clientX;
    suppressY = event.clientY;
  };
  const release = gesture => {
    try {
      if (stage.hasPointerCapture?.(gesture.id)) stage.releasePointerCapture(gesture.id);
    } catch { /* The browser may already have released a cancelled pointer. */ }
  };
  function cancel() {
    clearTap();
    const gesture = current;
    current = null;
    if (gesture) {
      release(gesture);
      if (gesture.kind) onActiveChange?.(false);
    }
    onPreview?.(null);
  }
  function tap(event, gesture) {
    const position = logicalPoint(event, gesture.rect, gesture.rotated);
    const point = {
      x: clamp(position.x / gesture.width, 0, 1),
      y: clamp(position.y / gesture.height, 0, 1),
    };
    if (event.pointerType === 'pen') {
      onTap?.({ ...point, double: false });
      return;
    }
    const now = Date.now();
    if (pendingTap && now - pendingTap.time <= 280 && Math.hypot(position.x - pendingTap.x, position.y - pendingTap.y) <= 40) {
      clearTap();
      onTap?.({ ...point, double: true });
      return;
    }
    if (pendingTap) {
      const previous = pendingTap.point;
      clearTap();
      onTap?.({ ...previous, double: false });
    }
    pendingTap = { point, time: now, x: position.x, y: position.y };
    tapTimer = setTimeout(() => {
      const previous = pendingTap;
      pendingTap = null;
      if (!destroyed && previous && !media.error) onTap?.({ ...previous.point, double: false });
    }, 280);
  }
  function down(event) {
    if (!touchPointer(event)) {
      // A real mouse press must not inherit the preceding touch's click suppression.
      suppressUntil = 0;
      return;
    }
    pointers.add(event.pointerId);
    if (pointers.size > 1) {
      suppressClick(event);
      cancel();
      return;
    }
    if (event.isPrimary === false || event.button > 0 || isControl(event.target)) return;
    suppressClick(event);
    if (media.error) return;
    const rect = stage.getBoundingClientRect();
    const viewportWidth = stage.ownerDocument.documentElement.clientWidth || view.innerWidth;
    // Reserve physical screen edges for browser/OS back navigation, not inset stage edges.
    if (event.clientX <= 16 || event.clientX >= viewportWidth - 16 || rect.width <= 0 || rect.height <= 0) return;
    const rotated = Boolean(stage.closest('.player-page')?.classList.contains('is-rotated'));
    const point = logicalPoint(event, rect, rotated);
    const width = rotated ? rect.height : rect.width;
    const height = rotated ? rect.width : rect.height;
    current = {
      id: event.pointerId, rect, rotated, width, height, x: point.x, y: point.y,
      time: Number.isFinite(media.currentTime) ? media.currentTime : 0,
      duration: media.duration, brightness: clamp(getBrightness(), 0.2, 1),
      volume: clamp(media.volume, 0, 1), left: point.x < width / 2,
      kind: '', moved: false, target: null,
    };
  }
  function move(event) {
    const gesture = current;
    if (!gesture || !touchPointer(event) || event.pointerId !== gesture.id) return;
    if (media.error) {
      cancel();
      return;
    }
    const point = logicalPoint(event, gesture.rect, gesture.rotated);
    const dx = point.x - gesture.x, dy = point.y - gesture.y;
    if (!gesture.kind) {
      if (Math.hypot(dx, dy) < 10) return;
      gesture.moved = true;
      clearTap();
      const horizontal = Math.abs(dx) >= Math.abs(dy) * 1.2;
      const vertical = Math.abs(dy) >= Math.abs(dx) * 1.2;
      if (!horizontal && !vertical) return;
      if (horizontal && (!Number.isFinite(gesture.duration) || gesture.duration <= 0)) {
        cancel();
        return;
      }
      gesture.kind = horizontal ? 'seek' : gesture.left ? 'brightness' : 'volume';
      onActiveChange?.(true);
      try { stage.setPointerCapture?.(gesture.id); } catch { /* Document release still cancels or completes the gesture. */ }
    }
    if (gesture.kind === 'seek') {
      const scale = Math.min(120, Math.max(10, gesture.duration * 0.2));
      gesture.target = clamp(gesture.time + dx / gesture.width * scale, 0, gesture.duration);
      onPreview?.({ kind: 'seek', value: gesture.target, delta: gesture.target - gesture.time });
    } else if (gesture.kind === 'brightness') {
      const value = clamp(gesture.brightness - dy / gesture.height, 0.2, 1);
      onBrightness(value);
      onPreview?.({ kind: 'brightness', value });
    } else {
      const value = clamp(gesture.volume - dy / gesture.height, 0, 1);
      onPreview?.(onVolume(value) === false ? { kind: 'system-volume' } : { kind: 'volume', value });
    }
  }
  function up(event) {
    if (!touchPointer(event)) return;
    const tracked = pointers.delete(event.pointerId);
    if (tracked && !isControl(event.target)) suppressClick(event);
    const gesture = current;
    if (!gesture || gesture.id !== event.pointerId) return;
    move(event);
    if (current !== gesture) return;
    current = null;
    release(gesture);
    if (gesture.kind) {
      onActiveChange?.(false);
      if (!media.error && gesture.kind === 'seek' && gesture.target !== null && Number.isFinite(media.duration) && media.duration > 0) onCommitSeek(gesture.target);
      else if (media.error || gesture.kind === 'seek') onPreview?.(null);
      return;
    }
    const rect = gesture.rect;
    if (!gesture.moved && !media.error && event.clientX >= rect.left && event.clientX <= rect.right && event.clientY >= rect.top && event.clientY <= rect.bottom) tap(event, gesture);
  }
  function pointerCancel(event) {
    if (!touchPointer(event)) return;
    pointers.delete(event.pointerId);
    if (!current || current.id === event.pointerId) cancel();
  }
  function lostCapture(event) {
    if (event.target === stage && current?.id === event.pointerId) cancel();
  }
  function click(event) {
    if (isControl(event.target)) return;
    const syntheticTouch = touchPointer(event) || event.sourceCapabilities?.firesTouchEvents;
    const nearLastTouch = Date.now() <= suppressUntil && Math.hypot(event.clientX - suppressX, event.clientY - suppressY) <= 40;
    if (!syntheticTouch && !nearLastTouch) return;
    event.preventDefault();
    event.stopImmediatePropagation();
  }
  function destroy() {
    if (destroyed) return;
    destroyed = true;
    cancel();
    pointers.clear();
    controller.abort();
    signal?.removeEventListener('abort', destroy);
  }
  if (signal?.aborted) destroy();
  else {
    listen(stage, 'pointerdown', down, { capture: true });
    listen(stage, 'pointermove', move);
    listen(stage, 'pointerup', up);
    listen(stage, 'pointercancel', pointerCancel);
    listen(stage, 'lostpointercapture', lostCapture);
    listen(stage, 'click', click, { capture: true });
    // Fallback for browsers without capture, and pointers released before direction lock.
    listen(stage.ownerDocument, 'pointerdown', event => {
      if (touchPointer(event) && pointers.size && !stage.contains(event.target)) {
        pointers.add(event.pointerId);
        cancel();
      }
    });
    listen(stage.ownerDocument, 'pointermove', event => {
      if (!stage.contains(event.target)) move(event);
    });
    listen(stage.ownerDocument, 'pointerup', up);
    listen(stage.ownerDocument, 'pointercancel', pointerCancel);
    listen(view, 'resize', cancel);
    listen(view, 'orientationchange', cancel);
    listen(view, 'playerrotationchange', cancel);
    if (view.visualViewport) listen(view.visualViewport, 'resize', cancel);
    if (view.screen.orientation) listen(view.screen.orientation, 'change', cancel);
    listen(stage.ownerDocument, 'fullscreenchange', cancel);
    listen(stage.ownerDocument, 'webkitfullscreenchange', cancel);
    signal?.addEventListener('abort', destroy, { once: true });
  }
  return { destroy };
}
