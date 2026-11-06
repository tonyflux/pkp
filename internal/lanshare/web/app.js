import { languages, detectLanguage, translate } from './locales.js';
import { mountPlayer } from './player.js';

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

export function supportsMedia(info, capabilities) {
  const videoKey = { h264: 'h264', hevc: info.bitDepth > 8 ? 'hevc10' : 'hevc', vp9: 'vp9', av1: 'av1' }[info.videoCodec];
  const audioKey = { aac: 'aac', opus: 'opus' }[info.audioCodec];
  const container = /webm/.test(info.mime || info.container || '') ? capabilities.webm : /mp4|mov/.test(info.mime || info.container || '') && capabilities.mp4;
  return Boolean(container && (!info.videoCodec || (videoKey && capabilities[videoKey] && capabilities.videoSupported !== false)) &&
    (!info.audioCodec || (audioKey && capabilities[audioKey] && capabilities.audioSupported !== false)) && (!info.hdr || capabilities.hdr));
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
  lock: '<rect x="5" y="10" width="14" height="11" rx="3"/><path d="M8 10V7a4 4 0 0 1 8 0v3M12 14v3"/>'
};
const icon = name => `<svg viewBox="0 0 24 24" aria-hidden="true">${paths[name] || paths.file}</svg>`;

export function startApp() {
  const root = document.getElementById('app');
  const panel = document.getElementById('panel');
  let preference;
  try { preference = localStorage.getItem('pkp-lan-language'); } catch { /* Private browsing may disable storage. */ }
  const state = {
    language: languages[preference] ? preference : detectLanguage(navigator.languages || [navigator.language]),
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
  const t = key => translate(state.language, key);
  const h = escapeHTML;
  const sourceName = space => t(space.kind === 'local' ? 'local' : 'cloud');
  const formatSize = size => {
    if (!Number.isFinite(size) || size < 0) return t('unknown');
    const units = ['B', 'KB', 'MB', 'GB', 'TB'];
    const order = size > 0 ? Math.min(4, Math.floor(Math.log(size) / Math.log(1024))) : 0;
    return `${new Intl.NumberFormat(state.language, { maximumFractionDigits: order ? 1 : 0 }).format(size / 1024 ** order)} ${units[order]}`;
  };
  const formatDate = value => {
    const date = new Date(value);
    return value && !Number.isNaN(date.valueOf()) ? new Intl.DateTimeFormat(state.language, { dateStyle: 'medium', timeStyle: 'short' }).format(date) : t('unknown');
  };
  const button = (action, label, className = 'secondary', attrs = '') => `<button type="button" class="${className}" data-action="${action}" ${attrs}>${label === 'play' || label === 'playWhenReady' ? icon('play') : ''}${h(t(label))}</button>`;
  const iconButton = (action, label, name, attrs = '') => `<button type="button" class="icon-button" data-action="${action}" aria-label="${h(t(label))}" title="${h(t(label))}" ${attrs}>${icon(name)}</button>`;
  const languageSelect = () => `<label class="sr-only" for="language">${h(t('language'))}</label><select id="language" data-language>${Object.entries(languages).map(([key, name]) => `<option value="${key}" ${key === state.language ? 'selected' : ''}>${name}</option>`).join('')}</select>`;
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
    if (pending?.element?.isConnected) {
      pending.element.disabled = false;
      pending.element.removeAttribute('aria-busy');
    }
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
    state.path = []; state.plan = null; state.capabilities = null; state.error = ''; state.detailError = '';
    state.loading = false; state.loginError = error; state.taskSnapshot = ''; state.tab = 'files';
    if (history.state?.pkpLANView) history.replaceState(null, '', location.pathname + location.search);
    closePanel(); clearFocusView(false); document.getElementById('notice').hidden = true; render();
  }
  async function api(path, body, signal = foreground?.controller.signal) {
    const epoch = state.epoch, intent = state.intent;
    const controller = new AbortController();
    // Verifying an existing large desktop download reads its checksum once.
    const timer = setTimeout(() => controller.abort(), path.startsWith('/api/file?') ? 600000 : 15000);
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
  function render() {
    document.documentElement.lang = state.language;
    document.title = `PKP · ${t('title')}`;
    document.body.classList.toggle('authenticated', state.authenticated);
    if (!state.authenticated) {
      root.innerHTML = `<main class="gate"><header class="gate-header">${brand()}${languageSelect()}</header><div class="gate-layout"><section class="gate-art"><h1>${h(t('welcome'))}</h1><p class="muted">${h(t('gateIntro'))}</p></section><section class="gate-form"><p class="eyebrow">${h(t('title'))}</p><h2>${h(t('loginTitle'))}</h2><p class="muted">${h(t('passwordHint'))}</p><form id="login-form"><label for="password">${h(t('password'))}</label><input id="password" name="password" type="password" autocomplete="current-password" required maxlength="1024" autocapitalize="none" enterkeyhint="go" aria-describedby="login-error"><p id="login-error" class="form-error error" role="alert">${h(state.loginError ? t(state.loginError) : '')}</p><button class="primary" type="submit" ${state.loading ? 'disabled' : ''}>${h(t(state.loading ? 'loading' : 'enter'))}</button></form><p class="note warning">${h(t('http'))}</p></section></div></main>`;
      const password = document.getElementById('password');
      const visibility = document.createElement('button');
      visibility.type = 'button'; visibility.textContent = t('showPassword');
      visibility.setAttribute('aria-controls', 'password'); visibility.setAttribute('aria-pressed', 'false');
      visibility.addEventListener('click', () => {
        const visible = password.type === 'password';
        password.type = visible ? 'text' : 'password';
        visibility.textContent = t(visible ? 'hidePassword' : 'showPassword');
        visibility.setAttribute('aria-pressed', String(visible));
      });
      password.after(visibility);
      return;
    }
    root.innerHTML = `<div id="library-view" class="portal"><header class="header"><div class="header-brand">${brand()}<span data-connection>${connectionHTML()}</span></div><div class="header-actions">${languageSelect()}${button('tasks', 'computerTasks', 'secondary desktop-tasks', `${state.space ? '' : 'disabled'} aria-current="${state.tab === 'tasks' ? 'page' : 'false'}"`)}<span class="task-count" data-task-count></span>${iconButton('logout', 'logout', 'logout')}</div></header><div class="layout"><nav class="sidebar" aria-label="${h(t('spaces'))}"><h2>${h(t('spaces'))}</h2>${button('home', 'spaces', 'space-button')}${state.spaces.map(spaceButton).join('')}<p class="sidebar-foot">${icon('lock')}${h(t('readOnlyShort'))}</p></nav><main class="library ${state.space ? '' : 'space-home'}">${state.tab === 'tasks' && state.space ? tasksPageHTML() : libraryHTML()}</main></div><nav class="bottom-nav" aria-label="${h(t('title'))}">${button('files', 'sharedFiles', '', `aria-current="${state.tab === 'files' ? 'page' : 'false'}"`)}${button('tasks', 'computerTasks', '', `${state.space ? '' : 'disabled'} aria-current="${state.tab === 'tasks' ? 'page' : 'false'}"`)}</nav></div>`;
    updateTaskCounts();
  }
  function spaceButton(space) {
    return `<button class="space-button" type="button" data-space="${h(space.id)}" aria-current="${state.space?.id === space.id}">${icon(space.kind === 'local' ? 'local' : 'cloud')}<span><strong>${h(space.name)}</strong><small>${h(sourceName(space))}</small></span></button>`;
  }
  function libraryHTML() {
    if (!state.space) return `<section class="space-heading"><p class="eyebrow">${h(t('readonly'))}</p><h1>${h(t('chooseSpace'))}</h1></section>${state.error ? errorHTML(state.error) : ''}${state.loading ? `<p class="empty" role="status">${h(t('loading'))}</p>` : state.spaces.length ? `<div class="space-cards">${state.spaces.map(spaceButton).join('')}</div>` : `<p class="empty">${h(t('noSpaces'))}</p>`}<p class="footer-note">${h(t('http'))}</p>`;
    const folder = state.path.at(-1)?.name || state.space.name;
    return `<section class="space-heading"><p class="eyebrow">${h(sourceName(state.space))}</p><div class="heading-title"><h1>${h(folder)}</h1><span class="readonly-badge">${h(t('readOnlyShort'))}</span></div></section><div class="toolbar"><nav class="breadcrumbs" aria-label="${h(t('folder'))}"><button type="button" data-path="0">${h(state.space.name)}</button>${state.path.map((part, index) => `<span aria-hidden="true">/</span><button type="button" data-path="${index + 1}">${h(part.name)}</button>`).join('')}</nav><label class="search"><span class="sr-only">${h(t('search'))}</span><input id="search" type="search" placeholder="${h(t('search'))}" value="${h(state.query)}" autocomplete="off"></label>${iconButton('refresh', 'refresh', 'refresh')}</div><div id="preparation-summary">${summaryHTML()}</div><div class="file-table"><div class="file-table-heading" aria-hidden="true"><span>${h(t('file'))}</span><span>${h(t('type'))} · ${h(t('size'))}</span><span>${h(t('readyShort'))}</span><span>${h(t('modified'))}</span><span></span></div><div id="file-list" class="file-list" aria-busy="${state.loading}">${filesHTML()}</div></div>`;
  }
  function errorHTML(code) { return `<p class="note error error-box" role="alert">${h(t(code))}</p>`; }
  function fileArt(entry, detail = false) {
    if (isImage(entry) && entry.ready) return `<img src="${h(contentURL(state.space.id, entry.id))}" alt="${detail ? h(entry.name) : ''}" loading="lazy" decoding="async">`;
    return icon(entry.isDir ? 'folder' : isImage(entry) ? 'image' : isMedia(entry) ? 'video' : 'file');
  }
  function filesHTML() {
    if (state.loading) return `<p class="empty" role="status">${h(t('loading'))}</p>`;
    if (state.error) return `<div class="empty">${errorHTML(state.error)}${button('refresh', 'refresh')}</div>`;
    const query = state.query.toLocaleLowerCase(state.language);
    const entries = state.entries.filter(entry => entry.name.toLocaleLowerCase(state.language).includes(query));
    if (!entries.length) return `<p class="empty">${h(t(state.query ? 'noMatches' : 'empty'))}</p>`;
    return entries.map(entry => {
      const task = state.tasks.find(item => item.fileId === entry.id && activeTask(item));
      const ready = entry.ready || entry.compatibleReady;
      const status = entry.isDir ? 'folder' : task ? task.state : entry.compatibleReady ? 'compatibleShort' : entry.ready ? 'readyShort' : entry.needsVerification ? 'verifyShort' : 'notReadyShort';
      const label = entry.isDir ? 'openFolder' : task ? 'viewProgress' : !ready ? 'prepareShort' : isMedia(entry) ? 'play' : isImage(entry) ? 'preview' : 'download';
      const unavailable = !entry.isDir && !ready && !task && !entry.needsVerification && !state.space.allowPrepare;
      const metadata = entry.isDir ? t('folder') : formatSize(entry.size);
      const extension = entry.isDir ? '' : entry.name.split('.').length > 1 ? entry.name.split('.').at(-1).toUpperCase() : t('file');
      return `<article class="file-row"><button type="button" class="file-main" data-file="${h(entry.id)}" title="${h(entry.name)}"><span class="file-cover">${fileArt(entry)}</span><span class="file-copy"><strong>${h(entry.name)}</strong><small class="file-mobile-meta">${h(metadata)}<span class="availability ${ready ? 'ready' : ''}">${h(t(status))}</span></small></span></button><span class="file-meta">${h(extension)}${extension ? ' · ' : ''}${h(metadata)}</span><span class="file-status"><span class="availability ${ready ? 'ready' : ''}">${h(t(status))}</span></span><time class="file-modified">${entry.modified ? h(formatDate(entry.modified)) : '—'}</time><div class="file-actions">${button('entry-primary', label, `file-primary ${ready && isMedia(entry) ? 'primary' : 'secondary'}`, `data-file-id="${h(entry.id)}" ${unavailable ? 'disabled' : ''}`)}${entry.isDir ? '' : iconButton('entry-details', 'more', 'more', `data-file-id="${h(entry.id)}"`)}</div></article>`;
    }).join('');
  }
  function summaryHTML() {
    const task = state.tasks.find(activeTask);
    if (!task) return '';
    const percent = taskPercent(task);
    return `<button type="button" class="preparation-summary" data-action="task-open" data-task-id="${h(task.id)}">${icon('refresh')}<span class="summary-copy"><strong>${h(task.name)}</strong><small>${h(t(task.state))}${percent === null ? '' : ` · ${h(Math.round(percent))}%`}</small>${percent === null ? '' : `<progress value="${percent}" max="100" aria-label="${h(t(task.state))}"></progress>`}</span>${icon('back')}</button>`;
  }
  function updateTaskCounts() {
    const count = state.tasks.filter(activeTask).length;
    document.querySelectorAll('[data-task-count]').forEach(node => { node.textContent = count ? String(count) : ''; node.hidden = !count; });
  }
  function refreshLibrary() {
    const list = document.getElementById('file-list');
    if (list) {
      const focused = document.activeElement;
      const id = focused?.dataset.file || focused?.dataset.fileId;
      const action = focused?.dataset.action;
      list.innerHTML = filesHTML();
      if (id && focused && !focused.isConnected) {
        const target = list.querySelector(action ? `[data-action="${CSS.escape(action)}"][data-file-id="${CSS.escape(id)}"]` : `[data-file="${CSS.escape(id)}"]`);
        target?.focus({ preventScroll: true });
      }
    }
    const summary = document.getElementById('preparation-summary');
    if (summary) summary.innerHTML = summaryHTML();
    updateTaskCounts();
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
    return `<article class="task" data-task="${h(task.id)}"><strong>${h(task.name)}</strong><p class="task-state">${h(t(stateKey))}</p><p class="task-meta">${h(t(task.kind === 'compatible' ? 'compatible' : 'original'))}</p>${percent !== null && activeTask(task) ? `<progress value="${percent}" max="100" aria-label="${h(t(stateKey))}"></progress><p class="task-meta">${h(Math.round(percent))}%${task.state === 'downloading' ? ` · ${h(formatSize(task.done))} / ${h(formatSize(task.total))}` : ''}</p>` : ''}${task.error ? errorHTML(task.error) : ''}<div class="task-actions">${button('task-open', activeTask(task) ? 'viewProgress' : 'details', 'secondary', `data-task-id="${h(task.id)}"`)}${activeTask(task) ? button('cancel-task', 'cancelTask', 'secondary', `data-task-id="${h(task.id)}"`) : removableTask(task) ? button('remove-task', 'removeTaskRecord', 'secondary', `data-task-id="${h(task.id)}"`) : ''}</div></article>`;
  }
  function tasksPageHTML() {
    return `<section class="tasks-heading"><div><p class="eyebrow">${h(state.space.name)}</p><h1 tabindex="-1">${h(t('computerTasks'))}</h1></div>${button('clear-tasks', 'clearTaskRecords', 'secondary', state.tasks.some(removableTask) ? '' : 'disabled')}</section><p class="tasks-intro muted">${h(t('twoStages'))}</p><p class="note">${h(t('taskRecordsHint'))}</p><div id="task-list" class="task-list">${state.tasks.length ? state.tasks.map(taskHTML).join('') : `<p class="empty">${h(t('noTasks'))}</p>`}</div><p class="note">${h(t('cancelHint'))}</p>`;
  }
  function refreshTaskList(fallbackID = '') {
    const list = document.getElementById('task-list');
    if (!list) return;
    const focused = list.contains(document.activeElement) ? document.activeElement : null;
    const id = focused?.dataset.taskId, action = focused?.dataset.action;
    list.innerHTML = state.tasks.length ? state.tasks.map(taskHTML).join('') : `<p class="empty">${h(t('noTasks'))}</p>`;
    const clear = root.querySelector('[data-action="clear-tasks"]');
    if (clear) clear.disabled = !state.tasks.some(removableTask);
    if (focused || fallbackID) {
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
    const percent = task && activeTask(task) ? taskPercent(task) : null;
    const stateKey = ready ? 'preparationComplete' : task?.state === 'ready' ? 'not_ready' : task?.state || 'notReadyShort';
    return `<p class="preparation-phase" role="status">${h(t(stateKey))}</p>${task ? `<p class="muted">${h(t(task.kind === 'compatible' ? 'compatible' : 'original'))}</p>` : ''}${percent === null ? '' : `<progress value="${percent}" max="100" aria-label="${h(t(stateKey))}"></progress><p class="preparation-percent">${h(Math.round(percent))}%${task.state === 'downloading' ? ` · ${h(formatSize(task.done))} / ${h(formatSize(task.total))}` : ''}</p>`}${task?.error ? errorHTML(task.error) : ''}<p class="preparation-message muted">${h(t(ready ? 'twoStages' : 'preparationWait'))}</p><div class="preparation-actions">${button('preparation-play', isMedia(entry) ? ready ? 'play' : 'playWhenReady' : 'download', 'primary', ready ? '' : 'disabled')}${!ready && task && ['held', 'failed'].includes(task.state) ? button('retry-preparation', 'retryPreparation', 'secondary', state.space.allowPrepare ? '' : 'disabled') : ''}${button('preparation-tasks', 'computerTasks')}${task && activeTask(task) ? button('cancel-task', 'cancelTask', 'secondary', `data-task-id="${h(task.id)}"`) : ''}</div>`;
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
    status.innerHTML = preparationStatusHTML();
    const original = document.getElementById('original-note');
    if (original) original.innerHTML = `<span>${h(t(state.selected.ready ? 'originalAvailable' : 'originalUnchanged'))}</span>${state.selected.ready ? button('download', 'download', 'secondary') : ''}`;
    const task = state.tasks.find(item => item.id === state.prepareTask);
    const ready = task?.kind === 'compatible' ? state.selected.compatibleReady : state.selected.ready;
    document.querySelector('.preparation-card h1').textContent = t(ready ? 'preparationComplete' : 'preparingTitle');
    if (focused) status.querySelector(`[data-action="${CSS.escape(focused)}"]:not(:disabled)`)?.focus({ preventScroll: true });
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
    if (!panel.open) focusBeforeDialog = document.activeElement;
    destroyMedia(); state.dialog = kind; panel.className = kind === 'image' ? 'image-dialog' : '';
    panel.innerHTML = panelHTML(title, body);
    if (!panel.open) panel.showModal();
    panel.querySelector('#panel-title').focus();
  }
  function updateDetails() {
    const focus = document.activeElement;
    const action = focus?.dataset.action;
    if (state.dialog === 'detail') panel.innerHTML = panelHTML('details', detailHTML());
    if (action && !focus.isConnected) {
      Array.from(panel.querySelectorAll('[data-action]')).find(node => node.dataset.action === action)?.focus({ preventScroll: true });
    }
    if (focus?.id === 'panel-title' && !focus.isConnected && panel.open) panel.querySelector('#panel-title')?.focus();
  }
  async function loadSpaces() {
    const epoch = state.epoch;
    const spaces = await api('/api/spaces');
    if (!state.authenticated || epoch !== state.epoch) return;
    state.spaces = spaces || []; state.loading = false; state.error = ''; render();
  }
  async function loadFiles() {
    if (!state.space) return;
    const request = ++state.request;
    state.intent++; closePanel();
    const space = state.space.id;
    const parent = state.path.at(-1)?.id || '';
    state.loading = true; state.error = ''; state.entries = []; state.selected = null; state.detailError = ''; state.plan = null;
    render();
    try {
      const result = await api('/api/files?' + new URLSearchParams({ space, parent }));
      if (request !== state.request || !state.authenticated) return;
      state.entries = (result.entries || []).sort((a, b) => Number(b.isDir) - Number(a.isDir) || a.name.localeCompare(b.name, state.language));
    } catch (error) {
      if (request !== state.request || !state.authenticated) return;
      state.error = error.code;
    }
    if (request === state.request && state.authenticated) { state.loading = false; render(); }
  }
  async function openSpace(id) {
    const space = state.spaces.find(item => item.id === id);
    if (!space) return;
    closePanel(); state.tab = 'files'; state.space = space; state.path = []; state.query = ''; state.tasks = []; state.taskSnapshot = '';
    await loadFiles(); void poll();
  }
  async function freshEntry(signal = foreground?.controller.signal) {
    if (!state.space || !state.selected) return null;
    const space = state.space.id, id = state.selected.id, epoch = state.epoch, intent = state.intent;
    const current = () => state.authenticated && state.epoch === epoch && state.intent === intent && state.space?.id === space && state.selected?.id === id;
    let entry;
    try { entry = await api('/api/file?' + new URLSearchParams({ space, id }), undefined, signal); }
    catch (error) { if (!current()) return null; throw error; }
    if (!current()) return null;
    state.selected = entry; state.detailError = '';
    state.entries = state.entries.map(item => item.id === id ? entry : item);
    return entry;
  }
  async function selectEntry(id, details = false) {
    const entry = state.entries.find(item => item.id === id);
    if (!entry) return;
    state.intent++; closePanel();
    if (state.view) clearFocusView();
    if (entry.isDir) { state.path.push({ id: entry.id, name: entry.name }); state.query = ''; await loadFiles(); return; }
    state.selected = entry; state.detailError = ''; state.plan = null;
    const current = await freshEntry();
    if (!current) return;
    const task = state.tasks.find(item => item.fileId === id && activeTask(item));
    if (details) showPanel('detail', 'details', detailHTML());
    else if (task) showPreparation(task);
    else if (!current.ready && !current.compatibleReady) {
      if (state.space.allowPrepare) await prepareOriginal();
      else showPanel('detail', 'details', detailHTML());
    } else if (isMedia(current)) await playSelection();
    else if (isImage(current)) await showImage();
    else await download();
    refreshLibrary();
    if (!state.view && history.state?.pkpLANView) history.replaceState(null, '', location.pathname + location.search);
  }
  async function playSelection() {
    if (state.selected?.compatibleReady) {
      try { await checkCompatible(); return; }
      catch (error) { if (error.code !== 'unsupported_media') throw error; }
    }
    await planPlayback();
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
  async function download() {
    const entry = await freshEntry();
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
  async function prepareOriginal() {
    const entry = await freshEntry();
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
  async function planPlayback() {
    const entry = await freshEntry();
    if (!entry) return;
    if (!entry.ready) throw { code: 'not_ready' };
    const space = state.space.id, file = entry.id, epoch = state.epoch, intent = state.intent;
    const info = await api('/api/media?' + new URLSearchParams({ space, id: file }));
    const capabilities = await probeCapabilities(info);
    if (!state.authenticated || epoch !== state.epoch || intent !== state.intent || state.selected?.id !== file || state.space?.id !== space) return;
    const plan = await api('/api/plan', { spaceId: space, fileId: file, capabilities });
    if (!state.authenticated || epoch !== state.epoch || intent !== state.intent || state.selected?.id !== file || state.space?.id !== space) return;
    state.plan = plan; state.capabilities = capabilities;
    if (plan.kind === 'direct') {
      await showPlayer('original');
    } else if (plan.kind === 'unsupported') {
      showPanel('unsupported', 'details', `<h3 class="detail-title">${h(entry.name)}</h3>${errorHTML(plan.reason === 'hdr_unsupported' ? 'hdr_unsupported' : 'unsupported')}${warningsHTML(plan)}<div class="dialog-actions">${button('back', 'back')}${button('download', 'download', 'primary')}</div>`);
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
  async function checkCompatible() {
    const entry = await freshEntry();
    if (!entry) return;
    if (!entry.compatibleReady) throw { code: 'not_ready' };
    const space = state.space.id, id = entry.id, epoch = state.epoch, intent = state.intent;
    const info = await api('/api/media?' + new URLSearchParams({ space, id, variant: 'compatible' }));
    const caps = await probeCapabilities(info);
    if (!state.authenticated || epoch !== state.epoch || intent !== state.intent || state.space?.id !== space || state.selected?.id !== id) return;
    if (!supportsMedia(info, caps)) throw { code: 'unsupported_media' };
    const sourcePlan = await api('/api/plan', { spaceId: space, fileId: id, capabilities: caps });
    if (!state.authenticated || epoch !== state.epoch || intent !== state.intent || state.space?.id !== space || state.selected?.id !== id) return;
    state.plan = { ...sourcePlan, info };
    await showPlayer('compatible');
  }
  async function showPlayer(variant) {
    const entry = await freshEntry();
    if (!entry) return;
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
  async function showImage() {
    const entry = await freshEntry();
    if (!entry) return;
    if (!entry.ready) throw { code: 'not_ready' };
    showPanel('image', 'preview', `<h3 class="detail-title">${h(entry.name)}</h3><div class="image-stage"><img src="${h(contentURL(state.space.id, entry.id))}" alt="${h(entry.name)}"></div><div id="image-error" role="alert"></div><div class="dialog-actions">${button('download', 'download')}</div>`);
    panel.querySelector('img').addEventListener('error', () => {
      const error = document.getElementById('image-error');
      if (error) error.innerHTML = errorHTML('source_unavailable');
    }, { once: true });
  }
  async function poll() {
    if (pollBusy || document.hidden || !state.authenticated || state.loading) return;
    pollBusy = true;
    const epoch = state.epoch, intent = state.intent, spaceID = state.space?.id, request = state.request, taskRevision = state.taskRevision;
    try {
      const session = await api('/api/session', undefined, null);
      if (!state.authenticated || epoch !== state.epoch || request !== state.request) return;
      if (!session.authenticated) { forgetSession(); return; }
      const spaces = await api('/api/spaces', undefined, null);
      if (!state.authenticated || epoch !== state.epoch || request !== state.request) return;
      const spacesChanged = JSON.stringify(state.spaces) !== JSON.stringify(spaces || []);
      state.spaces = spaces || [];
      if (spaceID && state.space?.id === spaceID) {
        const current = state.spaces.find(space => space.id === spaceID);
        if (!current) {
          let reason = 'forbidden';
          try { await api('/api/files?' + new URLSearchParams({ space: spaceID, parent: state.path.at(-1)?.id || '' }), undefined, null); }
          catch (error) { reason = error.code; }
          if (!state.authenticated || epoch !== state.epoch || request !== state.request) return;
          backToLibrary();
          state.request++; state.entries = []; state.tasks = []; state.selected = null; state.space = null; state.tab = 'files'; state.error = reason;
          render(); return;
        }
        const permissionChanged = state.space.allowPrepare !== current.allowPrepare;
        state.space = current;
        const result = await api('/api/files?' + new URLSearchParams({ space: spaceID, parent: state.path.at(-1)?.id || '' }), undefined, null);
        if (!state.authenticated || epoch !== state.epoch || request !== state.request) return;
        const entries = (result.entries || []).sort((a, b) => Number(b.isDir) - Number(a.isDir) || a.name.localeCompare(b.name, state.language));
        const filesChanged = state.error || JSON.stringify(entries) !== JSON.stringify(state.entries);
        state.entries = entries; state.error = '';
        const tasks = await api('/api/tasks?' + new URLSearchParams({ space: spaceID }), undefined, null);
        if (!state.authenticated || epoch !== state.epoch || request !== state.request || taskRevision !== state.taskRevision) return;
        const snapshot = JSON.stringify(tasks);
        const changed = snapshot !== state.taskSnapshot;
        state.tasks = tasks || []; state.taskSnapshot = snapshot;
        if (state.selected) {
          const old = JSON.stringify(state.selected), previousError = state.detailError;
          await freshEntry(null);
          if (!state.authenticated || epoch !== state.epoch || request !== state.request || taskRevision !== state.taskRevision) return;
          if (changed || permissionChanged || previousError || old !== JSON.stringify(state.selected)) {
            updateDetails(); updatePreparation();
          }
        }
        if (filesChanged || changed || permissionChanged) refreshLibrary();
        if (permissionChanged && !current.allowPrepare && state.dialog === 'confirm') showPanel('detail', 'details', detailHTML());
        if (changed) refreshTaskList();
      } else if (!state.space && spacesChanged) render();
      updateConnection();
    } catch (error) {
      if (error.code !== 'unauthorized' && epoch === state.epoch && intent === state.intent && request === state.request && taskRevision === state.taskRevision) {
        if (state.space && ['forbidden', 'source_unavailable'].includes(error.code)) {
          state.error = error.code; state.entries = [];
          refreshLibrary();
        }
        handleError(error, Boolean(state.selected));
      }
    } finally { pollBusy = false; }
  }
  async function action(name, element) {
    switch (name) {
      case 'back': dismissPanel(); if (state.view) backToLibrary(); return;
      case 'logout': await api('/api/logout', {}); forgetSession(''); return;
      case 'home': closePanel(); state.intent++; state.request++; state.tab = 'files'; state.space = null; state.selected = null; state.entries = []; state.tasks = []; state.error = ''; state.loading = true; render(); await loadSpaces(); return;
      case 'refresh': await loadSpaces(); if (state.space) await loadFiles(); return;
      case 'files': state.tab = 'files'; render(); return;
      case 'tasks': if (state.space) { state.tab = 'tasks'; render(); void poll(); } return;
      case 'preparation-tasks': backToLibrary(); state.tab = 'tasks'; render(); return;
      case 'entry-primary': await selectEntry(element.dataset.fileId); return;
      case 'entry-details': await selectEntry(element.dataset.fileId, true); return;
      case 'task-open': await openTask(element.dataset.taskId); return;
      case 'download': await download(); return;
      case 'preview': await showImage(); return;
      case 'play': await planPlayback(); return;
      case 'play-compatible': await checkCompatible(); return;
      case 'preparation-play': {
        if (!state.selected) return;
        if (isMedia(state.selected)) await playSelection();
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
    if (state.busy) return;
    const epoch = state.epoch;
    const owner = { controller: new AbortController(), element };
    foreground = owner;
    const focusFile = element?.dataset.file || element?.dataset.fileId;
    const focusAction = element?.dataset.action;
    try {
      state.busy = true;
      if (element) { element.disabled = true; element.setAttribute('aria-busy', 'true'); }
      await operation();
    } catch (error) { if (foreground === owner && epoch === state.epoch) handleError(error, Boolean(state.selected)); }
    finally {
      if (foreground === owner) {
        foreground = null; state.busy = false;
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
      }
    }
  }
  document.addEventListener('click', event => {
    const element = event.target.closest('button');
    if (!element || element.disabled || (!element.dataset.action && !element.dataset.space && !element.dataset.file && element.dataset.path === undefined)) return;
    if (element.dataset.action === 'close') { dismissPanel(); return; }
    if (element.dataset.action === 'return-library') { backToLibrary(); return; }
    void runOperation(async () => {
      if (element.dataset.space) await openSpace(element.dataset.space);
      else if (element.dataset.file) await selectEntry(element.dataset.file);
      else if (element.dataset.path !== undefined) { state.path = state.path.slice(0, Number(element.dataset.path)); state.query = ''; await loadFiles(); }
      else await action(element.dataset.action, element);
    }, element);
  });
  document.addEventListener('submit', async event => {
    if (event.target.id !== 'login-form') return;
    event.preventDefault();
    if (state.loading) return;
    const password = new FormData(event.target).get('password');
    const submit = event.target.querySelector('button[type="submit"]'); submit.disabled = true; state.loading = true;
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
        submit.disabled = false; document.getElementById('password')?.focus();
      }
    }
  });
  document.addEventListener('input', event => {
    if (event.target.id !== 'search') return;
    state.query = event.target.value;
    document.getElementById('file-list').innerHTML = filesHTML();
  });
  document.addEventListener('change', event => {
    if (!event.target.matches('[data-language]')) return;
    state.language = event.target.value;
    try { localStorage.setItem('pkp-lan-language', state.language); } catch { /* Optional preference only. */ }
    const dialog = state.dialog;
    render();
    if (dialog === 'detail') showPanel('detail', 'details', detailHTML());
    else if (dialog === 'confirm') showConfirmation();
    else if (panel.open) closePanel();
    document.getElementById('language')?.focus();
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
