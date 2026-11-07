import { languages, detectLanguage, translate } from './locales.js';
import { mountPlayer } from './player.js';
import { mountSelect, selectHTML } from './select.js';

export const escapeHTML = value => String(value ?? '').replace(/[&<>"']/g, character => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' })[character]);
export const isImage = entry => /^image\/(jpeg|png|gif|webp|avif|bmp)$/i.test(entry.mime || '');
export const isMedia = entry => /^(video|audio)\//i.test(entry.mime || '') || /\.(mp4|m4v|mov|mkv|webm|avi|mpg|mpeg|ts|m2ts|mp3|m4a|aac|flac|ogg|opus|wav)$/i.test(entry.name || '');
export const activeTask = task => ['queued', 'downloading', 'converting', 'held'].includes(task.state);
export const removableTask = task => ['ready', 'failed', 'cancelled'].includes(task.state);
export function taskPercent(task) {
  if (!(task.total > 0) || !Number.isFinite(task.done) || task.done < 0) return null;
  return Math.min(100, Math.max(0, task.done / task.total * 100));
}
export function contentURL(space, file, variant = 'original', download = false) {
  return '/api/content?' + new URLSearchParams({ space, id: file, variant, download: download ? '1' : '0' });
}
export async function probeCapabilities(info = {}, environment = globalThis) {
  const video = environment.document.createElement('video');
  const audio = environment.document.createElement('audio');
  const can = (mime, element = video) => Boolean(element.canPlayType(mime));
  const caps = {
    h264: can('video/mp4; codecs="avc1.640028"'),
    hevc: can('video/mp4; codecs="hvc1.1.6.L120.B0"'),
    hevc10: can('video/mp4; codecs="hvc1.2.4.L120.B0"'),
    vp9: can('video/webm; codecs="vp09.00.10.08"'),
    av1: can('video/mp4; codecs="av01.0.08M.08"'),
    aac: can('audio/mp4; codecs="mp4a.40.2"', audio),
    opus: can('audio/webm; codecs="opus"', audio),
    mp4: can('video/mp4'), webm: can('video/webm'),
    hdr: Boolean(environment.matchMedia?.('(dynamic-range: high)').matches)
  };
  const videoKey = { h264: 'h264', hevc: info.bitDepth > 8 ? 'hevc10' : 'hevc', vp9: 'vp9', av1: 'av1' }[info.videoCodec];
  const audioKey = { aac: 'aac', opus: 'opus' }[info.audioCodec];
  // Codec/profile probes use a browser-friendly target container, not the unsupported source container.
  if (videoKey && info.videoMime) caps.videoSupported = can(info.videoMime);
  if (audioKey && info.audioMime) caps.audioSupported = can(info.audioMime, audio);
  if (environment.navigator.mediaCapabilities?.decodingInfo && info.videoMime && videoKey && info.frameRate > 0 && info.videoBitrate > 0) {
    try {
      const result = await environment.navigator.mediaCapabilities.decodingInfo({ type: 'file', video: {
        contentType: info.videoMime, width: info.width, height: info.height,
        bitrate: info.videoBitrate,
        framerate: info.frameRate
      } });
      caps.videoSupported = caps.videoSupported && result.supported;
    } catch { /* canPlayType remains the fallback when MediaCapabilities rejects the configuration. */ }
  }
  return caps;
}


const paths = {
  folder: '<path d="M3 7h7l2 2h9v11H3z"/><path d="M3 7V4h7l2 3"/>',
  file: '<path d="M6 3h8l4 4v14H6z"/><path d="M14 3v5h4M9 12h6M9 16h6"/>',
  cloud: '<path d="M7 18a5 5 0 1 1 1-10 6 6 0 0 1 11 3 3.5 3.5 0 0 1-1 7z"/>',
  local: '<rect x="3" y="4" width="18" height="13" rx="2"/><path d="M8 21h8M12 17v4"/>',
  play: '<path d="m9 5 11 7-11 7z"/>',
  more: '<circle cx="12" cy="5" r="1"/><circle cx="12" cy="12" r="1"/><circle cx="12" cy="19" r="1"/>',
  back: '<path d="m15 5-7 7 7 7"/>',
  logout: '<path d="M9 4H4v16h5M10 12h11m-4-4 4 4-4 4"/>',
  close: '<path d="m6 6 12 12M6 18 18 6"/>',
  refresh: '<path d="M20 8a8 8 0 1 0 0 8M20 3v5h-5"/>',
  video: '<rect x="3" y="4" width="18" height="16" rx="3"/><path d="m10 8 6 4-6 4z"/>',
  image: '<rect x="3" y="3" width="18" height="18" rx="3"/><path d="m3 17 6-6 4 4 3-3 5 5"/><circle cx="16" cy="8" r="1"/>',
  eye: '<path d="M2.5 12s3.5-6.5 9.5-6.5 9.5 6.5 9.5 6.5-3.5 6.5-9.5 6.5S2.5 12 2.5 12zM12 15a3 3 0 1 0 0-6 3 3 0 0 0 0 6z"/>',
  eyeOff: '<path d="M3.5 3.5l17 17M10.4 5.6a9 9 0 0 1 1.6-.1c6 0 9.5 6.5 9.5 6.5a16 16 0 0 1-2.8 3.6M6.7 6.7C4 8.4 2.5 12 2.5 12s3.5 6.5 9.5 6.5a9.4 9.4 0 0 0 4.3-1M9.9 9.9a3 3 0 0 0 4.2 4.2"/>',
  lock: '<rect x="5" y="10" width="14" height="11" rx="3"/><path d="M8 10V7a4 4 0 0 1 8 0v3M12 14v3"/>'
};
const icon = name => `<svg viewBox="0 0 24 24" aria-hidden="true">${paths[name] || paths.file}</svg>`;

export function startApp() {
  const root = document.getElementById('app');
  const panel = document.getElementById('panel');
  let preference;
  try { preference = localStorage.getItem('pkp-lan-language'); } catch { /* Private browsing may disable storage. */ }
  const qrLanguage = new URLSearchParams(location.search).get('lang');
  const state = {
    language: Object.prototype.hasOwnProperty.call(languages, preference) ? preference : Object.prototype.hasOwnProperty.call(languages, qrLanguage) ? qrLanguage : detectLanguage(navigator.languages || [navigator.language]),
    authenticated: false, spaces: [], space: null, entries: [], path: [], tasks: [], selected: null,
    tab: 'files', view: '', query: '', loading: true, error: '', detailError: '', loginError: '', connected: true,
    dialog: '', plan: null, capabilities: null, prepareKind: '', prepareTask: '', intent: 0,
    epoch: 0, request: 0, taskRevision: 0, taskSnapshot: '', busy: false
  };
  let focusBeforeDialog = null;
  let noticeTimer;
  let pollBusy = false;
  let player = null;
  let libraryFocus = null;
  let libraryScroll = 0;
  let foreground = null;
  let languageMenu = null;
  let spaceMenu = null;
  let virtualList = null;
  let background = null;
  let filesChecked = 0, spacesChecked = 0;
  let filesRevision = '';
  let scrollFrame = 0;
  let nextPage = '', pageError = '';
  let pageController = null;
  const pageTokens = new Set();
  let directoryPages = new Map();
  const directories = new Map();
  let directoryEntries = 0;
  let localeFormats;
  function formatters() {
    if (localeFormats?.language !== state.language) localeFormats = {
      language: state.language, integer: new Intl.NumberFormat(state.language, { maximumFractionDigits: 0 }),
      decimal: new Intl.NumberFormat(state.language, { maximumFractionDigits: 1 }),
      date: new Intl.DateTimeFormat(state.language, { dateStyle: 'medium', timeStyle: 'short' }),
      collator: new Intl.Collator(state.language)
    };
    return localeFormats;
  }
  const t = key => translate(state.language, key);
  const h = escapeHTML;
  const sourceName = space => t(space.kind === 'local' ? 'local' : 'cloud');
  const formatSize = size => {
    if (!Number.isFinite(size) || size < 0) return t('unknown');
    const units = ['B', 'KB', 'MB', 'GB', 'TB'];
    const order = size > 0 ? Math.min(4, Math.floor(Math.log(size) / Math.log(1024))) : 0;
    return `${(order ? formatters().decimal : formatters().integer).format(size / 1024 ** order)} ${units[order]}`;
  };
  const formatDate = value => {
    const date = new Date(value);
    return value && !Number.isNaN(date.valueOf()) ? formatters().date.format(date) : t('unknown');
  };
  const button = (action, label, className = 'secondary', attrs = '') => `<button type="button" class="${className}" data-action="${action}" ${attrs}>${label === 'play' || label === 'playWhenReady' ? icon('play') : ''}${h(t(label))}</button>`;
  const iconButton = (action, label, name, attrs = '') => `<button type="button" class="icon-button" data-action="${action}" aria-label="${h(t(label))}" title="${h(t(label))}" ${attrs}>${icon(name)}</button>`;
  const operationStatus = document.createElement('div');
  operationStatus.id = 'operation-status'; operationStatus.className = 'operation-status'; operationStatus.hidden = true;
  operationStatus.innerHTML = `<span class="operation-spinner" aria-hidden="true"></span><span class="operation-message" role="status" aria-live="polite"></span>${button('cancel-operation', 'cancelWait')}`;
  document.body.append(operationStatus);
  function updateOperation(message = foreground?.message || 'loading', cancellable = foreground?.cancellable || false) {
    operationStatus.hidden = !foreground;
    if (!foreground) return;
    foreground.message = message; foreground.cancellable = cancellable;
    operationStatus.querySelector('.operation-message').textContent = t(message);
    const cancel = operationStatus.querySelector('button');
    cancel.textContent = t('cancelWait'); cancel.hidden = !cancellable;
    (panel.open ? panel : document.body).append(operationStatus);
  }
  const languageSelect = () => selectHTML({ id: 'language', label: t('language'), value: state.language, className: 'language-select', options: Object.entries(languages).map(([value, label]) => ({ value, label })) });
  const spaceSelect = () => selectHTML({ id: 'mobile-space', label: t('spaces'), value: state.space?.id || '', className: 'mobile-space-select', options: [{ value: '', label: t('spaces') }, ...state.spaces.map(space => ({ value: space.id, label: space.name }))] });
  const brand = () => `<div class="wordmark"><span class="brand-icon">${icon('folder')}</span><strong>PKP</strong><span>${h(t('title'))}</span></div>`;
  const notice = key => {
    const node = document.getElementById('notice');
    node.textContent = t(key); node.hidden = false;
    clearTimeout(noticeTimer); noticeTimer = setTimeout(() => { node.hidden = true; }, 8000);
  };
  function destroyMedia() {
    panel.querySelectorAll('video,audio').forEach(media => { media.pause(); media.removeAttribute('src'); media.load(); });
    panel.querySelectorAll('img').forEach(image => image.removeAttribute('src'));
  }
  function closePanel() {
    destroyMedia(); state.dialog = ''; panel.close(); panel.replaceChildren(); panel.className = '';
    if (focusBeforeDialog?.isConnected) focusBeforeDialog.focus();
    focusBeforeDialog = null;
    updateOperation();
  }
  function clearFocusView(restore = true) {
    player?.destroy(); player = null;
    root.querySelectorAll('#playback-view,#preparation-view').forEach(node => node.remove());
    state.view = ''; state.prepareTask = '';
    document.body.classList.remove('focus-view');
    const library = document.getElementById('library-view');
    if (library) library.hidden = false;
    if (restore) {
      window.scrollTo(0, libraryScroll);
      drawFiles();
      let target = libraryFocus?.element?.isConnected ? libraryFocus.element : null;
      if (!target && libraryFocus?.file) target = root.querySelector(libraryFocus.action ? `[data-action="${CSS.escape(libraryFocus.action)}"][data-file-id="${CSS.escape(libraryFocus.file)}"]` : `[data-file="${CSS.escape(libraryFocus.file)}"]`);
      if (!target && libraryFocus?.task) target = root.querySelector(`[data-task-id="${CSS.escape(libraryFocus.task)}"][data-action="${CSS.escape(libraryFocus.action)}"]`);
      target?.focus({ preventScroll: true });
    }
  }
  function focusView(kind, body = '') {
    if (!state.view && !history.state?.pkpLANView) {
      libraryScroll = window.scrollY;
      const active = document.activeElement;
      let origin = foreground?.element;
      const actionable = '[data-file],[data-file-id],[data-task-id]';
      if (!origin?.matches(actionable) || origin.closest('#panel')) origin = active;
      if (!origin?.matches(actionable) || origin.closest('#panel')) origin = state.selected ? root.querySelector(`[data-file="${CSS.escape(state.selected.id)}"]`) : active;
      libraryFocus = { element: origin, file: origin?.dataset.file || origin?.dataset.fileId, action: origin?.dataset.action, task: origin?.dataset.taskId };
      history.pushState({ pkpLANView: true }, '', `${location.pathname}${location.search}#${kind}`);
    } else {
      history.replaceState({ pkpLANView: true }, '', `${location.pathname}${location.search}#${kind}`);
    }
    clearFocusView(false); closePanel();
    state.view = kind;
    document.getElementById('library-view').hidden = true;
    document.body.classList.add('focus-view');
    const view = document.createElement('section');
    view.id = kind === 'player' ? 'playback-view' : 'preparation-view';
    view.className = kind === 'player' ? 'player-page' : 'preparation-page';
    view.innerHTML = body; root.append(view);
    window.scrollTo(0, 0);
    return view;
  }
  function abandonOperation() {
    const pending = foreground;
    foreground = null; state.busy = false;
    pending?.controller.abort();
    pending?.cancel?.();
    if (pending?.element?.isConnected) {
      pending.element.disabled = false;
      pending.element.removeAttribute('aria-busy');
      pending.element.focus({ preventScroll: true });
    }
    updateOperation();
  }
  function backToLibrary() {
    const ownedHistory = state.view && history.state?.pkpLANView;
    state.intent++; abandonOperation(); closePanel(); clearFocusView();
    state.selected = null; state.plan = null; state.detailError = '';
    if (ownedHistory) history.back();
  }
  function forgetSession(error = 'unauthorized') {
    state.epoch++; state.request++; state.intent++; state.authenticated = false;
    abandonOperation();
    state.spaces = []; state.entries = []; state.tasks = []; state.space = null; state.selected = null;
    background?.abort(); clearDirectories(); filesChecked = 0; spacesChecked = 0;
    state.path = []; state.plan = null; state.capabilities = null; state.error = ''; state.detailError = '';
    state.loading = false; state.loginError = error; state.taskSnapshot = ''; state.tab = 'files';
    if (history.state?.pkpLANView) history.replaceState(null, '', location.pathname + location.search);
    closePanel(); clearFocusView(false); document.getElementById('notice').hidden = true; render();
  }
  async function api(path, body, signal = foreground?.controller.signal) {
    const epoch = state.epoch, intent = state.intent;
    const controller = new AbortController();
    // Verifying an existing large desktop download reads its checksum once.
    const timer = setTimeout(() => controller.abort(), path.startsWith('/api/file?') || path === '/api/playback' ? 600000 : 60000);
    const abort = () => controller.abort();
    if (signal?.aborted) controller.abort();
    else signal?.addEventListener('abort', abort, { once: true });
    let response;
    try {
      response = await fetch(path, { method: body === undefined ? 'GET' : 'POST', credentials: 'same-origin', cache: 'no-store',
        headers: body === undefined ? {} : { 'Content-Type': 'application/json', 'X-PKP-Request': '1' },
        body: body === undefined ? undefined : JSON.stringify(body), signal: controller.signal });
      const data = await response.json();
      if (signal?.aborted) throw new DOMException('Aborted', 'AbortError');
      if (epoch === state.epoch && intent === state.intent) state.connected = true;
      if (!response.ok) {
        const code = data.error || 'internal';
        if (code === 'unauthorized' && state.authenticated && epoch === state.epoch) forgetSession();
        throw Object.assign(new Error(code), { code });
      }
      return data;
    } catch (error) {
      if (error.code || signal?.aborted) throw error;
      if (epoch === state.epoch && intent === state.intent) state.connected = false;
      throw Object.assign(new Error('offline'), { code: 'offline' });
    } finally { clearTimeout(timer); signal?.removeEventListener('abort', abort); }
  }
  function connectionHTML() {
    return `<span class="connection ${state.connected ? '' : 'offline'}" role="status">${h(t(state.connected ? 'connected' : 'offline'))}</span>`;
  }
  function mountLanguage() {
    languageMenu = mountSelect(root.querySelector('.language-select'), { onChange(value) {
      state.language = value;
      try { localStorage.setItem('pkp-lan-language', state.language); } catch { /* Optional preference only. */ }
      const dialog = state.dialog;
      render();
      if (dialog === 'detail') showPanel('detail', 'details', detailHTML());
      else if (dialog === 'confirm') showConfirmation();
      else if (panel.open) closePanel();
      root.querySelector('.language-select .select-face')?.focus({ preventScroll: true });
    } });
  }
  function mountSpaces() {
    const spaceSelector = root.querySelector('.mobile-space-select');
    spaceMenu = mountSelect(spaceSelector, { onChange(value) {
      void runOperation(async () => {
        if (value) await openSpace(value); else await action('home');
        root.querySelector('.mobile-space-select .select-face')?.focus({ preventScroll: true });
      }, spaceSelector.querySelector('.select-face'));
    } });
  }
  function render() {
    languageMenu?.destroy(); languageMenu = null;
    spaceMenu?.destroy(); spaceMenu = null;
    document.documentElement.lang = state.language;
    document.title = `PKP · ${t('title')}`;
    document.body.classList.toggle('authenticated', state.authenticated);
    updateOperation();
    if (!state.authenticated) {
      root.innerHTML = `<main class="gate"><header class="gate-header">${brand()}${languageSelect()}</header><div class="gate-layout"><section class="gate-art"><h1>${h(t('welcome'))}</h1><p class="muted">${h(t('gateIntro'))}</p></section><section class="gate-form"><p class="eyebrow">${h(t('title'))}</p><h2>${h(t('loginTitle'))}</h2><p class="muted">${h(t('passwordHint'))}</p><form id="login-form"><label for="password">${h(t('password'))}</label><input id="password" name="password" type="password" autocomplete="current-password" required maxlength="1024" autocapitalize="none" enterkeyhint="go" aria-describedby="login-error"><p id="login-error" class="form-error error" role="alert">${h(state.loginError ? t(state.loginError) : '')}</p><button class="primary" type="submit" ${state.loading ? 'disabled' : ''}>${h(t(state.loading ? 'loading' : 'enter'))}</button></form><p class="note warning">${h(t('http'))}</p></section></div></main>`;
      const password = document.getElementById('password');
      const field = document.createElement('div'); field.className = 'password-field';
      const visibility = document.createElement('button');
      visibility.type = 'button'; visibility.className = 'password-eye';
      visibility.setAttribute('aria-controls', 'password'); visibility.setAttribute('aria-pressed', 'false');
      visibility.setAttribute('aria-label', t('showPassword')); visibility.innerHTML = icon('eye');
      visibility.addEventListener('click', () => {
        const visible = password.type === 'password';
        password.type = visible ? 'text' : 'password';
        visibility.setAttribute('aria-label', t(visible ? 'hidePassword' : 'showPassword'));
        visibility.setAttribute('aria-pressed', String(visible));
        visibility.innerHTML = icon(visible ? 'eyeOff' : 'eye');
      });
      password.before(field); field.append(password, visibility);
      mountLanguage();
      return;
    }
    root.innerHTML = `<div id="library-view" class="portal"><header class="header"><div class="header-brand">${brand()}<span data-connection>${connectionHTML()}</span></div><div class="header-actions">${languageSelect()}${button('tasks', 'computerTasks', 'secondary desktop-tasks', `${state.space ? '' : 'disabled'} aria-current="${state.tab === 'tasks' ? 'page' : 'false'}"`)}<span class="task-count" data-task-count></span>${iconButton('logout', 'logout', 'logout')}</div></header><div class="layout"><nav class="sidebar" aria-label="${h(t('spaces'))}"><h2>${h(t('spaces'))}</h2><div class="mobile-space-nav">${spaceSelect()}</div><div class="sidebar-list">${button('home', 'spaces', 'space-button')}${state.spaces.map(spaceButton).join('')}</div><p class="sidebar-foot">${icon('lock')}${h(t('readOnlyShort'))}</p></nav><main class="library ${state.space ? '' : 'space-home'}">${state.tab === 'tasks' && state.space ? tasksPageHTML() : libraryHTML()}</main></div><nav class="bottom-nav" aria-label="${h(t('title'))}">${button('files', 'sharedFiles', '', `aria-current="${state.tab === 'files' ? 'page' : 'false'}"`)}${button('tasks', 'computerTasks', '', `${state.space ? '' : 'disabled'} aria-current="${state.tab === 'tasks' ? 'page' : 'false'}"`)}</nav></div>`;
    mountFiles();
    mountLanguage();
    mountSpaces();
    updateTaskCounts();
    updatePaging();
  }
  function spaceButton(space) {
    return `<button class="space-button" type="button" data-space="${h(space.id)}" title="${h(space.name)}" aria-current="${state.space?.id === space.id}">${icon(space.kind === 'local' ? 'local' : 'cloud')}<span><strong>${h(space.name)}</strong><small>${h(sourceName(space))}</small></span></button>`;
  }
  function libraryHTML() {
    if (!state.space) return `<section class="space-heading"><p class="eyebrow">${h(t('readonly'))}</p><h1>${h(t('chooseSpace'))}</h1></section>${state.error ? errorHTML(state.error) : ''}${state.loading ? `<p class="empty" role="status">${h(t('loading'))}</p>` : state.spaces.length ? `<div class="space-cards">${state.spaces.map(spaceButton).join('')}</div>` : `<p class="empty">${h(t('noSpaces'))}</p>`}<p class="footer-note">${h(t('http'))}</p>`;
    const folder = state.path.at(-1)?.name || state.space.name;
    return `<section class="space-heading"><p class="eyebrow">${h(sourceName(state.space))}</p><div class="heading-title"><h1 title="${h(folder)}">${h(folder)}</h1><span class="readonly-badge">${h(t('readOnlyShort'))}</span></div></section><div class="toolbar"><nav class="breadcrumbs" aria-label="${h(t('folder'))}"><button type="button" data-path="0" title="${h(state.space.name)}">${h(state.space.name)}</button>${state.path.map((part, index) => `<span aria-hidden="true">/</span><button type="button" data-path="${index + 1}" title="${h(part.name)}">${h(part.name)}</button>`).join('')}</nav><label class="search"><span class="sr-only">${h(t('search'))}</span><input id="search" type="search" placeholder="${h(t('search'))}" value="${h(state.query)}" autocomplete="off"></label>${iconButton('refresh', 'refresh', 'refresh')}</div><div id="preparation-summary">${summaryHTML()}</div><div class="file-table"><div class="file-table-heading" aria-hidden="true"><span>${h(t('file'))}</span><span>${h(t('type'))} · ${h(t('size'))}</span><span>${h(t('readyShort'))}</span><span>${h(t('modified'))}</span><span></span></div><div id="file-list" class="file-list" aria-busy="${state.loading}">${filesHTML()}</div></div>`;
  }
  function errorHTML(code) { return `<p class="note error error-box" role="alert">${h(t(code))}</p>`; }
  function fileArt(entry, detail = false) {
    if (isImage(entry) && entry.ready) return `<img src="${h(contentURL(state.space.id, entry.id))}" alt="${detail ? h(entry.name) : ''}" loading="lazy" decoding="async">`;
    return icon(entry.isDir ? 'folder' : isImage(entry) ? 'image' : isMedia(entry) ? 'video' : 'file');
  }
  function filesHTML() {
    if (state.loading) return `<p class="empty" role="status">${h(t('loading'))}</p>`;
    if (state.error) return `<div class="empty">${errorHTML(state.error)}${button('refresh', 'refresh')}</div>`;
    return '';
  }
  function fileHTML(entry, tasks) {
      const task = tasks.get(entry.id);
      const ready = entry.ready || entry.compatibleReady;
      const status = entry.isDir ? 'folder' : task ? task.state : entry.compatibleReady ? 'compatibleShort' : entry.ready ? 'readyShort' : entry.needsVerification ? 'verifyShort' : 'notReadyShort';
      const label = entry.isDir ? 'openFolder' : task ? 'viewProgress' : !ready ? 'prepareShort' : isMedia(entry) ? 'play' : isImage(entry) ? 'preview' : 'download';
      const unavailable = !entry.isDir && !ready && !task && !entry.needsVerification && !state.space.allowPrepare;
      const metadata = entry.isDir ? t('folder') : formatSize(entry.size);
      const extension = entry.isDir ? '' : entry.name.split('.').length > 1 ? entry.name.split('.').at(-1).toUpperCase() : t('file');
      return `<article class="file-row"><button type="button" class="file-main" data-file="${h(entry.id)}" title="${h(entry.name)}"><span class="file-cover">${fileArt(entry)}</span><span class="file-copy"><strong>${h(entry.name)}</strong><small class="file-mobile-meta">${h(metadata)}<span class="availability ${ready ? 'ready' : ''}">${h(t(status))}</span></small></span></button><span class="file-meta">${h(extension)}${extension ? ' · ' : ''}${h(metadata)}</span><span class="file-status"><span class="availability ${ready ? 'ready' : ''}">${h(t(status))}</span></span><time class="file-modified">${entry.modified ? h(formatDate(entry.modified)) : '—'}</time><div class="file-actions">${button('entry-primary', label, `file-primary ${ready && isMedia(entry) ? 'primary' : 'secondary'}`, `data-file-id="${h(entry.id)}" ${unavailable ? 'disabled' : ''}`)}${entry.isDir ? '' : iconButton('entry-details', 'more', 'more', `data-file-id="${h(entry.id)}"`)}</div></article>`;
  }
  const fileDisplayFields = ['id', 'name', 'mime', 'size', 'modified', 'isDir', 'ready', 'compatibleReady', 'needsVerification'];
  function cachedFileHTML(view, entry) {
    const taskState = view.tasks.get(entry.id)?.state;
    let cached = view.html.get(entry.id);
    if (!cached || cached.language !== state.language || cached.space !== state.space.id || cached.allowPrepare !== state.space.allowPrepare || cached.taskState !== taskState || fileDisplayFields.some(field => cached.entry[field] !== entry[field])) {
      const snapshot = {};
      for (const field of fileDisplayFields) snapshot[field] = entry[field];
      cached = { entry: snapshot, language: state.language, space: state.space.id, allowPrepare: state.space.allowPrepare, taskState, html: fileHTML(entry, view.tasks) };
      view.html.set(entry.id, cached);
    }
    return cached.html;
  }
  function mountFiles() {
    virtualList?.observer?.disconnect();
    virtualList = null;
    const list = document.getElementById('file-list');
    if (!list || state.loading || state.error) return;
    virtualList = { list, entries: [], heights: new Map(), rows: new Map(), html: new Map(), indices: new Map(), tasks: new Map(), offsets: [0], offsetsDirty: true, estimatedHeight: 80, estimateWidth: null, gap: null, spacers: [], width: 0, viewportWidth: window.innerWidth, observer: null, anchor: null, resizeAnchor: null, pinned: null, adjustedScroll: null };
    if (typeof ResizeObserver !== 'undefined') {
      virtualList.observer = new ResizeObserver(records => {
        const view = virtualList;
        if (!view || view.list !== list || state.view) return;
        let changed = false, widthChanged = false;
        for (const record of records) {
          if (record.target === list) {
            const width = record.contentRect.width;
            if (view.width && width && Math.abs(view.width - width) > 0.5) widthChanged = true;
            if (width) view.width = width;
          }
        }
        if (widthChanged) { view.resizeAnchor ||= view.anchor; view.heights.clear(); view.gap = null; view.estimateWidth = null; }
        for (const record of records) {
          if (record.target === list) continue;
          const row = record.target;
          if (!row.isConnected || view.rows.get(row.dataset.virtualId) !== row) continue;
          const height = row.getBoundingClientRect().height;
          if (!height) continue;
          if (view.estimateWidth === null) {
            changed ||= Math.abs(view.estimatedHeight - height) > 0.5;
            view.estimatedHeight = height; view.estimateWidth = window.innerWidth;
          }
          const previous = view.heights.get(row.dataset.virtualId) || view.estimatedHeight;
          if (Math.abs(height - previous) > 0.5) {
            view.heights.set(row.dataset.virtualId, height); changed = true;
          }
        }
        if (changed || widthChanged) redrawAnchored(view);
      });
      virtualList.observer.observe(list);
    }
    refreshFiles();
  }
  function refreshFiles() {
    const view = virtualList;
    if (!view || !view.list.isConnected) return;
    const query = state.query.toLocaleLowerCase(state.language);
    const sourceChanged = view.source !== state.entries;
    if (sourceChanged || view.language !== state.language) {
      view.searchNames = state.entries.map(entry => entry.name.toLocaleLowerCase(state.language));
      const present = new Set(state.entries.map(entry => entry.id));
      for (const id of view.html.keys()) if (!present.has(id)) view.html.delete(id);
      for (const id of view.heights.keys()) if (!present.has(id)) view.heights.delete(id);
    }
    if (sourceChanged || view.query !== query || view.language !== state.language) {
      const entries = query ? state.entries.filter((_entry, index) => view.searchNames[index].includes(query)) : state.entries;
      const reordered = entries.length !== view.entries.length || entries.some((entry, index) => entry.id !== view.entries[index].id);
      view.entries = entries; view.source = state.entries; view.query = query; view.language = state.language;
      if (reordered) {
        view.indices = new Map(entries.map((entry, index) => [entry.id, index]));
        view.offsetsDirty = true;
      }
    }
    if (view.taskSource !== state.tasks) {
      view.taskSource = state.tasks; view.tasks = new Map();
      for (const task of state.tasks) if (activeTask(task) && !view.tasks.has(task.fileId)) view.tasks.set(task.fileId, task);
    }
    drawFiles();
  }
  function redrawAnchored(view) {
    const anchor = view.resizeAnchor || view.anchor;
    view.offsetsDirty = true;
    if (view.estimateWidth !== window.innerWidth || view.gap === null) {
      const height = view.rows.values().next().value?.getBoundingClientRect().height;
      if (height) { view.estimatedHeight = height; view.estimateWidth = window.innerWidth; }
    }
    view.pinned = anchor?.id;
    for (let pass = 0; pass < 10; pass++) {
      drawFiles(false);
      let changed = false;
      for (const [id, row] of view.rows) {
        const height = row.getBoundingClientRect().height;
        if (height && Math.abs(height - (view.heights.get(id) || view.estimatedHeight)) > 0.5) {
          view.heights.set(id, height); changed = true;
        }
      }
      if (changed) { view.offsetsDirty = true; drawFiles(false); }
      const row = anchor && view.rows.get(anchor.id);
      const delta = row?.isConnected ? row.getBoundingClientRect().top - anchor.top : 0;
      if (Math.abs(delta) > 0.5) {
        window.scrollBy(0, delta);
        view.adjustedScroll = window.scrollY;
      }
      if (!changed && Math.abs(delta) <= 0.5) break;
    }
    view.pinned = null;
    if (!view.resizeAnchor) view.anchor = anchor;
  }
  function drawFiles(remember = true) {
    const view = virtualList;
    if (!view || !view.list.isConnected || state.view) return;
    const { list, entries, heights, rows } = view;
    if (!entries.length) {
      view.observer && rows.forEach(row => view.observer.unobserve(row));
      rows.clear(); list.innerHTML = `<p class="empty">${h(t(state.query && nextPage ? 'searchPartial' : state.query ? 'noMatches' : 'empty'))}</p>`; return;
    }
    const gap = view.gap ??= parseFloat(getComputedStyle(list).getPropertyValue('--file-gap')) || 0;
    if (view.offsetsDirty) {
      view.offsets = [0];
      for (const entry of entries) view.offsets.push(view.offsets.at(-1) + (heights.get(entry.id) || view.estimatedHeight) + gap);
      view.offsetsDirty = false;
    }
    const offsets = view.offsets, listTop = list.getBoundingClientRect().top;
    const top = Math.max(0, -listTop - window.innerHeight);
    const bottom = Math.max(0, -listTop + window.innerHeight * 2);
    let low = 0, high = entries.length;
    while (low < high) {
      const middle = (low + high) >>> 1;
      if (offsets[middle + 1] < top) low = middle + 1;
      else high = middle;
    }
    const start = Math.min(low, entries.length - 1);
    let end = start;
    while (end < entries.length && offsets[end] < bottom) end++;
    end = Math.max(end, Math.min(entries.length, start + 1));
    const focused = document.activeElement?.closest?.('.file-row');
    const focusedIndex = focused ? view.indices.get(focused.dataset.virtualId) ?? -1 : -1;
    const indices = [];
    const pinnedIndex = view.pinned ? view.indices.get(view.pinned) ?? -1 : -1;
    if (focusedIndex >= 0 && (focusedIndex < start || focusedIndex >= end)) indices.push(focusedIndex);
    if (pinnedIndex >= 0 && (pinnedIndex < start || pinnedIndex >= end) && pinnedIndex !== focusedIndex) indices.push(pinnedIndex);
    for (let index = start; index < end; index++) indices.push(index);
    indices.sort((a, b) => a - b);
    const wanted = new Set(indices.map(index => entries[index].id));
    for (const [id, row] of rows) if (!wanted.has(id)) { view.observer?.unobserve(row); row.remove(); rows.delete(id); }
    let spacerIndex = 0;
    const nodes = [], spacer = height => {
      if (height <= 0) return;
      let node = view.spacers[spacerIndex];
      if (!node) {
        node = document.createElement('div'); node.className = 'file-spacer'; node.setAttribute('aria-hidden', 'true');
        view.spacers[spacerIndex] = node;
      }
      spacerIndex++;
      const value = `${height}px`;
      if (node.style.height !== value) node.style.height = value;
      nodes.push(node);
    };
    let cursor = 0;
    for (const index of indices) {
      const entry = entries[index], html = cachedFileHTML(view, entry);
      spacer(offsets[index] - cursor);
      let row = rows.get(entry.id);
      if (!row || row.virtualHTML !== html) {
        const template = document.createElement('template'); template.innerHTML = html;
        const replacement = template.content.firstElementChild;
        replacement.dataset.virtualId = entry.id; replacement.virtualHTML = html;
        if (row) {
          const active = row.contains(document.activeElement) ? document.activeElement : null;
          const selector = active?.dataset.action ? `[data-action="${CSS.escape(active.dataset.action)}"]` : '.file-main';
          view.observer?.unobserve(row); row.replaceWith(replacement);
          if (active) replacement.querySelector(selector)?.focus({ preventScroll: true });
        }
        row = replacement; rows.set(entry.id, row); view.observer?.observe(row);
      }
      nodes.push(row); cursor = offsets[index + 1];
    }
    spacer(Math.max(0, offsets.at(-1) - gap - cursor));
    let position = list.firstChild;
    for (const node of nodes) {
      if (node === position) position = position.nextSibling;
      else list.insertBefore(node, position);
    }
    while (position) { const next = position.nextSibling; position.remove(); position = next; }
    if (remember && !view.resizeAnchor) {
      let first = null;
      for (const row of rows.values()) {
        const rect = row.getBoundingClientRect();
        if (rect.bottom > 0 && rect.top < window.innerHeight && (!first || rect.top < first.top)) first = { id: row.dataset.virtualId, top: rect.top };
      }
      view.anchor = first;
    }
  }
  function progressHTML(task, percent, label) {
    return `<span class="progress-track" role="progressbar" aria-label="${h(t(label))}" aria-valuemin="0" aria-valuemax="100" aria-valuenow="${percent}" data-progress-key="${h(task.id)}" data-progress-stage="${h(task.state)}"><span class="progress-fill${task.state === 'held' ? '' : ' moving'}" style="transform:scaleX(${percent / 100})"></span></span>`;
  }
  function reconcile(parent, html) {
    const template = document.createElement('template'); template.innerHTML = html;
    function sync(old, next) {
      if (old.nodeType !== next.nodeType || old.nodeName !== next.nodeName || old.nodeType === 1 && (old.getAttribute('data-progress-key') !== next.getAttribute('data-progress-key') || old.getAttribute('data-progress-stage') !== next.getAttribute('data-progress-stage'))) { old.replaceWith(next); return; }
      if (old.nodeType === 3) { if (old.nodeValue !== next.nodeValue) old.nodeValue = next.nodeValue; return; }
      if (old.nodeType !== 1) return;
      for (const attr of Array.from(old.attributes)) if (!next.hasAttribute(attr.name)) old.removeAttribute(attr.name);
      for (const attr of next.attributes) if (old.getAttribute(attr.name) !== attr.value) old.setAttribute(attr.name, attr.value);
      let child = old.firstChild, incoming = next.firstChild;
      while (incoming) {
        const following = incoming.nextSibling;
        if (child) { const after = child.nextSibling; sync(child, incoming); child = after; }
        else old.append(incoming);
        incoming = following;
      }
      while (child) { const after = child.nextSibling; child.remove(); child = after; }
    }
    let child = parent.firstChild, incoming = template.content.firstChild;
    while (incoming) {
      const following = incoming.nextSibling;
      if (child) { const after = child.nextSibling; sync(child, incoming); child = after; }
      else parent.append(incoming);
      incoming = following;
    }
    while (child) { const after = child.nextSibling; child.remove(); child = after; }
  }
  function summaryHTML() {
    const task = state.tasks.find(activeTask);
    if (!task) return '';
    const percent = taskPercent(task);
    return `<button type="button" class="preparation-summary" data-action="task-open" data-task-id="${h(task.id)}">${icon('refresh')}<span class="summary-copy"><strong>${h(task.name)}</strong><small>${h(t(task.state))}${percent === null ? '' : ` · ${h(Math.floor(percent))}%`}</small>${percent === null ? '' : `${progressHTML(task, percent, task.state)}`}</span>${icon('back')}</button>`;
  }
  function updateTaskCounts() {
    const count = state.tasks.filter(activeTask).length;
    document.querySelectorAll('[data-task-count]').forEach(node => { node.textContent = count ? String(count) : ''; node.hidden = !count; });
  }
  function refreshLibrary() {
    const list = document.getElementById('file-list');
    if (list && (state.loading || state.error)) {
      virtualList?.observer?.disconnect(); virtualList = null;
      list.innerHTML = filesHTML();
    } else if (list && !virtualList) mountFiles();
    else refreshFiles();
    const summary = document.getElementById('preparation-summary');
    if (summary) reconcile(summary, summaryHTML());
    updateTaskCounts();
    updatePaging();
  }
  function factsHTML(entry) {
    return `<dl class="facts"><dt>${h(t('size'))}</dt><dd>${h(formatSize(entry.size))}</dd><dt>${h(t('type'))}</dt><dd>${h(entry.mime || t('file'))}</dd><dt>${h(t('modified'))}</dt><dd>${h(formatDate(entry.modified))}</dd><dt>${h(t('spaces'))}</dt><dd>${h(state.space.name)}</dd></dl>`;
  }
  function detailHTML() {
    const entry = state.selected;
    if (!entry) return '';
    const task = state.tasks.find(item => item.fileId === entry.id && activeTask(item));
    const blocked = ['forbidden', 'source_unavailable', 'offline'].includes(state.detailError);
    return `<div class="detail-art">${blocked ? icon('file') : fileArt(entry, true)}</div><h3 class="detail-title">${h(entry.name)}</h3>${factsHTML(entry)}${state.detailError ? errorHTML(state.detailError) : ''}<p class="note ${entry.ready ? '' : 'warning'}">${h(t(entry.ready ? 'stageTwo' : entry.needsVerification ? 'needsVerification' : 'notReady'))}</p>${entry.compatibleReady ? `<p class="note">${h(t('compatibleReady'))}${entry.compatibleKind ? `<br>${h(t(entry.compatibleKind))}` : ''}</p>` : ''}${state.plan ? warningsHTML(state.plan) : ''}${task ? `<section class="task-slot">${taskHTML(task)}</section>` : ''}<div class="actions">${entry.ready ? button('download', 'download', 'primary', blocked ? 'disabled' : '') : button('prepare-original', 'prepare', 'primary', !state.space.allowPrepare || task || blocked || entry.needsVerification ? 'disabled' : '')}${entry.ready && isImage(entry) ? button('preview', 'preview', 'secondary', blocked ? 'disabled' : '') : ''}${entry.ready && isMedia(entry) ? button('play', 'play', 'secondary', blocked ? 'disabled' : '') : ''}${entry.compatibleReady ? button('play-compatible', 'playCompatible', 'secondary', blocked ? 'disabled' : '') : ''}</div>${!state.space.allowPrepare && !entry.ready && !entry.needsVerification ? `<p class="note warning">${h(t('prepareDisabled'))}</p>` : ''}`;
  }
  function taskHTML(task) {
    const percent = taskPercent(task);
    const stateKey = task.state === 'ready' ? 'taskReady' : task.state;
    return `<article class="task" data-task="${h(task.id)}"><strong>${h(task.name)}</strong><p class="task-state">${h(t(stateKey))}</p><p class="task-meta">${h(t(task.kind === 'compatible' ? 'compatible' : 'original'))}</p>${percent !== null && activeTask(task) ? `${progressHTML(task, percent, stateKey)}<p class="task-meta">${h(Math.floor(percent))}%${task.state === 'downloading' ? ` · ${h(formatSize(task.done))} / ${h(formatSize(task.total))}` : ''}</p>` : ''}${task.error ? errorHTML(task.error) : ''}<div class="task-actions">${button('task-open', activeTask(task) ? 'viewProgress' : 'details', 'secondary', `data-task-id="${h(task.id)}"`)}${activeTask(task) ? button('cancel-task', 'cancelTask', 'secondary', `data-task-id="${h(task.id)}"`) : removableTask(task) ? button('remove-task', 'removeTaskRecord', 'secondary', `data-task-id="${h(task.id)}"`) : ''}</div></article>`;
  }
  function tasksPageHTML() {
    return `<section class="tasks-heading"><div><p class="eyebrow">${h(state.space.name)}</p><h1 tabindex="-1">${h(t('computerTasks'))}</h1></div>${button('clear-tasks', 'clearTaskRecords', 'secondary', state.tasks.some(removableTask) ? '' : 'disabled')}</section><p class="tasks-intro muted">${h(t('twoStages'))}</p><p class="note">${h(t('taskRecordsHint'))}</p><div id="task-list" class="task-list">${state.tasks.length ? state.tasks.map(taskHTML).join('') : `<p class="empty">${h(t('noTasks'))}</p>`}</div><p class="note">${h(t('cancelHint'))}</p>`;
  }
  function refreshTaskList(fallbackID = '') {
    const list = document.getElementById('task-list');
    if (!list) return;
    const focused = list.contains(document.activeElement) ? document.activeElement : null;
    const id = focused?.dataset.taskId, action = focused?.dataset.action;
    const existing = new Map(Array.from(list.querySelectorAll('[data-task]'), node => [node.dataset.task, node]));
    if (!state.tasks.length) list.innerHTML = `<p class="empty">${h(t('noTasks'))}</p>`;
    else {
      list.querySelector('.empty')?.remove();
      let position = list.firstElementChild;
      for (const task of state.tasks) {
        const template = document.createElement('template'); template.innerHTML = taskHTML(task);
        const old = existing.get(task.id);
        let node = old;
        if (!old) node = template.content.firstElementChild;
        else reconcile(old, template.content.firstElementChild.innerHTML);
        existing.delete(task.id);
        if (node === position) position = position.nextElementSibling;
        else list.insertBefore(node, position);
      }
      existing.forEach(node => node.remove());
    }
    const clear = root.querySelector('[data-action="clear-tasks"]');
    if (clear) clear.disabled = !state.tasks.some(removableTask);
    if ((focused && !focused.isConnected) || fallbackID) {
      const target = id && list.querySelector(`[data-task-id="${CSS.escape(id)}"][data-action="${CSS.escape(action)}"]`)
        || fallbackID && list.querySelector(`[data-task-id="${CSS.escape(fallbackID)}"]`)
        || list.querySelector('button') || root.querySelector('.tasks-heading h1');
      target?.focus({ preventScroll: true });
    }
  }
  async function dismissTaskRecords(taskID = '') {
    const epoch = state.epoch, request = state.request, intent = state.intent, spaceID = state.space?.id;
    if (!spaceID) return;
    const index = state.tasks.findIndex(task => task.id === taskID);
    const nextID = state.tasks[index + 1]?.id || state.tasks[index - 1]?.id || '';
    state.taskRevision++;
    try {
      const result = await api(taskID ? '/api/tasks/remove' : '/api/tasks/clear', { spaceId: spaceID, ...(taskID ? { taskId: taskID } : {}) });
      if (!state.authenticated || epoch !== state.epoch || request !== state.request || intent !== state.intent || state.space?.id !== spaceID) return;
      const removed = new Set(result.removed);
      state.tasks = state.tasks.filter(task => !removed.has(task.id));
      state.taskSnapshot = JSON.stringify(state.tasks);
      refreshTaskList(nextID || taskID); refreshLibrary();
      notice(taskID ? 'taskRecordRemoved' : 'taskRecordsCleared');
    } finally { state.taskRevision++; }
  }
  function preparationStatusHTML() {
    const entry = state.selected;
    const task = state.tasks.find(item => item.id === state.prepareTask) || state.tasks.find(item => item.fileId === entry.id && activeTask(item));
    const ready = task?.kind === 'compatible' ? entry.compatibleReady : entry.ready;
    const percent = !ready && task && activeTask(task) ? taskPercent(task) : null;
    const stateKey = ready ? 'preparationComplete' : task?.state === 'ready' ? 'not_ready' : task?.state || 'notReadyShort';
    return `<p class="preparation-phase" role="status">${h(t(stateKey))}</p>${task ? `<p class="muted">${h(t(task.kind === 'compatible' ? 'compatible' : 'original'))}</p>` : ''}${percent === null ? '' : `${progressHTML(task, percent, stateKey)}<p class="preparation-percent">${h(Math.floor(percent))}%${task.state === 'downloading' ? ` · ${h(formatSize(task.done))} / ${h(formatSize(task.total))}` : ''}</p>`}${task?.error ? errorHTML(task.error) : ''}<p class="preparation-message muted">${h(t(ready ? 'twoStages' : 'preparationWait'))}</p><div class="preparation-actions">${button('preparation-play', isMedia(entry) ? ready ? 'play' : 'playWhenReady' : 'download', 'primary', ready ? '' : 'disabled')}${!ready && task && ['held', 'failed'].includes(task.state) ? button('retry-preparation', 'retryPreparation', 'secondary', state.space.allowPrepare ? '' : 'disabled') : ''}${button('preparation-tasks', 'computerTasks')}${task && activeTask(task) ? button('cancel-task', 'cancelTask', 'secondary', `data-task-id="${h(task.id)}"`) : ''}</div>`;
  }
  function showPreparation(task) {
    const entry = state.selected;
    const taskID = task?.id || '';
    focusView('preparation', `<header class="preparation-header">${iconButton('return-library', 'backToFolder', 'back')}<strong>${h(entry.name)}</strong></header><main class="preparation-content"><section class="preparation-card"><div class="preparation-symbol">${icon('refresh')}</div><h1>${h(t('preparingTitle'))}</h1><p class="preparation-name">${h(entry.name)}</p><div id="preparation-status"></div></section><div id="original-note" class="original-note"></div>${state.entries.some(item => !item.isDir && item.id !== entry.id) ? `<section class="other-files"><h2>${h(t('otherFiles'))}</h2>${state.entries.filter(item => !item.isDir && item.id !== entry.id).slice(0, 3).map(item => `<article>${icon(isMedia(item) ? 'video' : 'file')}<span><strong>${h(item.name)}</strong><small>${h(formatSize(item.size))} · ${h(t(item.ready || item.compatibleReady ? 'readyShort' : 'notReadyShort'))}</small></span>${button('entry-primary', item.ready || item.compatibleReady ? isMedia(item) ? 'play' : isImage(item) ? 'preview' : 'download' : 'prepareShort', 'secondary', `data-file-id="${h(item.id)}"`)}</article>`).join('')}</section>` : ''}</main>`);
    state.prepareTask = taskID;
    updatePreparation();
    document.getElementById('preparation-view').querySelector('button')?.focus({ preventScroll: true });
  }
  function updatePreparation() {
    const status = document.getElementById('preparation-status');
    if (!status || !state.selected) return;
    const focused = status.contains(document.activeElement) ? document.activeElement.dataset.action : '';
    reconcile(status, preparationStatusHTML());
    const original = document.getElementById('original-note');
    if (original) reconcile(original, `<span>${h(t(state.selected.ready ? 'originalAvailable' : 'originalUnchanged'))}</span>${state.selected.ready ? button('download', 'download', 'secondary') : ''}`);
    const task = state.tasks.find(item => item.id === state.prepareTask);
    const ready = task?.kind === 'compatible' ? state.selected.compatibleReady : state.selected.ready;
    document.querySelector('.preparation-card h1').textContent = t(ready ? 'preparationComplete' : 'preparingTitle');
    if (focused && !status.contains(document.activeElement)) status.querySelector(`[data-action="${CSS.escape(focused)}"]:not(:disabled)`)?.focus({ preventScroll: true });
  }
  async function openTask(id) {
    const task = state.tasks.find(item => item.id === id);
    if (!task) return;
    state.intent++;
    state.selected = state.entries.find(entry => entry.id === task.fileId) || { id: task.fileId, name: task.name };
    state.plan = null; state.detailError = '';
    if (await freshEntry()) showPreparation(task);
  }
  function panelHTML(title, body) {
    return `<header class="dialog-heading"><h2 id="panel-title" tabindex="-1">${h(t(title))}</h2>${iconButton('close', 'close', 'close')}</header><div class="dialog-body">${body}</div>`;
  }
  function showPanel(kind, title, body) {
    if (!panel.open) focusBeforeDialog = foreground?.element?.isConnected ? foreground.element : document.activeElement;
    destroyMedia(); state.dialog = kind; panel.className = kind === 'image' ? 'image-dialog' : '';
    panel.innerHTML = panelHTML(title, body);
    if (!panel.open) panel.showModal();
    panel.querySelector('#panel-title').focus();
    updateOperation();
  }
  function updateDetails() {
    const focus = document.activeElement;
    const action = focus?.dataset.action;
    if (state.dialog === 'detail') reconcile(panel, panelHTML('details', detailHTML()));
    if (action && !focus.isConnected) {
      Array.from(panel.querySelectorAll('[data-action]')).find(node => node.dataset.action === action)?.focus({ preventScroll: true });
    }
    if (focus?.id === 'panel-title' && !focus.isConnected && panel.open) panel.querySelector('#panel-title')?.focus();
    updateOperation();
  }
  async function loadSpaces() {
    const epoch = state.epoch, intent = state.intent;
    const result = await api('/api/state');
    if (!state.authenticated || epoch !== state.epoch || intent !== state.intent) return;
    state.spaces = result.spaces || []; spacesChecked = Date.now();
    state.loading = false; state.error = ''; render();
  }
  async function loadFiles(refresh = false) {
    if (!state.space) return;
    const request = ++state.request;
    state.intent++; closePanel();
    const space = state.space.id, parent = state.path.at(-1)?.id || '', key = `${space}/${parent}`;
    const cached = refresh ? null : directories.get(key);
    pageController?.abort(); pageController = null;
    nextPage = cached?.nextPage || ''; pageError = ''; pageTokens.clear();
    directoryPages = cached ? new Map(cached.pages) : new Map();
    state.loading = !cached; state.error = ''; state.entries = cached?.entries || [];
    state.selected = null; state.detailError = ''; state.plan = null;
    filesRevision = cached?.revision || '';
    updateOperation(cached ? 'refreshing' : 'loading', true);
    if (foreground) foreground.cancel = () => {
      if (request !== state.request) return;
      state.request++; state.loading = false; state.error = cached ? '' : 'waitCancelled'; render();
    };
    render();
    if (cached) window.scrollTo(0, cached.scroll);
    const query = new URLSearchParams({ space, parent, files: '1' });
    if (filesRevision) query.set('revision', filesRevision);
    try {
      const result = await api('/api/state?' + query);
      if (request !== state.request || !state.authenticated) return;
      state.space = result.space;
      applyPage('', result.files, !cached);
      state.loading = false; rememberDirectory(key); filesChecked = Date.now();
    } catch (error) {
      if (request !== state.request || !state.authenticated || foreground?.controller.signal.aborted) return;
      state.error = error.code; state.entries = []; clearDirectories();
    }
    if (request === state.request && state.authenticated) {
      state.loading = false; render();
      if (cached) window.scrollTo(0, cached.scroll);
      if (state.query) void loadMore();
    }
  }
  function applyPage(token, files, reset = false) {
    if (!files) return;
    if (reset) directoryPages.clear();
    const previous = directoryPages.get(token);
    if (files.entries) {
      if (previous && (token === '' || previous.nextPage !== files.nextPage)) {
        let remove = false;
        for (const key of directoryPages.keys()) { if (remove) directoryPages.delete(key); if (key === token) remove = true; }
      }
      directoryPages.set(token, { entries: files.entries, revision: files.revision, nextPage: files.nextPage || '' });
      const byID = new Map();
      for (const page of directoryPages.values()) for (const entry of page.entries) byID.set(entry.id, entry);
      state.entries = sortEntries([...byID.values()]);
    }
    filesRevision = directoryPages.get('')?.revision || files.revision;
    nextPage = [...directoryPages.values()].at(-1)?.nextPage || '';
    pageTokens.clear(); for (const key of directoryPages.keys()) if (key) pageTokens.add(key);
  }
  function visiblePage() {
    const rows = document.querySelectorAll('.file-row');
    let id;
    for (const row of rows) { const rect = row.getBoundingClientRect(); if (rect.bottom > 0 && rect.top < window.innerHeight) { id = row.dataset.virtualId; break; } }
    if (id) for (const [token, page] of directoryPages) if (page.entries.some(entry => entry.id === id)) return token;
    return '';
  }
  function acceptEntry(entry) {
    state.selected = entry; state.detailError = '';
    state.entries = state.entries.map(item => item.id === entry.id ? entry : item);
    for (const page of directoryPages.values()) {
      const index = page.entries.findIndex(item => item.id === entry.id);
      if (index >= 0) page.entries = page.entries.map(item => item.id === entry.id ? entry : item);
    }
  }
  function updatePaging() {
    let node = document.getElementById('file-paging');
    const table = document.querySelector('.file-table');
    if (!table) return;
    if (!node) { node = document.createElement('div'); node.id = 'file-paging'; node.className = 'file-paging'; table.append(node); }
    node.hidden = !nextPage && !pageError;
    node.innerHTML = pageError ? `${errorHTML(pageError)}${button('load-more', 'retry')}` : pageController
      ? `<span class="operation-spinner" aria-hidden="true"></span><span role="status">${h(t('loadingMore'))}</span>`
      : `${button('load-more', 'loadMore')}${state.query ? `<span class="muted">${h(t('searchPartial'))}</span>` : ''}`;
  }
  async function loadMore(manual = false) {
    if (!nextPage || pageController || state.loading || !state.space || state.view || !manual && (state.busy || pageError)) return;
    background?.abort();
    const controller = new AbortController(); pageController = controller; pageError = ''; updatePaging();
    if (manual) updateOperation('loadingMore', true);
    if (manual && foreground) foreground.cancel = () => { controller.abort(); if (pageController === controller) { pageController = null; updatePaging(); } };
    const token = nextPage, space = state.space.id, parent = state.path.at(-1)?.id || '', request = state.request, epoch = state.epoch;
    const current = () => state.authenticated && epoch === state.epoch && request === state.request && space === state.space?.id && !controller.signal.aborted;
    try {
      const result = await api('/api/state?' + new URLSearchParams({ space, parent, files: '1', page: token }), undefined, controller.signal);
      if (!current()) return;
      const entries = result.files?.entries || [], following = result.files?.nextPage || '';
      if (pageTokens.has(token) || following === token || following && pageTokens.has(following) || state.entries.length + entries.length > 10000) throw { code: 'invalid_request' };
      applyPage(token, result.files);
      rememberDirectory(); refreshLibrary();
    } catch (error) {
      if (!current()) return;
      if (['forbidden', 'source_unavailable'].includes(error.code)) {
        clearDirectories(); state.entries = []; state.error = error.code; refreshLibrary();
      } else pageError = error.code || 'offline';
    } finally {
      if (pageController === controller) { pageController = null; updatePaging(); }
      if (current() && state.query && nextPage && !pageError) void loadMore();
    }
  }
  function loadNearEnd() {
    const list = document.getElementById('file-list');
    if (list && !state.view && list.getBoundingClientRect().bottom < window.innerHeight + 600) void loadMore();
  }
  function sortEntries(entries) {
    const collator = formatters().collator;
    return entries.sort((a, b) => Number(b.isDir) - Number(a.isDir) || collator.compare(a.name, b.name));
  }
  function rememberDirectory(key = state.space && `${state.space.id}/${state.path.at(-1)?.id || ''}`) {
    if (!key || state.loading || state.error) return;
    directoryEntries -= directories.get(key)?.entries.length || 0;
    directories.delete(key);
    directories.set(key, { entries: state.entries, revision: filesRevision, scroll: window.scrollY, nextPage, pages: new Map(directoryPages) });
    directoryEntries += state.entries.length;
    while (directories.size > 12 || directoryEntries > 20000) {
      const oldest = directories.keys().next().value;
      directoryEntries -= directories.get(oldest).entries.length; directories.delete(oldest);
    }
  }
  function clearDirectories() { directories.clear(); directoryPages.clear(); directoryEntries = 0; filesRevision = ''; nextPage = ''; pageError = ''; pageController?.abort(); pageController = null; }
  async function openSpace(id) {
    const space = state.spaces.find(item => item.id === id);
    if (!space) return;
    rememberDirectory(); closePanel(); state.tab = 'files'; state.space = space; state.path = []; state.query = ''; state.tasks = []; state.taskSnapshot = '';
    filesRevision = ''; filesChecked = 0;
    await loadFiles();
  }
  async function freshEntry(signal = foreground?.controller.signal) {
    if (!state.space || !state.selected) return null;
    const space = state.space.id, id = state.selected.id, epoch = state.epoch, intent = state.intent;
    const current = () => state.authenticated && state.epoch === epoch && state.intent === intent && state.space?.id === space && state.selected?.id === id;
    if (signal && signal === foreground?.controller.signal) updateOperation('checkingFile', true);
    let entry;
    try { entry = await api('/api/file?' + new URLSearchParams({ space, id }), undefined, signal); }
    catch (error) { if (!current()) return null; throw error; }
    if (!current()) return null;
    acceptEntry(entry);
    return entry;
  }
  async function selectEntry(id, details = false) {
    const entry = state.entries.find(item => item.id === id);
    if (!entry) return;
    state.intent++; closePanel();
    if (state.view) clearFocusView();
    if (entry.isDir) { rememberDirectory(); state.path.push({ id: entry.id, name: entry.name }); state.query = ''; await loadFiles(); return; }
    state.selected = entry; state.detailError = ''; state.plan = null;
    if (!details && isMedia(entry) && !state.tasks.some(item => item.fileId === id && activeTask(item))) {
      await planPlayback(); refreshLibrary(); return;
    }
    const current = await freshEntry();
    if (!current) return;
    const task = state.tasks.find(item => item.fileId === id && activeTask(item));
    if (details) showPanel('detail', 'details', detailHTML());
    else if (task) showPreparation(task);
    else if (!current.ready && !current.compatibleReady) {
      if (state.space.allowPrepare) await prepareOriginal(current);
      else showPanel('detail', 'details', detailHTML());
    } else if (isMedia(current)) await planPlayback();
    else if (isImage(current)) await showImage(current);
    else await download(current);
    refreshLibrary();
    if (!state.view && history.state?.pkpLANView) history.replaceState(null, '', location.pathname + location.search);
  }
  function handleError(error, detail = false) {
    if (error.code === 'unauthorized') return;
    if (detail && state.selected) {
      state.detailError = error.code || 'internal';
      if (['forbidden', 'source_unavailable', 'offline'].includes(error.code)) {
        destroyMedia();
        if (state.view) backToLibrary();
      } else updateDetails();
    }
    notice(error.code || 'internal');
    updateConnection();
  }
  function updateConnection() {
    document.querySelectorAll('[data-connection]').forEach(node => { node.innerHTML = connectionHTML(); });
  }
  async function download(entry) {
    entry ??= await freshEntry();
    if (!entry) return;
    if (!entry.ready) throw { code: 'not_ready' };
    const link = document.createElement('a');
    link.href = contentURL(state.space.id, entry.id, 'original', true); link.download = entry.name;
    document.body.append(link); link.click(); link.remove(); notice('browserDownload'); updateDetails();
  }
  function warningsHTML(plan) {
    const warnings = new Set(plan.warnings || []);
    if (plan.info?.subtitles) warnings.add('subtitles_unsupported');
    return [...warnings].map(code => `<p class="note warning">${h(t(code))}</p>`).join('') + (plan.info?.hdr ? `<p class="note warning">${h(t('hdrNotice'))}</p>` : '');
  }
  async function prepareOriginal(entry) {
    entry ??= await freshEntry();
    if (!entry) return;
    if (entry.ready) { updateDetails(); updatePreparation(); refreshLibrary(); return; }
    if (!state.space.allowPrepare) throw { code: 'prepare_disabled' };
    state.prepareKind = 'original'; state.plan = null;
    showConfirmation();
  }
  async function retryPreparation() {
    const task = state.tasks.find(item => item.id === state.prepareTask);
    if (!task || !['held', 'failed'].includes(task.state)) return;
    if (task.kind === 'original') { await prepareOriginal(); return; }
    const entry = await freshEntry();
    if (!entry) return;
    if (entry.compatibleReady) { updateDetails(); updatePreparation(); return; }
    if (!state.space.allowPrepare) throw { code: 'prepare_disabled' };
    if (entry.ready) { await planPlayback(); return; }
    const epoch = state.epoch, intent = state.intent, spaceID = state.space.id, fileID = entry.id;
    const capabilities = await probeCapabilities();
    if (!state.authenticated || epoch !== state.epoch || intent !== state.intent || state.space?.id !== spaceID || state.selected?.id !== fileID) return;
    state.capabilities = capabilities;
    state.prepareKind = 'compatible'; state.plan = null;
    showConfirmation();
  }
  function showConfirmation() {
    const entry = state.selected;
    const compatible = state.prepareKind === 'compatible';
    showPanel('confirm', 'confirmTitle', `<h3 class="detail-title">${h(entry.name)}</h3>${factsHTML(entry)}<p class="note warning">${h(t(compatible ? 'costCompatible' : 'costOriginal'))}</p>${compatible && state.plan ? `<p class="note">${h(t(state.plan.kind))}</p>${warningsHTML(state.plan)}` : ''}<p class="note">${h(t('twoStages'))}</p><div class="dialog-actions">${button('back', 'notNow')}${button('confirm-prepare', compatible ? 'confirmCompatible' : 'confirmOriginal', 'primary')}</div>`);
  }
  async function planPlayback(variant = '') {
    if (!state.selected || !state.space) return;
    updateOperation('checkingPlayback', true);
    const spaceId = state.space.id, fileId = state.selected.id, epoch = state.epoch, intent = state.intent;
    const current = () => state.authenticated && epoch === state.epoch && intent === state.intent && state.selected?.id === fileId && state.space?.id === spaceId;
    let capabilities = await probeCapabilities();
    let result;
    for (let attempt = 0; attempt < 2; attempt++) {
      result = await api('/api/playback', { spaceId, fileId, variant, capabilities });
      if (!current()) return;
      if (!result.plan) break;
      const exact = await probeCapabilities(result.plan.info);
      if (!current()) return;
      if (attempt || (exact.videoSupported !== false && exact.audioSupported !== false)) { capabilities = exact; break; }
      capabilities = exact;
    }
    acceptEntry(result.entry); state.plan = result.plan; state.capabilities = capabilities;
    if (!result.plan) {
      if (state.space.allowPrepare) await prepareOriginal(result.entry);
      else showPanel('detail', 'details', detailHTML());
    } else if (result.plan.kind === 'direct') showPlayer(result.variant, result.entry);
    else if (result.plan.kind === 'unsupported') {
      const plan = result.plan;
      showPanel('unsupported', 'details', `<h3 class="detail-title">${h(result.entry.name)}</h3>${errorHTML(plan.reason === 'hdr_unsupported' ? 'hdr_unsupported' : 'unsupported')}${warningsHTML(plan)}<div class="dialog-actions">${button('back', 'back')}${button('download', 'download', 'primary')}</div>`);
    } else {
      if (!state.space.allowPrepare) throw { code: 'prepare_disabled' };
      state.prepareKind = 'compatible'; showConfirmation();
    }
  }
  async function prepareConfirmed() {
    if (!state.selected || state.dialog !== 'confirm') return;
    const spaceId = state.space.id, fileId = state.selected.id, intent = state.intent;
    const task = await api('/api/prepare', { spaceId, fileId, kind: state.prepareKind, ...(state.prepareKind === 'compatible' ? { capabilities: state.capabilities } : {}) });
    if (!state.authenticated || state.space?.id !== spaceId || state.selected?.id !== fileId || intent !== state.intent) return;
    state.tasks = [task, ...state.tasks.filter(item => item.id !== task.id)];
    refreshLibrary(); showPreparation(task);
    void poll();
  }
  function showPlayer(variant, entry) {
    if (variant === 'compatible' ? !entry.compatibleReady : !entry.ready) throw { code: 'not_ready' };
    const view = focusView('player');
    const current = entry.id, epoch = state.epoch, space = state.space.id;
    const playlist = state.entries.filter(item => !item.isDir && isMedia(item) && item.parentId === entry.parentId);
    if (!playlist.some(item => item.id === entry.id)) playlist.unshift(entry);
    const playerEntry = state.plan?.info?.audioCodec && !state.plan.info.videoCodec ? { ...entry, mime: 'audio/' } : entry;
    player = mountPlayer({
      container: view, entry: playerEntry, source: contentURL(state.space.id, entry.id, variant),
      variant, language: state.language, connected: state.connected,
      playlist,
      onBack: backToLibrary,
      onSelect: id => runOperation(() => selectEntry(id)),
      onDetails: () => { showPanel('detail', 'playbackInfo', detailHTML()); },
      onDownload: () => runOperation(download),
      onError: async () => {
        const currentView = () => view.isConnected && state.authenticated && state.epoch === epoch && state.space?.id === space && state.selected?.id === current;
        try { if (currentView()) await freshEntry(null); }
        catch (error) { if (currentView()) handleError(error, true); }
      }
    });
  }
  async function showImage(entry) {
    entry ??= await freshEntry();
    if (!entry) return;
    if (!entry.ready) throw { code: 'not_ready' };
    showPanel('image', 'preview', `<h3 class="detail-title">${h(entry.name)}</h3><div class="image-stage"><img src="${h(contentURL(state.space.id, entry.id))}" alt="${h(entry.name)}"></div><div id="image-error" role="alert"></div><div class="dialog-actions">${button('download', 'download')}</div>`);
    panel.querySelector('img').addEventListener('error', () => {
      const error = document.getElementById('image-error');
      if (error) error.innerHTML = errorHTML('source_unavailable');
    }, { once: true });
  }
  async function poll() {
    if (pollBusy || state.busy || document.hidden || !state.authenticated || state.loading) return;
    pollBusy = true;
    const controller = new AbortController(); background = controller;
    const epoch = state.epoch, intent = state.intent, spaceID = state.space?.id, request = state.request, revision = state.taskRevision;
    const current = () => state.authenticated && epoch === state.epoch && intent === state.intent && request === state.request && revision === state.taskRevision && !controller.signal.aborted;
    const query = new URLSearchParams();
    if (spaceID) {
      query.set('space', spaceID);
      query.set('tasks', state.tab === 'tasks' || state.view === 'preparation' ? 'all' : 'active');
      if (!state.view && !pageController && state.tab === 'files' && Date.now() - filesChecked >= 30000) {
        const token = visiblePage();
        query.set('files', '1'); query.set('parent', state.path.at(-1)?.id || '');
        if (token) query.set('page', token);
        const page = directoryPages.get(token);
        if (page?.revision) query.set('revision', page.revision);
      }
      if (state.selected && (state.dialog || state.view === 'preparation')) query.set('id', state.selected.id);
    }
    if (!spaceID || Date.now() - spacesChecked >= 30000) query.set('spaces', '1');
    try {
      const result = await api('/api/state?' + query, undefined, controller.signal);
      if (!current()) return;
      let permissionChanged = false, filesChanged = false, spacesChanged = false;
      if (result.spaces) {
        spacesChanged = JSON.stringify(state.spaces.map(({ id, name, kind }) => [id, name, kind])) !== JSON.stringify(result.spaces.map(({ id, name, kind }) => [id, name, kind]));
        state.spaces = result.spaces; spacesChecked = Date.now();
      }
      if (result.space && state.space?.id === spaceID) {
        permissionChanged = state.space.allowPrepare !== result.space.allowPrepare; state.space = result.space;
        state.spaces = state.spaces.map(space => space.id === spaceID ? result.space : space);
      }
      if (result.files) {
        filesChecked = Date.now();
        applyPage(query.get('page') || '', result.files);
        if (result.files.entries) { state.error = ''; rememberDirectory(); filesChanged = true; }
      }
      const snapshot = JSON.stringify(result.tasks || []), changed = snapshot !== state.taskSnapshot;
      if (result.tasks) { state.tasks = result.tasks; state.taskSnapshot = snapshot; }
      if (result.selectedError) { clearDirectories(); handleError({ code: result.selectedError }, true); }
      else if (result.selected) {
        acceptEntry(result.selected);
        updateDetails(); updatePreparation();
      }
      if (filesChanged || changed || permissionChanged || result.selected) refreshLibrary();
      if (spacesChanged && spaceID) {
        const nav = root.querySelector('.mobile-space-nav'), list = root.querySelector('.sidebar-list');
        if (nav && list) {
          const focused = nav.contains(document.activeElement), open = spaceMenu?.open;
          spaceMenu?.destroy(); nav.innerHTML = spaceSelect(); mountSpaces();
          list.innerHTML = button('home', 'spaces', 'space-button') + state.spaces.map(spaceButton).join('');
          if (open) nav.querySelector('.select-face')?.click();
          else if (focused) nav.querySelector('.select-face')?.focus({ preventScroll: true });
        }
      }
      if (permissionChanged && !state.space.allowPrepare && state.dialog === 'confirm') showPanel('detail', 'details', detailHTML());
      if (changed) refreshTaskList();
      if (!spaceID && result.spaces) render();
      updateConnection();
    } catch (error) {
      if (!current()) return;
      if (['forbidden', 'source_unavailable'].includes(error.code)) {
        clearDirectories(); state.entries = []; state.error = error.code; refreshLibrary();
      }
      handleError(error, Boolean(state.selected));
    } finally { if (background === controller) background = null; pollBusy = false; }
  }
  async function action(name, element) {
    switch (name) {
      case 'back': dismissPanel(); if (state.view) backToLibrary(); return;
      case 'logout': await api('/api/logout', {}); forgetSession(''); return;
      case 'home': rememberDirectory(); closePanel(); state.intent++; state.request++; state.tab = 'files'; state.space = null; state.selected = null; state.entries = []; state.tasks = []; state.error = ''; state.loading = true; render(); await loadSpaces(); return;
      case 'refresh': if (state.space) await loadFiles(true); else await loadSpaces(); return;
      case 'load-more': await loadMore(true); return;
      case 'files': state.tab = 'files'; render(); return;
      case 'tasks': if (state.space) { state.tab = 'tasks'; render(); } return;
      case 'preparation-tasks': backToLibrary(); state.tab = 'tasks'; render(); return;
      case 'entry-primary': await selectEntry(element.dataset.fileId); return;
      case 'entry-details': await selectEntry(element.dataset.fileId, true); return;
      case 'task-open': await openTask(element.dataset.taskId); return;
      case 'download': await download(); return;
      case 'preview': await showImage(); return;
      case 'play': await planPlayback(); return;
      case 'play-compatible': await planPlayback('compatible'); return;
      case 'preparation-play': {
        if (!state.selected) return;
        if (isMedia(state.selected)) await planPlayback();
        else await download();
        return;
      }
      case 'prepare-original': await prepareOriginal(); return;
      case 'confirm-prepare': await prepareConfirmed(); return;
      case 'retry-preparation': await retryPreparation(); return;
      case 'remove-task': await dismissTaskRecords(element.dataset.taskId); return;
      case 'clear-tasks': await dismissTaskRecords(); return;
      case 'cancel-task':
        await api('/api/cancel', { spaceId: state.space.id, taskId: element.dataset.taskId });
        notice('cancelHint');
        if (state.view === 'preparation') backToLibrary();
        void poll(); return;
    }
  }
  async function runOperation(operation, element) {
    if (state.busy) {
      const file = element?.dataset.file || element?.dataset.fileId;
      const pendingFile = foreground?.element?.dataset.file || foreground?.element?.dataset.fileId;
      if (!foreground?.cancellable || foreground.element === element || file && file === pendingFile) return;
      state.intent++; abandonOperation();
    }
    background?.abort();
    if (pageController) { pageController.abort(); pageController = null; updatePaging(); }
    const epoch = state.epoch;
    const owner = { controller: new AbortController(), element };
    element ||= document.activeElement instanceof HTMLButtonElement ? document.activeElement : null;
    owner.element = element;
    foreground = owner;
    const focusFile = element?.dataset.file || element?.dataset.fileId;
    const focusAction = element?.dataset.action;
    try {
      state.busy = true;
      if (element) { element.disabled = true; element.setAttribute('aria-busy', 'true'); }
      updateOperation();
      await operation();
    } catch (error) { if (foreground === owner && epoch === state.epoch) handleError(error, Boolean(state.selected)); }
    finally {
      if (foreground === owner) {
        foreground = null; state.busy = false;
        updateOperation();
        if (element?.isConnected) { element.disabled = element.dataset.action === 'clear-tasks' && !state.tasks.some(removableTask); element.removeAttribute('aria-busy'); }
        else if (!panel.open && !state.view && epoch === state.epoch && document.activeElement === document.body) {
          const replacement = Array.from(root.querySelectorAll('button')).find(node =>
            (focusFile && (node.dataset.file === focusFile || node.dataset.fileId === focusFile)) || (focusAction && node.dataset.action === focusAction));
          if (replacement) replacement.focus();
          else {
            const heading = root.querySelector('main h1');
            if (heading) { heading.tabIndex = -1; heading.focus(); }
          }
        }
        if (state.tab === 'tasks' || state.view === 'preparation') void poll();
        if (state.query && nextPage && !pageError) void loadMore();
      }
    }
  }
  document.addEventListener('click', event => {
    const element = event.target.closest('button');
    if (!element || element.disabled || (!element.dataset.action && !element.dataset.space && !element.dataset.file && element.dataset.path === undefined)) return;
    if (element.dataset.action === 'close') { dismissPanel(); return; }
    if (element.dataset.action === 'return-library') { backToLibrary(); return; }
    if (element.dataset.action === 'cancel-operation') { state.intent++; abandonOperation(); return; }
    void runOperation(async () => {
      if (element.dataset.space) await openSpace(element.dataset.space);
      else if (element.dataset.file) await selectEntry(element.dataset.file);
      else if (element.dataset.path !== undefined) { rememberDirectory(); state.path = state.path.slice(0, Number(element.dataset.path)); state.query = ''; await loadFiles(); }
      else await action(element.dataset.action, element);
    }, element);
  });
  document.addEventListener('submit', async event => {
    if (event.target.id !== 'login-form') return;
    event.preventDefault();
    if (state.loading) return;
    const password = new FormData(event.target).get('password');
    const submit = event.target.querySelector('button[type="submit"]'); submit.disabled = true; state.loading = true;
    submit.textContent = t('loading'); submit.setAttribute('aria-busy', 'true');
    try {
      await api('/api/login', { password });
      event.target.reset(); state.authenticated = true; state.epoch++; state.loginError = ''; state.loading = true; render();
      await loadSpaces();
    } catch (error) {
      state.loading = false;
      if (state.authenticated) { state.error = error.code; render(); }
      else {
        state.loginError = error.code;
        const errorNode = document.getElementById('login-error'); if (errorNode) errorNode.textContent = t(error.code);
        submit.disabled = false; submit.textContent = t('enter'); submit.removeAttribute('aria-busy'); document.getElementById('password')?.focus();
      }
    }
  });
  document.addEventListener('input', event => {
    if (event.target.id !== 'search') return;
    state.query = event.target.value;
    if (virtualList) { virtualList.resizeAnchor = null; virtualList.anchor = null; }
    refreshFiles();
    if (state.query) void loadMore();
  });
  window.addEventListener('scroll', () => {
    if (scrollFrame) return;
    scrollFrame = requestAnimationFrame(() => {
      scrollFrame = 0;
      const view = virtualList;
      if (!view || window.innerWidth !== view.viewportWidth) return;
      if (view.adjustedScroll !== null && Math.abs(window.scrollY - view.adjustedScroll) < 1) {
        view.adjustedScroll = null; drawFiles(false); return;
      }
      view.adjustedScroll = null; view.resizeAnchor = null; drawFiles();
      loadNearEnd();
    });
  }, { passive: true });
  window.addEventListener('resize', () => {
    const view = virtualList;
    if (!view || state.view || !view.list.isConnected) return;
    view.resizeAnchor ||= view.anchor;
    view.viewportWidth = window.innerWidth;
    const width = view.list.getBoundingClientRect().width;
    if (width && view.width && Math.abs(width - view.width) > 0.5) view.heights.clear();
    if (width) view.width = width;
    for (const [id, row] of view.rows) {
      const height = row.getBoundingClientRect().height;
      if (height) view.heights.set(id, height);
    }
    view.offsetsDirty = true; view.gap = null;
    redrawAnchored(view);
  });
  document.addEventListener('keydown', event => {
    if (event.key === 'Escape' && !panel.open && foreground?.cancellable) { state.intent++; abandonOperation(); return; }
    if (event.key !== 'Tab' || !virtualList || state.view) return;
    const row = event.target.closest?.('.file-row');
    if (!row) return;
    const controls = Array.from(row.querySelectorAll('button:not(:disabled)'));
    if (event.target !== controls[event.shiftKey ? 0 : controls.length - 1]) return;
    const index = virtualList.entries.findIndex(entry => entry.id === row.dataset.virtualId);
    const nextIndex = index + (event.shiftKey ? -1 : 1), next = virtualList.entries[nextIndex];
    if (!next || virtualList.rows.has(next.id)) return;
    event.preventDefault();
    const gap = parseFloat(getComputedStyle(virtualList.list).getPropertyValue('--file-gap')) || 0;
    let offset = 0;
    for (let i = 0; i < nextIndex; i++) offset += (virtualList.heights.get(virtualList.entries[i].id) || virtualList.estimatedHeight) + gap;
    window.scrollTo(0, window.scrollY + virtualList.list.getBoundingClientRect().top + offset);
    drawFiles();
    const buttons = virtualList.rows.get(next.id)?.querySelectorAll('button:not(:disabled)');
    buttons?.[event.shiftKey ? buttons.length - 1 : 0]?.focus({ preventScroll: true });
  });
  const dismissPanel = () => { state.intent++; abandonOperation(); closePanel(); };
  panel.addEventListener('cancel', event => { event.preventDefault(); dismissPanel(); });
  panel.addEventListener('click', event => { if (event.target === panel) { const rect = panel.getBoundingClientRect(); if (event.clientX < rect.left || event.clientX > rect.right || event.clientY < rect.top || event.clientY > rect.bottom) dismissPanel(); } });
  window.addEventListener('popstate', () => {
    state.intent++; abandonOperation(); closePanel(); if (state.view) clearFocusView(); state.selected = null; state.plan = null;
  });
  window.addEventListener('online', () => { void poll(); });
  window.addEventListener('offline', () => { state.connected = false; updateConnection(); });
  document.addEventListener('visibilitychange', () => { if (!document.hidden) void poll(); });
  setInterval(() => { void poll(); }, 5000);
  if (history.state?.pkpLANView) history.replaceState(null, '', location.pathname + location.search);
  render();
  void (async () => {
    try {
      const session = await api('/api/session');
      state.loading = false;
      if (session.authenticated) { state.authenticated = true; await loadSpaces(); } else render();
    } catch (error) { state.loading = false; state.loginError = error.code; render(); }
  })();
}

if (typeof document !== 'undefined') startApp();
