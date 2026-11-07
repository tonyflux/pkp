const h = value => String(value ?? '').replace(/[&<>"']/g, character => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' })[character]);
const chevron = '<svg viewBox="0 0 24 24" aria-hidden="true"><path d="m7 10 5 5 5-5"/></svg>';
const check = '<svg viewBox="0 0 24 24" aria-hidden="true"><path d="m5 12 4 4L19 6"/></svg>';

// Keep the menu within its owner so it remains visible in fullscreen and native dialogs.
export function selectHTML({ id, label, value, options, className = '' }) {
  const current = options.find(option => String(option.value) === String(value));
  return `<div class="select-menu ${h(className)}" data-select-menu="${h(id)}"><button type="button" class="select-face" role="combobox" aria-label="${h(label)}" aria-haspopup="listbox" aria-expanded="false" aria-controls="${h(id)}-list"><span data-select-value>${h(current?.label ?? value)}</span>${chevron}</button><div id="${h(id)}-list" class="select-list" role="listbox" aria-label="${h(label)}" hidden><div class="select-rows">${options.map(option => `<button type="button" role="option" data-select-option="${h(option.value)}" aria-selected="${String(option.value) === String(value)}" tabindex="-1"${option.disabled ? ' disabled' : ''}><span>${h(option.label)}</span>${String(option.value) === String(value) ? check : ''}</button>`).join('')}</div></div></div>`;
}

export function mountSelect(root, { onChange, signal, onOpenChange } = {}) {
  const face = root.querySelector('.select-face');
  const list = root.querySelector('.select-list');
  const options = [...list.querySelectorAll('[role=option]')];
  const rows = list.querySelector('.select-rows');
  const controller = new AbortController();
  const listen = (target, event, handler, extra = {}) => target.addEventListener(event, handler, { ...extra, signal: controller.signal });
  let active = -1;
  const selected = () => options.findIndex(option => option.getAttribute('aria-selected') === 'true');
  function focus(index) {
    if (!options[index] || options[index].disabled) return;
    active = index;
    options.forEach((option, i) => { option.tabIndex = i === index ? 0 : -1; });
    options[index].focus({ preventScroll: true });
    options[index].scrollIntoView?.({ block: 'nearest' });
  }
  function hide(refocus = false) {
    if (list.hidden) return;
    list.hidden = true; root.classList.remove('is-open'); face.setAttribute('aria-expanded', 'false');
    onOpenChange?.(false);
    if (refocus && face.isConnected) face.focus({ preventScroll: true });
  }
  function place() {
    if (list.hidden) return;
    if (!face.isConnected) { destroy(); return; }
    const margin = 8, gap = 6, box = face.getBoundingClientRect();
    const viewport = window.visualViewport;
    const leftEdge = viewport?.offsetLeft ?? 0, topEdge = viewport?.offsetTop ?? 0;
    const width = viewport?.width ?? window.innerWidth, height = viewport?.height ?? window.innerHeight;
    const bottomEdge = topEdge + height, rightEdge = leftEdge + width;
    const availableAbove = Math.max(0, box.top - topEdge - margin - gap);
    const availableBelow = Math.max(0, bottomEdge - box.bottom - margin - gap);
    const desiredHeight = rows.scrollHeight + 2;
    const above = availableBelow < desiredHeight && availableAbove > availableBelow;
    const maxHeight = above ? availableAbove : availableBelow;
    list.style.maxWidth = `${Math.max(0, width - margin * 2)}px`;
    list.style.minWidth = `${Math.min(Math.max(144, box.width), width - margin * 2)}px`;
    list.style.maxHeight = `${maxHeight}px`;
    const actualHeight = Math.min(desiredHeight, maxHeight);
    list.style.left = `${Math.max(leftEdge + margin, Math.min(box.left, rightEdge - margin - list.offsetWidth))}px`;
    list.style.top = `${above ? box.top - gap - actualHeight : box.bottom + gap}px`;
    list.dataset.placement = above ? 'above' : 'below';
  }
  function show() {
    if (!list.hidden || face.disabled) return;
    list.hidden = false; root.classList.add('is-open'); face.setAttribute('aria-expanded', 'true');
    place();
    const current = selected();
    const initial = current >= 0 && !options[current].disabled ? current : options.findIndex(option => !option.disabled);
    if (initial >= 0) focus(initial);
    onOpenChange?.(true);
  }
  function setValue(value) {
    for (const option of options) {
      const current = option.dataset.selectOption === String(value);
      option.setAttribute('aria-selected', String(current));
      option.querySelector('svg')?.remove();
      if (current) option.insertAdjacentHTML('beforeend', check);
      if (current) face.querySelector('[data-select-value]').textContent = option.querySelector('span').textContent;
    }
  }
  function choose(option) {
    if (!option || option.disabled) return;
    const value = option.dataset.selectOption, previous = options[selected()]?.dataset.selectOption;
    setValue(value); hide(true);
    if (value !== previous) onChange?.(value);
  }
  function move(from, step) {
    for (let index = from + step; index >= 0 && index < options.length; index += step) {
      if (!options[index].disabled) { focus(index); return; }
    }
  }
  listen(face, 'click', () => { if (list.hidden) show(); else hide(true); });
  listen(face, 'keydown', event => {
    if (!['ArrowDown', 'ArrowUp', 'Enter', ' '].includes(event.key)) return;
    event.preventDefault(); event.stopPropagation(); show();
  });
  listen(list, 'click', event => {
    const option = event.target.closest('[role=option]');
    if (option && list.contains(option)) { event.stopPropagation(); choose(option); }
  });
  listen(list, 'focusin', event => { const index = options.indexOf(event.target); if (index >= 0) active = index; });
  listen(list, 'keydown', event => {
    switch (event.key) {
      case 'ArrowDown': move(active, 1); break;
      case 'ArrowUp': move(active, -1); break;
      case 'Home': move(-1, 1); break;
      case 'End': move(options.length, -1); break;
      case 'Enter': case ' ': choose(options[active]); break;
      case 'Escape': case 'Tab': hide(true); break;
      default: return;
    }
    event.preventDefault(); event.stopPropagation();
  });
  listen(document, 'pointerdown', event => { if (!root.isConnected) destroy(); else if (!root.contains(event.target)) hide(); });
  listen(document, 'scroll', place, { capture: true, passive: true });
  listen(window, 'resize', place, { passive: true });
  if (window.visualViewport) {
    listen(window.visualViewport, 'resize', place, { passive: true });
    listen(window.visualViewport, 'scroll', place, { passive: true });
  }
  function destroy() { hide(); controller.abort(); }
  if (signal?.aborted) destroy();
  else signal?.addEventListener('abort', destroy, { once: true });
  return { setValue, hide, destroy, get open() { return !list.hidden; } };
}
