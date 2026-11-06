import { translate } from './locales.js';

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
  close: '<path d="m6 6 12 12M18 6 6 18"/>'
};
const icon = name => `<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">${paths[name]}</svg>`;
const clock = seconds => {
  if (!Number.isFinite(seconds) || seconds < 0) return '--:--';
  const value = Math.floor(seconds), hours = Math.floor(value / 3600);
  return `${hours ? `${hours}:` : ''}${String(Math.floor(value / 60) % 60).padStart(2, '0')}:${String(value % 60).padStart(2, '0')}`;
};

export function mountPlayer({ container, entry, source, variant = 'original', language = 'en', connected = true, playlist = [], onBack, onSelect, onDetails, onDownload, onError }) {
  const t = key => translate(language, key);
  const h = escapeHTML;
  const size = value => {
    if (!Number.isFinite(value) || value < 0) return t('unknown');
    const units = ['B', 'KB', 'MB', 'GB', 'TB'];
    const order = value > 0 ? Math.min(4, Math.floor(Math.log(value) / Math.log(1024))) : 0;
    return `${new Intl.NumberFormat(language, { maximumFractionDigits: order ? 1 : 0 }).format(value / 1024 ** order)} ${units[order]}`;
  };
  const isAudio = /^audio\//i.test(entry.mime || '') || (!/^video\//i.test(entry.mime || '') && /\.(mp3|m4a|aac|flac|ogg|opus|wav)$/i.test(entry.name || ''));
  const badge = t(variant === 'compatible' ? 'compatibleShort' : 'original');
  const button = (action, label, image, className = '', extra = '') => `<button type="button" class="icon-button ${className}" data-player-action="${action}" aria-label="${h(t(label))}" title="${h(t(label))}" ${extra}>${icon(image)}</button>`;
  const transport = () => `${button('backward', 'skipBack', 'backward', 'player-skip-back')}${button('play', 'play', 'play', 'player-play')}${button('forward', 'skipForward', 'forward', 'player-skip-forward')}`;
  const mediaTag = isAudio ? 'audio' : 'video';
  container.className = `player-page${isAudio ? ' is-audio' : ''}`;
  container.tabIndex = -1;
  container.setAttribute('aria-label', entry.name);
  container.innerHTML = `
    <header class="player-header">
      <button type="button" class="player-back" data-player-action="back" aria-label="${h(t('backToFolder'))}">${icon('back')}<span>${h(t('backToFolder'))}</span></button>
      <div class="player-heading"><strong title="${h(entry.name)}">${h(entry.name)}</strong><span class="variant-badge">${h(badge)}</span></div>
      <div class="player-header-actions">
        <span data-connection><span class="connection${connected ? '' : ' offline'}" role="status">${h(t(connected ? 'connected' : 'offline'))}</span></span>
        <button type="button" class="player-playlist-toggle" data-player-action="playlist" aria-label="${h(t('sameFolder'))} (${playlist.length})" aria-expanded="false" aria-controls="player-playlist">${icon('playlist')}<span>${h(t('sameFolder'))} (${playlist.length})</span></button>
        ${button('menu', 'more', 'more', 'player-more', 'aria-haspopup="menu" aria-expanded="false" aria-controls="player-menu"')}
        <div id="player-menu" class="player-menu" role="menu" hidden>
          <button type="button" role="menuitem" data-player-action="details">${h(t('playbackInfo'))}</button>
          <button type="button" role="menuitem" data-player-action="download">${h(t('download'))}</button>
        </div>
      </div>
    </header>
    <div class="player-layout">
      <div class="player-main">
        <div class="player-stage">
          <${mediaTag} class="player-media" playsinline preload="metadata" aria-label="${h(entry.name)}"></${mediaTag}>
          ${isAudio ? `<div class="audio-art" aria-hidden="true">${icon('audio')}</div>` : ''}
          <div class="player-central">${transport()}</div>
          <div class="player-status" role="status" aria-live="polite" data-state="loading">${h(t('loadingMedia'))}</div>
        </div>
        <div class="player-caption"><strong>${h(entry.name)}</strong><small>${h(size(entry.size))}</small><span class="variant-badge">${h(badge)}</span></div>
        <div class="player-controlbar">
          <div class="player-timeline">
            <div class="player-time"><span class="player-elapsed">00:00</span><span aria-hidden="true"> / </span><span class="player-duration">--:--</span><span class="player-remaining">${h(t('remaining'))} --:--</span></div>
            <div class="seek-track"><div class="seek-buffer" aria-hidden="true"></div><span class="seek-played" aria-hidden="true"></span><input class="player-seek" type="range" min="0" max="0" value="0" step="0.1" aria-label="${h(t('seek'))}" disabled></div>
          </div>
          <div class="player-bottom">
            <div class="player-transport">${transport()}<div class="player-volume">${button('mute', 'mute', 'volume', 'player-mute')}<input class="player-volume-range" type="range" min="0" max="1" step="0.05" value="1" aria-label="${h(t('volume'))}"></div></div>
            <div class="player-options">
              <label class="player-speed"><span class="sr-only">${h(t('speed'))}</span><select aria-label="${h(t('speed'))}">${[0.5, 0.75, 1, 1.25, 1.5, 1.75, 2].map(rate => `<option value="${rate}"${rate === 1 ? ' selected' : ''}>${rate === 1 ? '1.0' : rate}×</option>`).join('')}</select></label>
              ${button('pip', 'pictureInPicture', 'pip', 'player-pip', 'hidden')}
              ${button('fullscreen', 'fullscreen', 'fullscreen', 'player-fullscreen', 'hidden')}
            </div>
          </div>
        </div>
      </div>
      <aside id="player-playlist" class="player-playlist" aria-label="${h(t('sameFolder'))}" hidden>
        <div class="playlist-heading"><h2>${h(t('sameFolder'))} <span>${playlist.length}</span></h2>${button('playlist', 'close', 'close')}</div>
        <div class="playlist-list">${playlist.map(item => {
          const current = item.id === entry.id;
          return `<article class="playlist-entry${current ? ' is-current' : ''}"${current ? ' aria-current="true"' : ''}><span class="playlist-art" aria-hidden="true">${icon(current ? 'play' : /^audio\//i.test(item.mime || '') ? 'audio' : 'video')}</span><div class="playlist-copy"><strong>${h(item.name)}</strong><small>${h(size(item.size))} · ${h(t(current ? 'playingNow' : item.compatibleReady ? 'compatibleShort' : item.ready ? 'readyShort' : 'notReadyShort'))}</small></div><button type="button" class="playlist-action" data-player-select="${h(item.id)}"${current ? ' disabled' : ''}>${h(t(current ? 'playingNow' : item.ready || item.compatibleReady ? 'play' : 'prepareShort'))}</button></article>`;
        }).join('')}</div>
      </aside>
    </div>`;

  const media = container.querySelector('.player-media');
  const stage = container.querySelector('.player-stage');
  const status = container.querySelector('.player-status');
  const seek = container.querySelector('.player-seek');
  const played = container.querySelector('.seek-played');
  const buffered = container.querySelector('.seek-buffer');
  const elapsed = container.querySelector('.player-elapsed');
  const duration = container.querySelector('.player-duration');
  const remaining = container.querySelector('.player-remaining');
  const volume = container.querySelector('.player-volume-range');
  const speed = container.querySelector('.player-speed select');
  const menu = container.querySelector('.player-menu');
  const more = container.querySelector('.player-more');
  const list = container.querySelector('.player-playlist');
  const listToggle = container.querySelector('.player-playlist-toggle');
  const fullscreenButton = container.querySelector('.player-fullscreen');
  const pipButton = container.querySelector('.player-pip');
  const playButtons = [...container.querySelectorAll('.player-play')];
  const skipButtons = [...container.querySelectorAll('.player-skip-back,.player-skip-forward')];
  const controller = new AbortController();
  const portrait = window.matchMedia('(max-width: 760px) and (orientation: portrait)');
  let destroyed = false, idleTimer, scrubbing = false, waiting = false, needsGesture = false;
  let failure = '', revealOnly = false, selecting = false, playRequest = 0;
  const listen = (target, event, handler, options = {}) => target.addEventListener(event, handler, { ...options, signal: controller.signal });
  const finiteDuration = () => Number.isFinite(media.duration) && media.duration > 0 ? media.duration : 0;
  const fullscreenActive = () => document.fullscreenElement === container || document.webkitFullscreenElement === container || Boolean(media.webkitDisplayingFullscreen);
  const pipActive = () => document.pictureInPictureElement === media || media.webkitPresentationMode === 'picture-in-picture';

  function showControls() {
    if (destroyed) return;
    container.classList.remove('controls-hidden');
    clearTimeout(idleTimer);
    if (!media.paused && !media.ended) idleTimer = setTimeout(hideControls, 3000);
  }
  function hideControls() {
    if (destroyed || media.paused || media.ended || scrubbing || media.seeking || waiting || needsGesture || failure || !menu.hidden || !list.hidden && !portrait.matches) return;
    const focus = document.activeElement;
    if (focus && container.contains(focus) && focus.closest('button,input,select,.player-menu')) return;
    container.classList.add('controls-hidden');
  }
  function setStatus(text, state) {
    status.textContent = text;
    status.hidden = !text;
    status.dataset.state = state;
    status.setAttribute('role', state === 'error' ? 'alert' : 'status');
  }
  function renderState() {
    if (destroyed) return;
    const paused = media.paused || media.ended;
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
    else if (waiting && !paused) setStatus(t('buffering'), 'buffering');
    else if (media.readyState === 0) setStatus(t('loadingMedia'), 'loading');
    else if (paused) setStatus(t('playbackPaused'), 'paused');
    else setStatus('', 'playing');
    if (paused || waiting || failure || needsGesture) showControls();
  }
  function renderTimeline() {
    if (destroyed) return;
    const total = finiteDuration();
    const position = Number.isFinite(media.currentTime) ? Math.max(0, media.currentTime) : 0;
    elapsed.textContent = clock(position);
    duration.textContent = clock(total || NaN);
    remaining.textContent = `${t('remaining')} ${clock(total ? Math.max(0, total - position) : NaN)}`;
    seek.disabled = !total || Boolean(media.error);
    seek.max = String(total);
    if (!scrubbing) seek.value = String(Math.min(total, position));
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
    volume.value = String(media.muted ? 0 : media.volume);
    volume.style.setProperty('--volume', `${Number(volume.value) * 100}%`);
    const control = container.querySelector('.player-mute');
    const muted = media.muted || media.volume === 0;
    control.innerHTML = icon(muted ? 'muted' : 'volume');
    control.setAttribute('aria-label', t(muted ? 'unmute' : 'mute'));
    control.title = t(muted ? 'unmute' : 'mute');
    control.setAttribute('aria-pressed', String(muted));
  }
  function renderPresentation() {
    if (destroyed) return;
    const full = fullscreenActive();
    container.classList.toggle('is-fullscreen', full);
    fullscreenButton.hidden = !(document.fullscreenEnabled && container.requestFullscreen || container.webkitRequestFullscreen || !isAudio && media.webkitEnterFullscreen && media.webkitSupportsFullscreen !== false);
    fullscreenButton.innerHTML = icon(full ? 'exitFullscreen' : 'fullscreen');
    fullscreenButton.setAttribute('aria-label', t(full ? 'exitFullscreen' : 'fullscreen'));
    fullscreenButton.title = t(full ? 'exitFullscreen' : 'fullscreen');
    fullscreenButton.setAttribute('aria-pressed', String(full));
    const supportsWebkitPiP = !isAudio && media.webkitSupportsPresentationMode?.('picture-in-picture');
    pipButton.hidden = isAudio || !(document.pictureInPictureEnabled && media.requestPictureInPicture || supportsWebkitPiP);
    pipButton.disabled = media.readyState === 0 || Boolean(media.error);
    pipButton.setAttribute('aria-label', t(pipActive() ? 'exitPictureInPicture' : 'pictureInPicture'));
    pipButton.title = t(pipActive() ? 'exitPictureInPicture' : 'pictureInPicture');
    pipButton.setAttribute('aria-pressed', String(pipActive()));
  }
  function showOperationError(error) {
    if (destroyed) return;
    failure = t(typeof error?.code === 'string' ? error.code : 'internal');
    renderState();
  }
  async function reportMediaError(error) {
    if (destroyed) return;
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
    if (media.paused || media.ended) {
      if (media.ended && finiteDuration()) media.currentTime = 0;
      void play();
    } else { playRequest++; media.pause(); }
  }
  function seekTo(value) {
    const total = finiteDuration();
    if (!total || media.error || !Number.isFinite(value)) return;
    media.currentTime = Math.min(total, Math.max(0, value));
    renderTimeline();
    showControls();
  }
  function toggleMenu(open = menu.hidden, focus = false) {
    menu.hidden = !open;
    more.setAttribute('aria-expanded', String(open));
    container.classList.toggle('menu-open', open);
    showControls();
    if (open && focus) menu.querySelector('button').focus();
  }
  function togglePlaylist(open = list.hidden) {
    list.hidden = !open;
    listToggle.setAttribute('aria-expanded', String(open));
    container.classList.toggle('playlist-open', open);
    showControls();
  }
  async function toggleFullscreen() {
    if (fullscreenActive()) {
      if (media.webkitDisplayingFullscreen) media.webkitExitFullscreen();
      else if (document.exitFullscreen) await document.exitFullscreen();
      else document.webkitExitFullscreen?.();
    } else if (document.fullscreenEnabled && container.requestFullscreen) await container.requestFullscreen();
    else if (container.webkitRequestFullscreen) await container.webkitRequestFullscreen();
    else if (!isAudio && media.webkitEnterFullscreen) media.webkitEnterFullscreen();
    renderPresentation();
  }
  async function togglePiP() {
    if (document.pictureInPictureElement === media) await document.exitPictureInPicture();
    else if (media.webkitPresentationMode === 'picture-in-picture') media.webkitSetPresentationMode('inline');
    else if (document.pictureInPictureEnabled && media.requestPictureInPicture) await media.requestPictureInPicture();
    else if (media.webkitSupportsPresentationMode?.('picture-in-picture')) media.webkitSetPresentationMode('picture-in-picture');
    renderPresentation();
  }
  async function activate(action) {
    showControls();
    switch (action) {
      case 'back': await onBack?.(); break;
      case 'play': togglePlay(); break;
      case 'backward': seekTo(media.currentTime - 10); break;
      case 'forward': seekTo(media.currentTime + 10); break;
      case 'mute':
        if (media.volume === 0) media.volume = 1;
        media.muted = !media.muted && Number(volume.value) !== 0;
        renderVolume();
        break;
      case 'menu': toggleMenu(undefined, true); break;
      case 'playlist': togglePlaylist(); break;
      case 'details': toggleMenu(false); more.focus(); await onDetails?.(); break;
      case 'download': toggleMenu(false); more.focus(); await onDownload?.(); break;
      case 'fullscreen': await toggleFullscreen(); break;
      case 'pip': await togglePiP(); break;
    }
  }

  listen(container, 'click', event => {
    const select = event.target.closest('[data-player-select]');
    const action = event.target.closest('[data-player-action]');
    if (!select && !action) return;
    event.stopPropagation();
    if (select) {
      if (selecting) return;
      selecting = true;
      const controls = [...list.querySelectorAll('[data-player-select]:not(:disabled)')];
      controls.forEach(control => { control.disabled = true; });
      media.pause();
      Promise.resolve().then(() => onSelect?.(select.dataset.playerSelect)).catch(showOperationError).finally(() => {
        if (destroyed) return;
        selecting = false;
        controls.forEach(control => { control.disabled = false; });
      });
    } else void activate(action.dataset.playerAction).catch(showOperationError);
  });
  listen(stage, 'pointerdown', event => {
    if (event.target.closest('button')) return;
    revealOnly = container.classList.contains('controls-hidden');
    showControls();
  });
  listen(stage, 'click', event => {
    if (event.target.closest('button')) return;
    if (revealOnly) { revealOnly = false; return; }
    togglePlay();
  });
  listen(container, 'pointermove', showControls);
  listen(container, 'pointerdown', showControls);
  listen(container, 'focusin', showControls);
  listen(container, 'focusout', () => { if (!destroyed) showControls(); });
  listen(document, 'pointerdown', event => { if (!menu.hidden && !more.contains(event.target) && !menu.contains(event.target)) toggleMenu(false); });
  listen(seek, 'pointerdown', () => { scrubbing = true; showControls(); });
  listen(document, 'pointerup', () => { if (scrubbing) { scrubbing = false; renderTimeline(); showControls(); } });
  listen(document, 'pointercancel', () => { scrubbing = false; renderTimeline(); showControls(); });
  listen(seek, 'input', () => seekTo(Number(seek.value)));
  listen(seek, 'change', () => { scrubbing = false; renderTimeline(); showControls(); });
  listen(volume, 'input', () => { media.volume = Number(volume.value); media.muted = media.volume === 0; renderVolume(); });
  listen(speed, 'change', () => { media.playbackRate = Number(speed.value); });
  listen(media, 'volumechange', renderVolume);
  listen(media, 'ratechange', () => { speed.value = String(media.playbackRate); });
  listen(media, 'timeupdate', renderTimeline);
  listen(media, 'progress', renderBuffered);
  for (const event of ['loadedmetadata', 'durationchange', 'loadeddata', 'canplay']) listen(media, event, () => {
    waiting = false;
    renderTimeline(); renderBuffered(); renderState(); renderPresentation();
  });
  listen(media, 'loadstart', () => { waiting = true; renderState(); });
  for (const event of ['waiting', 'stalled']) listen(media, event, () => { waiting = true; renderState(); });
  listen(media, 'seeking', () => { waiting = true; renderState(); showControls(); });
  listen(media, 'seeked', () => { waiting = false; renderTimeline(); renderState(); showControls(); });
  listen(media, 'play', () => { needsGesture = false; renderState(); showControls(); });
  listen(media, 'playing', () => { waiting = false; needsGesture = false; renderState(); showControls(); });
  for (const event of ['pause', 'ended']) listen(media, event, () => { renderState(); showControls(); });
  listen(media, 'error', () => { if (media.error) { renderTimeline(); renderPresentation(); void reportMediaError(media.error); } });
  for (const event of ['fullscreenchange', 'webkitfullscreenchange']) listen(document, event, () => { renderPresentation(); showControls(); });
  for (const event of ['webkitbeginfullscreen', 'webkitendfullscreen', 'enterpictureinpicture', 'leavepictureinpicture', 'webkitpresentationmodechanged']) listen(media, event, renderPresentation);
  listen(portrait, 'change', () => togglePlaylist(portrait.matches));
  listen(document, 'keydown', event => {
    if (event.defaultPrevented || event.altKey || event.ctrlKey || event.metaKey || document.querySelector('dialog[open]')) return;
    if (event.key === 'Escape') {
      if (!menu.hidden) { event.preventDefault(); toggleMenu(false); more.focus(); }
      else if (!list.hidden && !fullscreenActive()) { event.preventDefault(); togglePlaylist(false); listToggle.focus(); }
      return;
    }
    if (event.target.closest('input,select,textarea,[contenteditable="true"],[role="textbox"]')) return;
    if (event.key === ' ' && event.target.closest('button,a')) return;
    const key = event.key.toLowerCase();
    const action = { ' ': 'play', k: 'play', arrowleft: 'backward', arrowright: 'forward', m: 'mute', f: 'fullscreen' }[key];
    if (action) {
      if (action === 'fullscreen' && fullscreenButton.hidden) return;
      event.preventDefault();
      void activate(action).catch(showOperationError);
    } else if (key === 'arrowup' || key === 'arrowdown') {
      event.preventDefault();
      media.volume = Math.max(0, Math.min(1, media.volume + (key === 'arrowup' ? 0.05 : -0.05)));
      media.muted = media.volume === 0;
      showControls();
    }
  });
  listen(menu, 'keydown', event => {
    const items = [...menu.querySelectorAll('button')];
    const index = items.indexOf(document.activeElement);
    if (['ArrowDown', 'ArrowUp', 'Home', 'End'].includes(event.key)) {
      event.preventDefault(); event.stopPropagation();
      const next = event.key === 'Home' ? 0 : event.key === 'End' ? items.length - 1 : (index + (event.key === 'ArrowDown' ? 1 : -1) + items.length) % items.length;
      items[next].focus();
    } else if (event.key === 'Tab') toggleMenu(false);
  });
  container.querySelector('.player-controlbar').title = t('playerShortcuts');
  togglePlaylist(portrait.matches);
  renderTimeline(); renderVolume(); renderPresentation(); renderState();
  media.src = source;
  void play();

  return {
    destroy() {
      if (destroyed) return;
      destroyed = true;
      playRequest++;
      clearTimeout(idleTimer);
      controller.abort();
      if (document.pictureInPictureElement === media) void document.exitPictureInPicture().catch(() => {});
      if (media.webkitPresentationMode === 'picture-in-picture') media.webkitSetPresentationMode('inline');
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
