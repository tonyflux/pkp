import { translate } from './locales.js';

const paths = {
  previous: '<path d="m15 5-7 7 7 7"/>',
  next: '<path d="m9 5 7 7-7 7"/>',
  close: '<path d="m6 6 12 12M18 6 6 18"/>',
  download: '<path d="M12 3v12m-5-5 5 5 5-5M4 16v5h16v-5"/>'
};
const icon = name => `<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">${paths[name]}</svg>`;
const isImage = entry => /^image\/(jpeg|png|gif|webp|avif|bmp)$/i.test(entry?.mime || '');

// Sources and download permission belong to the caller; only the selected image is loaded.
export function mountGallery({ container, entry, entries = [], language = 'en', onSelect, onDownload }) {
  const t = key => translate(language, key);
  const controller = new AbortController();
  const listen = (target, event, handler, options = {}) => target.addEventListener(event, handler, { ...options, signal: controller.signal });
  const parentId = entry?.parentId;
  const seen = new Set();
  const images = entries.filter(item => {
    if (!item.ready || !isImage(item) || item.parentId !== parentId || seen.has(item.id)) return false;
    seen.add(item.id);
    return true;
  });
  let current = entry?.ready && isImage(entry) && entry.source ? entry : null;
  let index = current ? images.findIndex(item => item.id === current.id) : -1;
  // A validated initial image may not be in the current loaded list (for example, after a refresh).
  if (current && index < 0) { images.unshift(current); index = 0; }
  let targetIndex = index, request = 0, destroyed = false, selecting = false;
  let image = null, imageState = current ? 'loading' : 'empty', failure = '', touch = null;

  container.innerHTML = `
    <header class="gallery-header">
      <h2 id="panel-title" tabindex="-1"></h2>
      <span class="gallery-count" aria-live="polite" aria-atomic="true"></span>
      <button type="button" class="icon-button gallery-close" data-action="close">${icon('close')}</button>
    </header>
    <div class="gallery-stage">
      <div class="gallery-image"></div>
      <p class="gallery-status" role="status" aria-live="polite" aria-atomic="true"></p>
      <button type="button" class="icon-button gallery-previous" data-gallery-action="previous">${icon('previous')}</button>
      <button type="button" class="icon-button gallery-next" data-gallery-action="next">${icon('next')}</button>
    </div>
    <footer class="gallery-footer">
      <button type="button" class="secondary gallery-download" data-gallery-action="download">${icon('download')}<span></span></button>
    </footer>`;
  container.setAttribute('aria-labelledby', 'panel-title');
  const heading = container.querySelector('#panel-title');
  const count = container.querySelector('.gallery-count');
  const close = container.querySelector('.gallery-close');
  const stage = container.querySelector('.gallery-stage');
  const imageHost = container.querySelector('.gallery-image');
  const status = container.querySelector('.gallery-status');
  const previous = container.querySelector('.gallery-previous');
  const next = container.querySelector('.gallery-next');
  const download = container.querySelector('.gallery-download');

  function render() {
    if (destroyed) return;
    heading.textContent = current?.name || t('preview');
    heading.title = current?.name || '';
    count.textContent = `${index < 0 ? 0 : index + 1} / ${images.length}`;
    for (const [button, key] of [[close, 'close'], [previous, 'galleryPrevious'], [next, 'galleryNext']]) {
      button.setAttribute('aria-label', t(key));
      button.title = t(key);
    }
    previous.disabled = targetIndex <= 0 || !onSelect;
    next.disabled = targetIndex < 0 || targetIndex >= images.length - 1 || !onSelect;
    download.querySelector('span').textContent = t('galleryDownload');
    download.disabled = !current || selecting || !onDownload;
    const key = failure || (selecting || imageState === 'loading' ? 'loadingMedia' : imageState === 'error' ? 'galleryLoadFailed' : imageState === 'empty' ? 'galleryEmpty' : '');
    status.textContent = key ? t(key) : '';
    status.hidden = !key;
    status.dataset.state = failure || imageState === 'error' ? 'error' : selecting ? 'loading' : imageState;
    status.setAttribute('role', status.dataset.state === 'error' ? 'alert' : 'status');
    status.setAttribute('aria-live', status.dataset.state === 'error' ? 'assertive' : 'polite');
    stage.setAttribute('aria-busy', String(selecting || imageState === 'loading'));
  }
  function removeImage() {
    if (!image) return;
    image.onload = null;
    image.onerror = null;
    image.removeAttribute('src');
    image.remove();
    image = null;
  }
  function loadImage() {
    removeImage();
    if (!current) return;
    const active = document.createElement('img');
    image = active;
    imageState = 'loading';
    active.alt = current.name;
    active.decoding = 'async';
    active.draggable = false;
    active.hidden = true;
    active.onload = () => {
      if (destroyed || image !== active) return;
      imageState = 'ready';
      active.hidden = false;
      render();
    };
    active.onerror = () => {
      if (destroyed || image !== active) return;
      imageState = 'error';
      active.hidden = true;
      render();
    };
    imageHost.replaceChildren(active);
    active.src = current.source;
  }
  async function navigate(direction) {
    const candidateIndex = targetIndex + direction;
    if (destroyed || !onSelect || candidateIndex < 0 || candidateIndex >= images.length) return;
    const candidate = images[candidateIndex];
    const ticket = ++request;
    targetIndex = candidateIndex;
    selecting = true;
    failure = '';
    render();
    try {
      const validated = await onSelect(candidate.id);
      if (destroyed || ticket !== request || !validated) return;
      if (validated.id !== candidate.id || validated.parentId !== parentId || !isImage(validated)) {
        throw Object.assign(new Error('invalid_request'), { code: 'invalid_request' });
      }
      if (!validated.ready || !validated.source) throw Object.assign(new Error('not_ready'), { code: 'not_ready' });
      current = validated;
      index = targetIndex = candidateIndex;
      selecting = false;
      loadImage();
      render();
    } catch (error) {
      if (destroyed || ticket !== request) return;
      targetIndex = index;
      selecting = false;
      failure = error?.code || 'internal';
      render();
    }
  }
  listen(container, 'click', event => {
    const control = event.target.closest('button');
    if (!control || control.disabled) return;
    if (control.dataset.action === 'close') { destroy(); return; }
    const action = control.dataset.galleryAction;
    if (action === 'previous') void navigate(-1);
    if (action === 'next') void navigate(1);
    if (action === 'download' && current && !selecting) {
      const ticket = request;
      Promise.resolve().then(() => {
        if (!destroyed && ticket === request) return onDownload(current);
      }).catch(error => {
        if (destroyed || ticket !== request) return;
        failure = error?.code || 'internal';
        render();
      });
    }
  });
  listen(container, 'keydown', event => {
    if (event.defaultPrevented || event.altKey || event.ctrlKey || event.metaKey || event.shiftKey || event.isComposing || event.target.closest('input,textarea,select,[contenteditable]:not([contenteditable="false"]),[role="textbox"],[role="slider"]')) return;
    if (event.key !== 'ArrowLeft' && event.key !== 'ArrowRight') return;
    event.preventDefault();
    void navigate(event.key === 'ArrowLeft' ? -1 : 1);
  });
  const zoomed = () => (window.visualViewport?.scale || 1) > 1;
  listen(stage, 'touchstart', event => {
    touch = null;
    if (event.touches.length !== 1 || zoomed() || event.target.closest('button')) return;
    const point = event.touches[0];
    touch = { id: point.identifier, x: point.clientX, y: point.clientY };
  }, { passive: true });
  listen(stage, 'touchmove', event => {
    if (!touch) return;
    const point = event.touches[0];
    if (event.touches.length !== 1 || point.identifier !== touch.id || zoomed() || Math.abs(point.clientY - touch.y) > Math.max(16, Math.abs(point.clientX - touch.x))) touch = null;
  }, { passive: true });
  listen(stage, 'touchend', event => {
    const start = touch;
    touch = null;
    if (!start || event.touches.length || zoomed()) return;
    const point = [...event.changedTouches].find(item => item.identifier === start.id);
    if (!point) return;
    const dx = point.clientX - start.x, dy = point.clientY - start.y;
    if (Math.abs(dx) >= 48 && Math.abs(dx) > Math.abs(dy) * 1.5) void navigate(dx < 0 ? 1 : -1);
  }, { passive: true });
  listen(stage, 'touchcancel', () => { touch = null; }, { passive: true });
  listen(container, 'close', destroy);
  listen(container, 'cancel', destroy);

  function destroy() {
    if (destroyed) return;
    destroyed = true;
    request++;
    touch = null;
    controller.abort();
    removeImage();
  }
  loadImage();
  render();
  return {
    destroy,
    setLanguage(value) { language = value; render(); }
  };
}
