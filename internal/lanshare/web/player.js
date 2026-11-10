import { translate } from './locales.js';
import { mountSelect, selectHTML } from './select.js';
import { mountPlayerGestures } from './gestures.js';

const escapeHTML = value => String(value ?? '').replace(/[&<>"']/g, character => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' })[character]);
const paths = {
  back: '<path d="m14 5-7 7 7 7M7 12h14"/>',
  play: '<path d="m9 5 11 7-11 7z"/>',
  pause: '<path d="M8 5v14M16 5v14"/>',
  backward: '<path d="M5 8a8 8 0 1 1-1 8M5 3v5h5"/><text x="8" y="16" stroke="none" fill="currentColor" font-size="8">10</text>',
  forward: '<path d="M19 8a8 8 0 1 0 1 8M19 3v5h-5"/><text x="8" y="16" stroke="none" fill="currentColor" font-size="8">10</text>',
  volume: '<path d="m11 4-6 5H2v6h3l6 5zM15 8a6 6 0 0 1 0 8M18 4a11 11 0 0 1 0 16"/>',
  muted: '<path d="m11 4-6 5H2v6h3l6 5zM16 9l6 6M22 9l-6 6"/>',
  fullscreen: '<path d="M8 3H3v5M16 3h5v5M3 16v5h5M21 16v5h-5"/>',
  exitFullscreen: '<path d="M3 8h5V3M21 8h-5V3M8 21v-5H3M16 21v-5h5"/>',
  pip: '<rect x="2" y="4" width="20" height="16" rx="2"/><rect x="11" y="11" width="8" height="6" rx="1"/>',
  playlist: '<path d="M3 5h14M3 10h14M3 15h8M17 14v7m-3-3 3 3 3-3"/>',
  more: '<circle cx="12" cy="5" r="1"/><circle cx="12" cy="12" r="1"/><circle cx="12" cy="19" r="1"/>',
  audio: '<path d="M9 18V5l11-2v13M9 7l11-2"/><ellipse cx="6" cy="18" rx="3" ry="3"/><ellipse cx="17" cy="16" rx="3" ry="3"/>',
  video: '<rect x="3" y="4" width="18" height="16" rx="3"/><path d="m10 8 6 4-6 4z"/>',
  brightness: '<circle cx="12" cy="12" r="4"/><path d="M12 2v2M12 20v2M2 12h2M20 12h2M5 5l1.5 1.5M17.5 17.5 19 19M5 19l1.5-1.5M17.5 6.5 19 5"/>',
  close: '<path d="m6 6 12 12M18 6 6 18"/>'
};
const icon = name => `<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">${paths[name]}</svg>`;
const clock = seconds => {
  if (!Number.isFinite(seconds) || seconds < 0) return '--:--';
  const value = Math.floor(seconds), hours = Math.floor(value / 3600);
  return `${hours ? `${hours}:` : ''}${String(Math.floor(value / 60) % 60).padStart(2, '0')}:${String(value % 60).padStart(2, '0')}`;
};

export function mountPlayer({ container, entry, source, variant = 'original', language = 'en', connected = true, playlist = [], onBack, onSelect, onDetails, onDownload, onOptimizeStartup, onError }) {
  const t = key => translate(language, key);
  const h = escapeHTML;
  const byteNumber = new Intl.NumberFormat(language, { maximumFractionDigits: 0 });
  const sizeNumber = new Intl.NumberFormat(language, { maximumFractionDigits: 1 });
  const size = value => {
    if (!Number.isFinite(value) || value < 0) return t('unknown');
    const units = ['B', 'KB', 'MB', 'GB', 'TB'];
    const order = value > 0 ? Math.min(4, Math.floor(Math.log(value) / Math.log(1024))) : 0;
    return `${(order ? sizeNumber : byteNumber).format(value / 1024 ** order)} ${units[order]}`;
  };
  const isAudio = /^audio\//i.test(entry.mime || '') || (!/^video\//i.test(entry.mime || '') && /\.(mp3|m4a|aac|flac|ogg|opus|wav)$/i.test(entry.name || ''));
  const badge = t(variant === 'compatible' ? 'compatibleShort' : 'original');
  const button = (action, label, image, className = '', extra = '') => `<button type="button" class="icon-button ${className}" data-player-action="${action}" aria-label="${h(t(label))}" title="${h(t(label))}" ${extra}>${icon(image)}</button>`;
  const transport = () => `${button('play', 'play', 'play', 'player-play')}${button('backward', 'skipBack', 'backward', 'player-skip-back')}${button('forward', 'skipForward', 'forward', 'player-skip-forward')}`;
  const mediaTag = isAudio ? 'audio' : 'video';
  container.className = `player-page${isAudio ? ' is-audio' : ''}`;
  container.tabIndex = -1;
  container.setAttribute('aria-label', entry.name);
  container.innerHTML = `
    <div class="player-surface">
    <header class="player-header">
      <button type="button" class="player-back" data-player-action="back" aria-label="${h(t('backToFolder'))}">${icon('back')}<span>${h(t('backToFolder'))}</span></button>
      <div class="player-heading"><strong title="${h(entry.name)}">${h(entry.name)}</strong><span class="variant-badge">${h(badge)}</span></div>
      <div class="player-header-actions">
        <span data-connection><span class="connection${connected ? '' : ' offline'}" role="status">${h(t(connected ? 'connected' : 'offline'))}</span></span>
        ${button('pip', 'pictureInPicture', 'pip', 'player-pip player-header-pip', 'hidden')}
        <button type="button" class="player-playlist-toggle" data-player-action="playlist" aria-label="${h(t('sameFolder'))} (${playlist.length})" aria-expanded="false" aria-controls="player-playlist">${icon('playlist')}<span>${h(t('sameFolder'))} (${playlist.length})</span></button>
        ${button('menu', 'more', 'more', 'player-more', 'aria-haspopup="menu" aria-expanded="false" aria-controls="player-menu"')}
        <div id="player-menu" class="player-menu" role="menu" hidden>
          <button type="button" role="menuitem" data-player-action="details"><span>${h(t('playbackInfo'))}</span></button>
          <button type="button" role="menuitem" data-player-action="download"><span>${h(t('download'))}</span></button>
          ${isAudio ? '' : `<button type="button" role="menuitem" class="player-compact-action" data-player-action="brightness" aria-expanded="false" aria-controls="player-brightness-panel">${icon('brightness')}<span>${h(t('pictureBrightness'))}</span></button>`}
          <button type="button" role="menuitem" class="player-compact-action" data-player-action="mute">${icon('volume')}<span>${h(t('mute'))}</span></button>
          <button type="button" role="menuitem" class="player-compact-action player-pip" data-player-action="pip" hidden>${icon('pip')}<span>${h(t('pictureInPicture'))}</span></button>
          <button type="button" role="menuitem" data-player-action="system-fullscreen" hidden><span>${h(t('systemFullscreen'))}</span></button>
          ${isAudio ? '' : `<button type="button" role="menuitem" data-player-action="gestures"><span>${h(t('playerGestures'))}</span></button>`}
          ${onOptimizeStartup ? `<button type="button" role="menuitem" data-player-action="optimize-startup"><span>${h(t('optimizeStartup'))}</span></button>` : ''}
        </div>
      </div>
    </header>
    <div class="player-layout">
      <div class="player-main">
        <div class="player-frame">
        <div class="player-stage" aria-busy="true">
          <${mediaTag} class="player-media" playsinline preload="metadata" aria-label="${h(entry.name)}"></${mediaTag}>
          <div class="player-brightness-shade" aria-hidden="true"></div>
          <div class="player-gesture-feedback" role="status" aria-live="polite" hidden><span class="player-gesture-icon" aria-hidden="true"></span><strong></strong><small></small></div>
          ${isAudio ? `<div class="audio-art" aria-hidden="true">${icon('audio')}</div>` : ''}
          <div class="player-status" data-state="loading"><span class="player-status-spinner" aria-hidden="true"><span></span></span><span class="player-status-text" role="status" aria-live="polite" aria-atomic="true">${h(t('loadingMedia'))}</span></div>
        </div>
        <div class="player-controlbar">
          <div class="player-timeline">
            <span class="player-elapsed">00:00</span>
            <div class="seek-track"><div class="seek-buffer" aria-hidden="true"></div><span class="seek-played" aria-hidden="true"></span><input class="player-seek" type="range" min="0" max="0" value="0" step="0.1" aria-label="${h(t('seek'))}" disabled></div>
            <span class="player-duration">--:--</span>
          </div>
          <div class="player-bottom">
            <div class="player-transport">${transport()}<div class="player-volume">${button('mute', 'mute', 'volume', 'player-mute')}<input class="player-volume-range" type="range" min="0" max="1" step="0.05" value="1" aria-label="${h(t('volume'))}"></div></div>
            <div class="player-options">
              ${isAudio ? '' : button('brightness', 'pictureBrightness', 'brightness', 'player-brightness-toggle', 'aria-expanded="false" aria-controls="player-brightness-panel"')}
              ${selectHTML({ id: 'player-speed', label: t('speed'), value: 1, className: 'player-speed', options: [0.5, 0.75, 1, 1.25, 1.5, 1.75, 2].map(rate => ({ value: rate, label: `${rate === 1 ? '1.0' : rate}×` })) })}
              ${button('pip', 'pictureInPicture', 'pip', 'player-pip', 'hidden')}
              ${button('fullscreen', 'fullscreen', 'fullscreen', 'player-fullscreen', 'hidden')}
            </div>
          </div>
          ${isAudio ? '' : `<div id="player-brightness-panel" class="player-brightness" role="group" aria-label="${h(t('pictureBrightness'))}" hidden><label for="player-brightness-range">${icon('brightness')}<span>${h(t('pictureBrightness'))}</span><output>100%</output></label><input id="player-brightness-range" class="player-brightness-range" type="range" min="0.2" max="1" step="0.05" value="1" aria-label="${h(t('pictureBrightness'))}"><p>${h(t('pictureBrightnessNote'))}</p></div>`}
          <div class="player-help" hidden><strong>${h(t('playerGestures'))}</strong><p>${h(t('playerGestureHelp'))}</p><p>${h(t('pictureBrightnessNote'))}</p><p>${h(t('pageFullscreenNote'))}</p></div>
        </div>
        </div>
        <div class="player-caption"><strong>${h(entry.name)}</strong><small>${h(size(entry.size))}</small><span class="variant-badge">${h(badge)}</span></div>
      </div>
      <aside id="player-playlist" class="player-playlist" aria-label="${h(t('sameFolder'))}" hidden>
        <div class="playlist-heading"><h2>${h(t('sameFolder'))} <span>${playlist.length}</span></h2>${button('playlist', 'close', 'close')}</div>
        <div class="playlist-list"><button type="button" class="playlist-load-more secondary" data-player-action="playlist-more" hidden>${h(t('loadMore'))}</button></div>
      </aside>
    </div></div>`;

  const media = container.querySelector('.player-media');
  const stage = container.querySelector('.player-stage');
  const status = container.querySelector('.player-status');
  const statusText = container.querySelector('.player-status-text');
  const seek = container.querySelector('.player-seek');
  const played = container.querySelector('.seek-played');
  const buffered = container.querySelector('.seek-buffer');
  const elapsed = container.querySelector('.player-elapsed');
  const duration = container.querySelector('.player-duration');
  const volume = container.querySelector('.player-volume-range');
  const speed = container.querySelector('.player-speed');
  const menu = container.querySelector('.player-menu');
  const more = container.querySelector('.player-more');
  const list = container.querySelector('.player-playlist');
  const listToggle = container.querySelector('.player-playlist-toggle');
  const listMore = container.querySelector('.playlist-load-more');
  const fullscreenButton = container.querySelector('.player-fullscreen');
  const pipButtons = [...container.querySelectorAll('.player-pip')];
  const brightnessPanel = container.querySelector('.player-brightness');
  const brightnessRange = container.querySelector('.player-brightness-range');
  const brightnessButtons = [...container.querySelectorAll('[data-player-action="brightness"]')];
  const shade = container.querySelector('.player-brightness-shade');
  const feedback = container.querySelector('.player-gesture-feedback');
  const help = container.querySelector('.player-help');
  const playButtons = [...container.querySelectorAll('.player-play')];
  const skipButtons = [...container.querySelectorAll('.player-skip-back,.player-skip-forward')];
  const controller = new AbortController();
  const portrait = window.matchMedia('(max-width: 760px) and (orientation: portrait)');
  let destroyed = false, idleTimer, scrubbing = false, waiting = false, needsGesture = false;
  let initialLoading = true, explicitlyPaused = false;
  let progressTime = null, frameTime = null, frameRequest = null, progressGeneration = 0;
  let failure = '', revealOnly = false, selecting = false, playRequest = 0;
  let playlistStarted = false, playlistOffset = 0;
  let gestureActive = false, feedbackTimer, brightness = 1, volumeSupported = true;
  let pageFullscreen = false, fullscreenScroll = null, presentationRequest = 0;
  let previewPosition = null, nativeWasFullscreen = false;
  let resumeAfterScrub = false, pendingSeek = null;
  const touch = window.matchMedia('(any-pointer: coarse)');
  let orientationLocked = false;
  let fullscreenPlaylist = null, nativeVideoFullscreen = false;
  let dismissPopoverClick = false;
  const speedMenu = mountSelect(speed, { signal: controller.signal, onChange: value => { media.playbackRate = Number(value); }, onOpenChange: showControls });
  const listen = (target, event, handler, options = {}) => target.addEventListener(event, handler, { ...options, signal: controller.signal });
  const finiteDuration = () => Number.isFinite(media.duration) && media.duration > 0 ? media.duration : 0;
  const nativeFullscreenActive = () => document.fullscreenElement === container || document.webkitFullscreenElement === container;
  const systemVideoFullscreen = () => nativeVideoFullscreen || Boolean(media.webkitDisplayingFullscreen);
  const fullscreenActive = () => pageFullscreen || nativeFullscreenActive() || systemVideoFullscreen();
  const standardPiP = () => typeof media.requestPictureInPicture === 'function' && 'pictureInPictureElement' in document;
  const pipActive = () => standardPiP() ? document.pictureInPictureElement === media : media.webkitPresentationMode === 'picture-in-picture';
  function updateRotation() {
    const full = !destroyed && fullscreenActive();
    const width = Math.round(window.visualViewport?.width ?? window.innerWidth), height = Math.round(window.visualViewport?.height ?? window.innerHeight);
    const rotated = !isAudio && full && !systemVideoFullscreen() && touch.matches && height > width;
    const changed = container.classList.contains('is-rotated') !== rotated;
    if (full) {
      container.style.setProperty('--player-viewport-width', `${width}px`);
      container.style.setProperty('--player-viewport-height', `${height}px`);
    } else {
      container.style.removeProperty('--player-viewport-width');
      container.style.removeProperty('--player-viewport-height');
    }
    container.classList.toggle('is-rotated', rotated);
    if (changed) {
      speedMenu.hide();
      window.dispatchEvent(new Event('playerrotationchange'));
    }
    placeControlPanel(brightnessPanel); placeControlPanel(help);
  }
  function releaseOrientation() {
    if (!orientationLocked) return;
    orientationLocked = false;
    try { screen.orientation?.unlock?.(); } catch { /* Browser owns the physical direction. */ }
  }
  async function requestLandscape(request) {
    if (isAudio || !touch.matches || !screen.orientation?.lock) return;
    try {
      await screen.orientation.lock('landscape');
      if (destroyed || request !== presentationRequest || !fullscreenActive()) {
        screen.orientation.unlock?.();
        return;
      }
      orientationLocked = true;
    } catch { /* Portrait browsers use the rotated player surface instead. */ }
    if (!destroyed && request === presentationRequest) updateRotation();
  }

  function showControls() {
    if (destroyed) return;
    container.classList.remove('controls-hidden');
    clearTimeout(idleTimer);
    if (!media.paused && !media.ended) idleTimer = setTimeout(hideControls, 3000);
  }
  function hideControls() {
    if (destroyed || media.paused || media.ended || scrubbing || gestureActive || media.seeking || initialLoading || waiting || needsGesture || failure || !menu.hidden || speedMenu.open || !help.hidden || brightnessPanel && !brightnessPanel.hidden || !list.hidden && (!portrait.matches || fullscreenActive())) return;
    const focus = document.activeElement;
    if (focus && container.contains(focus) && focus.matches(':focus-visible') && focus.closest('button,input,select,.player-menu')) return;
    container.classList.add('controls-hidden');
  }
  function setStatus(text, state) {
    if (statusText.textContent !== text) statusText.textContent = text;
    if (state === 'error' || state === 'gesture') {
      clearTimeout(feedbackTimer); feedback.hidden = true; previewPosition = null;
      renderTimeline();
    }
    status.hidden = !text || !feedback.hidden;
    status.dataset.state = state;
    const role = state === 'error' ? 'alert' : 'status';
    if (statusText.getAttribute('role') !== role) {
      statusText.setAttribute('role', role);
      statusText.setAttribute('aria-live', state === 'error' ? 'assertive' : 'polite');
    }
    stage.setAttribute('aria-busy', String(state === 'loading' || state === 'buffering'));
  }
  function stopPlaybackProgress() {
    progressGeneration++;
    if (frameRequest !== null) media.cancelVideoFrameCallback?.(frameRequest);
    frameRequest = progressTime = frameTime = null;
  }
  function canWatchPlaybackProgress() {
    return !destroyed && (initialLoading || waiting) && !media.paused && !media.ended && !media.error && !media.seeking && !failure && !needsGesture;
  }
  function recoverPlaybackProgress() {
    initialLoading = waiting = false;
    renderState();
  }
  function watchPlaybackProgress() {
    if (!canWatchPlaybackProgress()) { stopPlaybackProgress(); return; }
    if (progressTime === null) progressTime = media.currentTime;
    if (isAudio || !media.requestVideoFrameCallback || frameRequest !== null) return;
    const generation = progressGeneration;
    frameRequest = media.requestVideoFrameCallback((now, metadata) => {
      if (destroyed || generation !== progressGeneration) return;
      frameRequest = null;
      if (!canWatchPlaybackProgress()) { stopPlaybackProgress(); return; }
      const time = metadata.mediaTime;
      // The first submitted frame may be cached or the result of a seek.
      if (media.readyState >= 2 && Number.isFinite(time) && frameTime !== null && time > frameTime) {
        recoverPlaybackProgress();
        return;
      }
      frameTime = media.readyState >= 2 && Number.isFinite(time) ? time : null;
      watchPlaybackProgress();
    });
  }
  function observePlaybackTime() {
    if (!canWatchPlaybackProgress() || !isAudio && media.requestVideoFrameCallback) return;
    const time = media.currentTime;
    const advanced = progressTime !== null && Number.isFinite(time) && time > progressTime;
    progressTime = time;
    if (advanced && media.readyState >= 3) recoverPlaybackProgress();
  }
  function renderState() {
    if (destroyed) return;
    const paused = !(scrubbing ? resumeAfterScrub : pendingSeek?.resume) && (media.paused || media.ended);
    watchPlaybackProgress();
    const loading = initialLoading;
    for (const control of playButtons) {
      const label = t(paused ? 'play' : 'pause');
      control.setAttribute('aria-label', label);
      control.title = label;
      control.innerHTML = icon(paused ? 'play' : 'pause');
      control.disabled = Boolean(media.error);
    }
    container.classList.toggle('is-paused', paused);
    container.classList.toggle('is-playing', !paused);
    if (failure) setStatus(failure, 'error');
    else if (needsGesture) setStatus(t('tapToPlay'), 'gesture');
    else if (media.ended) setStatus(t('playbackEnded'), 'ended');
    else if (scrubbing) setStatus('', 'seeking');
    else if (pendingSeek) setStatus(t(loading ? 'loadingMedia' : 'buffering'), loading ? 'loading' : 'buffering');
    else if (explicitlyPaused || paused && !loading) setStatus(t('playbackPaused'), 'paused');
    else if (loading) setStatus(t('loadingMedia'), 'loading');
    else if (waiting && !paused) setStatus(t('buffering'), 'buffering');
    else setStatus('', 'playing');
    if (paused || loading || waiting || failure || needsGesture) showControls();
  }
  function renderTimeline() {
    if (destroyed) return;
    const total = finiteDuration();
    const position = previewPosition ?? (Number.isFinite(media.currentTime) ? Math.max(0, media.currentTime) : 0);
    elapsed.textContent = clock(position);
    duration.textContent = clock(total || NaN);
    seek.disabled = !total || Boolean(media.error);
    seek.max = String(total);
    seek.value = String(Math.min(total, position));
    seek.setAttribute('aria-valuetext', `${clock(position)} / ${clock(total || NaN)}`);
    const percent = total ? Math.min(100, position / total * 100) : 0;
    played.style.width = `${percent}%`;
    seek.style.setProperty('--progress', `${percent}%`);
    for (const control of skipButtons) control.disabled = !total || Boolean(media.error);
  }
  function renderBuffered() {
    if (destroyed) return;
    const total = finiteDuration();
    const ranges = document.createDocumentFragment();
    if (total) for (let index = 0; index < media.buffered.length; index++) {
      const start = Math.min(total, Math.max(0, media.buffered.start(index)));
      const end = Math.min(total, media.buffered.end(index));
      if (end <= start) continue;
      const range = document.createElement('span');
      range.style.left = `${start / total * 100}%`;
      range.style.width = `${(end - start) / total * 100}%`;
      ranges.append(range);
    }
    buffered.replaceChildren(ranges);
  }
  function renderVolume() {
    volume.disabled = !volumeSupported;
    volume.title = t(volumeSupported ? 'volume' : 'systemVolume');
    volume.value = String(media.muted ? 0 : media.volume);
    volume.style.setProperty('--volume', `${Number(volume.value) * 100}%`);
    const muted = media.muted || media.volume === 0;
    for (const control of container.querySelectorAll('[data-player-action="mute"]')) {
      const label = t(muted ? 'unmute' : 'mute');
      control.innerHTML = icon(muted ? 'muted' : 'volume') + (control.classList.contains('player-compact-action') ? `<span>${h(label)}</span>` : '');
      control.setAttribute('aria-label', label); control.title = label;
      control.setAttribute('aria-pressed', String(muted));
    }
  }
  function renderPresentation() {
    if (destroyed) return;
    const full = fullscreenActive();
    container.classList.toggle('is-fullscreen', full);
    updateRotation();
    fullscreenButton.hidden = isAudio;
    fullscreenButton.innerHTML = icon(full ? 'exitFullscreen' : 'fullscreen');
    fullscreenButton.setAttribute('aria-label', t(full ? 'exitFullscreen' : 'fullscreen'));
    fullscreenButton.title = t(full ? 'exitFullscreen' : 'fullscreen');
    fullscreenButton.setAttribute('aria-pressed', String(full));
    const back = container.querySelector('.player-back');
    back.setAttribute('aria-label', t(full ? 'exitFullscreen' : 'backToFolder'));
    back.title = t(full ? 'exitFullscreen' : 'backToFolder');
    back.querySelector('span').textContent = t(full ? 'exitFullscreen' : 'backToFolder');
    const supportsWebkitPiP = !isAudio && media.webkitSetPresentationMode && media.webkitSupportsPresentationMode?.('picture-in-picture');
    for (const control of pipButtons) {
      control.hidden = isAudio || media.disablePictureInPicture || !(standardPiP() ? document.pictureInPictureEnabled : supportsWebkitPiP);
      control.disabled = media.readyState === 0 || Boolean(media.error);
      const label = t(pipActive() ? 'exitPictureInPicture' : 'pictureInPicture');
      control.setAttribute('aria-label', label); control.title = label;
      control.setAttribute('aria-pressed', String(pipActive()));
      if (control.classList.contains('player-compact-action')) control.querySelector('span').textContent = label;
    }
    const nativeControl = container.querySelector('[data-player-action="system-fullscreen"]');
    nativeControl.hidden = isAudio || !media.webkitEnterFullscreen || media.webkitSupportsFullscreen === false;
    nativeControl.disabled = media.readyState === 0 || Boolean(media.error);
  }
  function showOperationError(error) {
    if (destroyed) return;
    pendingSeek = null; scrubbing = false;
    failure = t(typeof error?.code === 'string' ? error.code : 'internal');
    renderState();
  }
  async function reportMediaError(error) {
    if (destroyed) return;
    pendingSeek = null; scrubbing = false;
    failure = t('playbackFailed');
    renderState();
    try { await onError?.(error); } catch (callbackError) { showOperationError(callbackError); }
  }
  async function play() {
    const request = ++playRequest;
    needsGesture = false;
    if (!media.error) failure = '';
    try {
      await media.play();
      if (!destroyed && request === playRequest) { renderState(); showControls(); }
    } catch (error) {
      if (destroyed || request !== playRequest || error.name === 'AbortError') return;
      if (error.name === 'NotAllowedError') {
        needsGesture = true;
        renderState();
      } else if (!media.error) await reportMediaError(error);
    }
  }
  function togglePlay() {
    if (scrubbing) { resumeAfterScrub = !resumeAfterScrub; renderState(); return; }
    if (pendingSeek) { pendingSeek.resume = !pendingSeek.resume; renderState(); return; }
    if (media.paused || media.ended) {
      if (media.ended && finiteDuration()) media.currentTime = 0;
      void play();
    } else { playRequest++; media.pause(); }
  }
  function finishSeek() {
    if (!pendingSeek?.settled || scrubbing || media.seeking || media.readyState < 2 || media.error || failure) return;
    const resume = pendingSeek.resume; pendingSeek = null;
    initialLoading = waiting = false; explicitlyPaused = !resume;
    renderState();
    if (resume) void play();
  }
  function seekTo(value, resume = pendingSeek?.resume ?? (!media.paused && !media.ended)) {
    const total = finiteDuration();
    if (!total || media.error || !Number.isFinite(value)) return;
    const target = Math.min(total, Math.max(0, value));
    const unchanged = target === media.currentTime && !media.seeking;
    pendingSeek = { target, resume, settled: unchanged };
    playRequest++; stopPlaybackProgress(); waiting = true;
    if (!media.paused) media.pause();
    if (!unchanged) media.currentTime = target;
    renderTimeline(); renderState(); showControls();
    if (unchanged) finishSeek();
  }
  function beginScrub() {
    if (scrubbing || seek.disabled) return;
    resumeAfterScrub = pendingSeek?.resume ?? (!media.paused && !media.ended);
    hideFeedback(); scrubbing = true;
    previewPosition = media.currentTime;
    playRequest++;
    if (!media.paused) media.pause();
    renderState(); showControls();
  }
  function finishScrub(commit) {
    if (!scrubbing) return;
    const target = previewPosition, resume = resumeAfterScrub;
    scrubbing = false; previewPosition = null;
    if (commit && target !== null) seekTo(target, resume);
    else {
      if (pendingSeek) { pendingSeek.resume = resume; finishSeek(); }
      else if (resume) void play();
      renderTimeline(); renderState(); showControls();
    }
  }
  function toggleMenu(open = menu.hidden, focus = false) {
    if (open) renderPresentation();
    menu.hidden = !open;
    more.setAttribute('aria-expanded', String(open));
    container.classList.toggle('menu-open', open);
    showControls();
    if (open && focus) menu.querySelector('button').focus();
  }
  function revealPlaylist() {
    const items = [];
    if (!playlistStarted) {
      const current = playlist.find(item => item.id === entry.id);
      if (current) items.push(current);
      playlistStarted = true;
    }
    // Append only on explicit requests: native scrolling never replaces or estimates rows.
    while (playlistOffset < playlist.length && items.length < 50) {
      const item = playlist[playlistOffset++];
      if (item.id !== entry.id) items.push(item);
    }
    while (playlistOffset < playlist.length && playlist[playlistOffset].id === entry.id) playlistOffset++;
    const focusMore = document.activeElement === listMore;
    const previous = listMore.previousElementSibling;
    listMore.insertAdjacentHTML('beforebegin', items.map(item => {
      const current = item.id === entry.id;
      return `<article class="playlist-entry${current ? ' is-current' : ''}"${current ? ' aria-current="true"' : ''}><span class="playlist-art" aria-hidden="true">${icon(current ? 'play' : /^audio\//i.test(item.mime || '') ? 'audio' : 'video')}</span><div class="playlist-copy"><strong>${h(item.name)}</strong><small>${h(size(item.size))} · ${h(t(current ? 'playingNow' : item.compatibleReady ? 'compatibleShort' : item.ready ? 'readyShort' : 'notReadyShort'))}</small></div><button type="button" class="playlist-action" data-player-select="${h(item.id)}"${current ? ' disabled' : ''}>${h(t(current ? 'playingNow' : item.ready || item.compatibleReady ? 'play' : 'prepareShort'))}</button></article>`;
    }).join(''));
    listMore.hidden = playlistOffset >= playlist.length;
    if (focusMore) (previous ? previous.nextElementSibling : listMore.parentElement.firstElementChild)?.querySelector('button:not(:disabled)')?.focus({ preventScroll: true });
  }
  function togglePlaylist(open = list.hidden) {
    if (open && !playlistStarted) revealPlaylist();
    list.hidden = !open;
    listToggle.setAttribute('aria-expanded', String(open));
    container.classList.toggle('playlist-open', open);
    showControls();
  }
  function restoreFullscreenView() {
    if (!fullscreenScroll || !destroyed && fullscreenActive()) return;
    const saved = fullscreenScroll; fullscreenScroll = null;
    document.body.style.cssText = saved.bodyStyle;
    document.documentElement.style.overflow = saved.root;
    if (!destroyed && fullscreenPlaylist) {
      togglePlaylist(fullscreenPlaylist.open);
      list.scrollTop = fullscreenPlaylist.scroll;
    }
    fullscreenPlaylist = null;
    window.scrollTo(saved.x, saved.y);
  }
  function setPageFullscreen(active) {
    if (pageFullscreen === active) { if (!active) restoreFullscreenView(); return; }
    if (active && !fullscreenScroll) {
      fullscreenScroll = { x: window.scrollX, y: window.scrollY, bodyStyle: document.body.style.cssText, root: document.documentElement.style.overflow };
      fullscreenPlaylist = { open: !list.hidden, scroll: list.scrollTop };
      Object.assign(document.body.style, { position: 'fixed', top: `${-fullscreenScroll.y}px`, left: `${-fullscreenScroll.x}px`, width: '100%', overflow: 'hidden' });
      document.documentElement.style.overflow = 'hidden';
    }
    pageFullscreen = active;
    container.classList.toggle('is-fullscreen', active || nativeFullscreenActive() || systemVideoFullscreen());
    if (!active) releaseOrientation();
    updateRotation();
    if (!active) restoreFullscreenView();
  }
  async function exitFullscreen() {
    presentationRequest++;
    setPageFullscreen(false);
    if (systemVideoFullscreen()) media.webkitExitFullscreen?.();
    if (nativeFullscreenActive()) {
      if (document.exitFullscreen) await document.exitFullscreen();
      else await document.webkitExitFullscreen?.();
    }
    restoreFullscreenView();
    if (!destroyed) { renderPresentation(); showControls(); }
  }
  async function toggleFullscreen() {
    if (fullscreenActive()) { await exitFullscreen(); return; }
    const request = ++presentationRequest;
    setPageFullscreen(true);
    togglePlaylist(false); toggleMenu(false); toggleBrightness(false); help.hidden = true; container.classList.remove('help-open'); speedMenu.hide();
    renderPresentation(); showControls();
    try {
      if (document.fullscreenEnabled && container.requestFullscreen) await container.requestFullscreen();
      else if (container.webkitRequestFullscreen) await container.webkitRequestFullscreen();
    } catch { /* Page fullscreen keeps custom controls when the browser denies container fullscreen. */ }
    if (destroyed || request !== presentationRequest) {
      if (nativeFullscreenActive()) {
        if (document.exitFullscreen) await document.exitFullscreen().catch(() => {});
        else document.webkitExitFullscreen?.();
      }
      return;
    }
    nativeWasFullscreen = nativeFullscreenActive();
    void requestLandscape(request);
    renderPresentation();
  }
  function enterSystemFullscreen() {
    if (isAudio || !media.webkitEnterFullscreen || media.readyState === 0 || media.error) return;
    toggleMenu(false); speedMenu.hide();
    media.webkitEnterFullscreen();
  }
  function setBrightness(value) {
    brightness = Math.min(1, Math.max(0.2, value));
    shade.style.opacity = String(1 - brightness);
    if (brightnessRange) {
      brightnessRange.value = String(brightness);
      const label = `${Math.round(brightness * 100)}%`;
      brightnessRange.setAttribute('aria-valuetext', label);
      brightnessPanel.querySelector('output').textContent = label;
    }
  }
  function setVolume(value) {
    const next = Math.max(0, Math.min(1, value));
    try { media.volume = next; } catch { volumeSupported = false; }
    if (Math.abs(media.volume - next) > 0.01) volumeSupported = false;
    if (volumeSupported) media.muted = next === 0;
    renderVolume();
    return volumeSupported;
  }
  function hideFeedback() {
    clearTimeout(feedbackTimer);
    previewPosition = null; feedback.hidden = true;
    renderTimeline();
    setStatus(statusText.textContent, status.dataset.state);
  }
  function showFeedback(data) {
    if (destroyed) return;
    clearTimeout(feedbackTimer);
    if (!data) { hideFeedback(); return; }
    if (failure || media.error || needsGesture) return;
    const kind = data.kind;
    const seekPreview = kind === 'seek';
    previewPosition = seekPreview && !data.committed ? data.value : null;
    feedback.querySelector('.player-gesture-icon').innerHTML = icon(seekPreview ? data.delta < 0 ? 'backward' : 'forward' : kind === 'brightness' ? 'brightness' : 'volume');
    feedback.querySelector('strong').textContent = seekPreview ? clock(data.value) : kind === 'system-volume' ? t('systemVolume') : `${t(kind === 'brightness' ? 'pictureBrightness' : 'volume')} ${Math.round(data.value * 100)}%`;
    feedback.querySelector('small').textContent = seekPreview ? `${data.delta < 0 ? '−' : '+'}${clock(Math.abs(data.delta))}${data.committed ? '' : ` · ${t('releaseToSeek')}`}` : kind === 'brightness' ? t('pictureBrightnessNote') : '';
    feedback.dataset.kind = kind; feedback.hidden = false;
    renderTimeline();
    setStatus(statusText.textContent, status.dataset.state);
    if (!gestureActive) feedbackTimer = setTimeout(hideFeedback, 1200);
  }
  function placeControlPanel(panel) {
    if (!panel || panel.hidden) return;
    const viewport = window.visualViewport;
    const dock = container.querySelector('.player-controlbar').getBoundingClientRect();
    const rotated = container.classList.contains('is-rotated');
    const surface = container.querySelector('.player-surface').getBoundingClientRect();
    const top = rotated ? surface.right - dock.right : dock.top - (viewport?.offsetTop ?? 0);
    const bottom = rotated ? surface.right - dock.left : dock.bottom - (viewport?.offsetTop ?? 0);
    const height = rotated ? viewport?.width ?? window.innerWidth : viewport?.height ?? window.innerHeight;
    const above = Math.max(0, top - 18), below = Math.max(0, height - bottom - 18);
    const openAbove = above >= Math.min(300, panel.scrollHeight + 2) || above >= below;
    panel.style.maxHeight = `${Math.max(0, Math.min(300, openAbove ? above : below))}px`;
    panel.style.top = openAbove ? 'auto' : 'calc(100% + 10px)';
    panel.style.bottom = openAbove ? 'calc(100% + 10px)' : 'auto';
    panel.dataset.placement = openAbove ? 'above' : 'below';
  }
  function toggleBrightness(open = brightnessPanel?.hidden) {
    if (!brightnessPanel) return;
    brightnessPanel.hidden = !open;
    container.classList.toggle('brightness-open', Boolean(open));
    for (const control of brightnessButtons) control.setAttribute('aria-expanded', String(Boolean(open)));
    if (open) { help.hidden = true; container.classList.remove('help-open'); }
    placeControlPanel(brightnessPanel);
    showControls();
  }
  const gestures = isAudio ? null : mountPlayerGestures({
    stage, media, signal: controller.signal,
    getBrightness: () => brightness, onBrightness: setBrightness, onVolume: setVolume,
    onPreview: showFeedback,
    onCommitSeek: value => { previewPosition = null; seekTo(value); feedback.querySelector('small').textContent = ''; },
    onActiveChange: active => {
      if (destroyed) return;
      gestureActive = active;
      container.classList.toggle('gesture-active', active);
      showControls();
      if (!active) { previewPosition = null; renderTimeline(); clearTimeout(feedbackTimer); feedbackTimer = setTimeout(hideFeedback, 1000); }
    },
    onTap: ({ x, double }) => {
      if (destroyed) return;
      if (double) {
        if (x < 1 / 3 || x > 2 / 3) {
          const delta = x < 1 / 3 ? -10 : 10;
          seekTo(media.currentTime + delta);
          showFeedback({ kind: 'seek', value: media.currentTime, delta, committed: true });
        } else togglePlay();
      } else if (container.classList.contains('controls-hidden') || media.paused || media.ended || failure || initialLoading) showControls();
      else { clearTimeout(idleTimer); container.classList.add('controls-hidden'); }
    }
  });
  async function togglePiP() {
    toggleMenu(false); speedMenu.hide();
    if (standardPiP()) {
      if (pipActive()) await document.exitPictureInPicture();
      else if (document.pictureInPictureEnabled) await media.requestPictureInPicture();
    } else if (media.webkitSetPresentationMode && media.webkitSupportsPresentationMode?.('picture-in-picture')) {
      media.webkitSetPresentationMode(pipActive() ? 'inline' : 'picture-in-picture');
    }
    if (pipActive()) { presentationRequest++; setPageFullscreen(false); }
    renderPresentation();
  }
  async function activate(action) {
    showControls();
    switch (action) {
      case 'back': if (fullscreenActive()) await exitFullscreen(); else await onBack?.(); break;
      case 'play': togglePlay(); break;
      case 'backward': seekTo(media.currentTime - 10); break;
      case 'forward': seekTo(media.currentTime + 10); break;
      case 'mute':
        if (media.volume === 0) media.volume = 1;
        media.muted = !media.muted && Number(volume.value) !== 0;
        renderVolume();
        break;
      case 'brightness': toggleMenu(false); toggleBrightness(); if (brightnessPanel && !brightnessPanel.hidden) brightnessRange.focus({ preventScroll: true }); break;
      case 'gestures': toggleMenu(false); toggleBrightness(false); help.hidden = !help.hidden; container.classList.toggle('help-open', !help.hidden); placeControlPanel(help); more.focus(); break;
      case 'menu': toggleMenu(undefined, true); break;
      case 'playlist': togglePlaylist(); break;
      case 'playlist-more': revealPlaylist(); break;
      case 'details': toggleMenu(false); more.focus(); await onDetails?.(); break;
      case 'download': toggleMenu(false); more.focus(); await onDownload?.(); break;
      case 'optimize-startup': toggleMenu(false); more.focus(); await onOptimizeStartup?.(); break;
      case 'fullscreen': await toggleFullscreen(); break;
      case 'system-fullscreen': enterSystemFullscreen(); break;
      case 'pip': await togglePiP(); break;
    }
  }

  listen(container, 'click', event => {
    const select = event.target.closest('[data-player-select]');
    const action = event.target.closest('[data-player-action]');
    if (!select && !action) return;
    event.stopPropagation();
    if (select) {
      if (selecting || select.disabled) return;
      selecting = true;
      select.disabled = true;
      list.setAttribute('aria-busy', 'true');
      media.pause();
      Promise.resolve().then(() => onSelect?.(select.dataset.playerSelect)).catch(showOperationError).finally(() => {
        if (destroyed) return;
        selecting = false;
        select.disabled = false;
        list.removeAttribute('aria-busy');
      });
    } else void activate(action.dataset.playerAction).catch(showOperationError);
  });
  listen(stage, 'pointerdown', event => {
    if (event.target.closest('button,input,select') || event.pointerType === 'touch' || event.pointerType === 'pen') return;
    revealOnly = container.classList.contains('controls-hidden');
    showControls();
  });
  listen(stage, 'click', event => {
    if (event.target.closest('button,input,select') || event.pointerType === 'touch' || event.pointerType === 'pen') return;
    if (revealOnly) { revealOnly = false; return; }
    togglePlay();
  });
  const pointerActivity = event => { if ((event.pointerType === 'touch' || event.pointerType === 'pen') && stage.contains(event.target) && !event.target.closest('button,input,select')) return; showControls(); };
  listen(container, 'pointermove', pointerActivity);
  listen(container, 'pointerdown', pointerActivity);
  listen(container, 'focusin', showControls);
  listen(container, 'focusout', () => { if (!destroyed) showControls(); });
  listen(document, 'pointerdown', event => {
    dismissPopoverClick = false;
    const target = event.target;
    let closed = false;
    if (!menu.hidden && !menu.contains(target) && !more.contains(target)) { toggleMenu(false); closed = true; }
    if (brightnessPanel && !brightnessPanel.hidden && !brightnessPanel.contains(target) && !target.closest('[data-player-action="brightness"]')) { toggleBrightness(false); closed = true; }
    if (!help.hidden && !help.contains(target) && !target.closest('[data-player-action="gestures"]')) { help.hidden = true; container.classList.remove('help-open'); closed = true; }
    if (speedMenu.open && !speed.contains(target)) { speedMenu.hide(); closed = true; }
    if (closed) showControls();
    if (closed && !target.closest('button,input,select,textarea,a,label,[contenteditable],[role="button"],[role="slider"]')) {
      dismissPopoverClick = true;
      event.preventDefault(); event.stopPropagation();
    }
  }, { capture: true });
  listen(document, 'click', event => {
    if (!dismissPopoverClick) return;
    dismissPopoverClick = false;
    event.preventDefault(); event.stopPropagation();
  }, { capture: true });
  listen(seek, 'pointerdown', beginScrub);
  listen(document, 'pointerup', () => finishScrub(true));
  listen(document, 'pointercancel', () => finishScrub(false));
  listen(seek, 'input', () => {
    const value = Number(seek.value); beginScrub();
    if (!scrubbing) return;
    previewPosition = Math.min(finiteDuration(), Math.max(0, value)); renderTimeline();
  });
  listen(seek, 'change', () => finishScrub(true));
  listen(volume, 'input', () => { if (!setVolume(Number(volume.value))) showFeedback({ kind: 'system-volume' }); });
  listen(media, 'volumechange', renderVolume);
  if (brightnessRange) listen(brightnessRange, 'input', () => { setBrightness(Number(brightnessRange.value)); showControls(); });
  listen(media, 'ratechange', () => { speedMenu.setValue(media.playbackRate); });
  listen(media, 'timeupdate', () => { renderTimeline(); observePlaybackTime(); });
  listen(media, 'progress', renderBuffered);
  for (const event of ['loadedmetadata', 'durationchange']) listen(media, event, () => {
    renderTimeline(); renderBuffered(); renderState(); renderPresentation();
  });
  for (const event of ['loadeddata', 'canplay']) listen(media, event, () => {
    if (media.readyState >= 2 && !media.seeking) initialLoading = waiting = false;
    renderTimeline(); renderBuffered(); renderState(); renderPresentation();
    finishSeek();
  });
  for (const event of ['loadstart', 'emptied']) listen(media, event, () => { pendingSeek = null; scrubbing = false; previewPosition = null; stopPlaybackProgress(); initialLoading = true; waiting = true; renderState(); });
  for (const event of ['waiting', 'stalled']) listen(media, event, () => { stopPlaybackProgress(); waiting = true; renderState(); });
  listen(media, 'seeking', () => { stopPlaybackProgress(); waiting = true; renderState(); showControls(); });
  listen(media, 'seeked', () => {
    stopPlaybackProgress();
    if (pendingSeek && !media.seeking && Math.abs(media.currentTime - pendingSeek.target) <= 0.25) pendingSeek.settled = true;
    finishSeek(); renderTimeline(); renderState(); showControls();
  });
  listen(media, 'play', () => { explicitlyPaused = false; needsGesture = false; renderState(); showControls(); });
  listen(media, 'playing', () => { initialLoading = false; explicitlyPaused = false; waiting = false; needsGesture = false; renderState(); showControls(); });
  listen(media, 'pause', () => { if (!scrubbing && !pendingSeek) explicitlyPaused = true; renderState(); showControls(); });
  listen(media, 'ended', () => { renderState(); showControls(); });
  listen(media, 'error', () => { if (media.error) { renderTimeline(); renderPresentation(); void reportMediaError(media.error); } });
  for (const event of ['fullscreenchange', 'webkitfullscreenchange']) listen(document, event, () => {
    const native = nativeFullscreenActive();
    if (nativeWasFullscreen && !native) { setPageFullscreen(false); restoreFullscreenView(); }
    nativeWasFullscreen = native;
    renderPresentation(); showControls();
  });
  listen(media, 'webkitbeginfullscreen', () => { nativeVideoFullscreen = true; presentationRequest++; releaseOrientation(); renderPresentation(); });
  listen(media, 'webkitendfullscreen', () => { nativeVideoFullscreen = false; setPageFullscreen(false); restoreFullscreenView(); renderPresentation(); showControls(); });
  for (const event of ['enterpictureinpicture', 'leavepictureinpicture', 'webkitpresentationmodechanged']) listen(media, event, () => {
    if (pipActive()) { presentationRequest++; setPageFullscreen(false); }
    renderPresentation();
  });
  listen(window, 'focus', renderPresentation);
  listen(document, 'visibilitychange', () => { if (!document.hidden) renderPresentation(); });
  listen(portrait, 'change', () => { if (!fullscreenActive()) togglePlaylist(portrait.matches); });
  listen(window, 'resize', () => { updateRotation(); if (fullscreenActive()) showControls(); });
  listen(window, 'orientationchange', updateRotation);
  if (window.visualViewport) listen(window.visualViewport, 'resize', updateRotation);
  if (screen.orientation?.addEventListener) listen(screen.orientation, 'change', updateRotation);
  listen(touch, 'change', updateRotation);
  listen(document, 'keydown', event => {
    if (event.defaultPrevented || event.altKey || event.ctrlKey || event.metaKey || document.querySelector('dialog[open]')) return;
    if (event.key === 'Escape') {
      if (!menu.hidden) { event.preventDefault(); toggleMenu(false); more.focus(); }
      else if (!list.hidden && !fullscreenActive()) { event.preventDefault(); togglePlaylist(false); listToggle.focus(); }
      else if (brightnessPanel && !brightnessPanel.hidden) { event.preventDefault(); toggleBrightness(false); const direct = container.querySelector('.player-brightness-toggle'); (direct?.getBoundingClientRect().width ? direct : more).focus(); }
      else if (!help.hidden) { event.preventDefault(); help.hidden = true; container.classList.remove('help-open'); more.focus(); }
      else if (fullscreenActive()) { event.preventDefault(); void exitFullscreen().catch(showOperationError); }
      return;
    }
    if (event.target.closest('input,select,textarea,[contenteditable="true"],[role="textbox"],[data-select-menu]')) return;
    if (event.key === ' ' && event.target.closest('button,a')) return;
    const key = event.key.toLowerCase();
    const action = { ' ': 'play', k: 'play', arrowleft: 'backward', arrowright: 'forward', m: 'mute', f: 'fullscreen' }[key];
    if (action) {
      if (action === 'fullscreen' && fullscreenButton.hidden) return;
      event.preventDefault();
      void activate(action).catch(showOperationError);
    } else if (key === 'arrowup' || key === 'arrowdown') {
      event.preventDefault();
      if (!setVolume(media.volume + (key === 'arrowup' ? 0.05 : -0.05))) showFeedback({ kind: 'system-volume' });
      showControls();
    }
  });
  listen(menu, 'keydown', event => {
    const items = [...menu.querySelectorAll('button:not([hidden]):not(:disabled)')].filter(item => getComputedStyle(item).display !== 'none');
    const index = items.indexOf(document.activeElement);
    if (['ArrowDown', 'ArrowUp', 'Home', 'End'].includes(event.key)) {
      event.preventDefault(); event.stopPropagation();
      const next = event.key === 'Home' ? 0 : event.key === 'End' ? items.length - 1 : (index + (event.key === 'ArrowDown' ? 1 : -1) + items.length) % items.length;
      items[next].focus();
    } else if (event.key === 'Tab') toggleMenu(false);
  });
  container.querySelector('.player-controlbar').title = t('playerShortcuts');
  togglePlaylist(portrait.matches);
  setBrightness(1);
  renderTimeline(); renderVolume(); renderPresentation(); renderState();
  media.src = source;
  void play();

  return {
    get fullscreen() { return fullscreenActive(); },
    exitFullscreen,
    destroy() {
      if (destroyed) return;
      destroyed = true;
      playRequest++;
      clearTimeout(idleTimer);
      clearTimeout(feedbackTimer);
      presentationRequest++;
      gestures?.destroy();
      setPageFullscreen(false);
      releaseOrientation();
      stopPlaybackProgress();
      controller.abort();
      if (pipActive()) {
        if (standardPiP()) void document.exitPictureInPicture().catch(() => {});
        else media.webkitSetPresentationMode?.('inline');
      }
      if (media.webkitDisplayingFullscreen) media.webkitExitFullscreen();
      if (document.fullscreenElement === container) void document.exitFullscreen().catch(() => {});
      else if (document.webkitFullscreenElement === container) document.webkitExitFullscreen?.();
      media.pause();
      media.removeAttribute('src');
      media.load();
      container.replaceChildren();
    }
  };
}
