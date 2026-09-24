'use strict';

// Wails injects window.go (bindings) and window.runtime (events) at load.
const api = () => window.go.main.App;
const $ = (sel, root = document) => root.querySelector(sel);

const shell = $('#shell');
const card = $('#card');
const panel = $('#panel');

const state = {
  open: null,        // 'input' | 'output' | 'hide' | 'settings' | null
  busy: false,       // a window animation is running
  dragging: false,   // user holds the volume slider
  lastHtml: '',      // last rendered panel markup, to skip useless re-renders
};

const esc = (s) => String(s ?? '').replace(/[&<>"']/g, (c) => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' }[c]));
const icon = (id, cls = 'ico') => `<svg class="${cls}"><use href="#i-${id}"/></svg>`;
const wait = (ms) => new Promise((r) => setTimeout(r, ms));

// ---------------------------------------------------------------- feedback

let toastTimer;
function toast(msg, isErr = false) {
  const t = $('#toast');
  t.textContent = msg;
  t.classList.toggle('err', isErr);
  t.classList.add('show');
  clearTimeout(toastTimer);
  toastTimer = setTimeout(() => t.classList.remove('show'), isErr ? 4500 : 2600);
}

let brandTimer;
function brandMessage(msg) {
  const brand = $('.brand');
  $('#brandMsg').textContent = msg;
  brand.classList.add('msg');
  clearTimeout(brandTimer);
  brandTimer = setTimeout(() => brand.classList.remove('msg'), 3200);
}

function notify(msg, isErr = false) {
  if (state.open) toast(msg, isErr);
  else brandMessage(msg);
}

async function call(fn, ...args) {
  try {
    return await api()[fn](...args);
  } catch (e) {
    notify(String(e?.message ?? e), true);
    throw e;
  }
}

// ---------------------------------------------------------------- panels

// Window heights in CSS px (the width is fixed at 560 in app.go).
const PANEL_H = 640;
const DIALOG_H = 300;

function fadeOutPanel(ms) {
  const a = panel.animate(
    [{ opacity: 1, transform: 'none' }, { opacity: 0, transform: 'translateY(-6px)' }],
    { duration: ms, easing: 'cubic-bezier(.4, 0, 1, 1)', fill: 'forwards' },
  );
  return a.finished;
}

async function openPanel(name) {
  if (state.busy) return;
  if (state.open === name) return closePanel();

  state.busy = true;
  try {
    const html = await renderView(name);
    const first = !state.open;
    state.open = name;
    state.lastHtml = html;
    setActiveButton(name);

    if (first) {
      panel.innerHTML = html;
      // Let the content start rising while the window is still growing.
      const grow = api().Expand(PANEL_H);
      setTimeout(() => card.classList.add('open'), 110);
      await grow;
    } else {
      // Cross-fade between panels while expanded.
      await fadeOutPanel(150);
      card.classList.remove('open');
      panel.innerHTML = html;
      panel.scrollTop = 0;
      panel.getAnimations().forEach((a) => a.cancel());
      void panel.offsetWidth; // restart the entrance animation
      card.classList.add('open');
    }
    bindView(name);
  } catch (e) {
    notify(String(e?.message ?? e), true);
  } finally {
    state.busy = false;
  }
}

async function closePanel() {
  if (!state.open || state.busy) return;
  state.busy = true;
  try {
    await fadeOutPanel(170);
    card.classList.remove('open');
    state.open = null;
    setActiveButton(null);
    await api().Collapse();
    panel.getAnimations().forEach((a) => a.cancel());
    panel.innerHTML = '';
  } finally {
    state.busy = false;
  }
}

function setActiveButton(name) {
  document.querySelectorAll('[data-panel]').forEach((b) => b.classList.toggle('active', b.dataset.panel === name));
}

function renderView(name) {
  switch (name) {
    case 'input':
    case 'output': return renderFlow(name);
    case 'hide': return renderHide();
    case 'settings': return renderSettings();
  }
}

function bindView(name) {
  if (name === 'input' || name === 'output') bindFlow(name);
  else if (name === 'hide') bindHide();
  else if (name === 'settings') bindSettings();
}

// Re-render the open panel in place (no entrance animation) when data changed.
async function refresh(force = false) {
  const name = state.open;
  if (!name || state.busy || state.dragging || name === 'settings') return;
  const html = await renderView(name);
  if (name !== state.open) return;
  if (!force && html === state.lastHtml) return;
  state.lastHtml = html;
  const scroll = panel.scrollTop;
  panel.innerHTML = html;
  panel.querySelector('.view')?.classList.add('static');
  panel.scrollTop = scroll;
  bindView(name);
}

// ---------------------------------------------------------------- input / output

const FLOW = {
  input: {
    icon: 'mic', title: 'Микрофон', sub: 'Устройство ввода по умолчанию',
    lockText: 'Если Windows или программа сменит микрофон — вернём выбранный',
    volText: 'Если что-то изменит уровень микрофона (Discord, игры, авто-усиление) — вернём его',
  },
  output: {
    icon: 'speaker', title: 'Вывод звука', sub: 'Колонки, наушники и другие устройства вывода',
    lockText: 'Если Windows или программа сменит устройство вывода — вернём выбранное',
    volText: 'Если что-то изменит громкость — вернём заданное значение',
  },
};

function devTitle(d) {
  return d.description || d.name;
}

async function renderFlow(flow) {
  const v = await api().GetFlow(flow);
  const f = FLOW[flow];
  const p = v.pref;
  const hasPinned = !!p.deviceId;
  const pinnedPresent = v.devices.some((d) => d.id === p.deviceId && d.state === 'active');

  const rows = v.devices.map((d) => {
    const sel = d.id === p.deviceId;
    const badges = [];
    if (d.isDefault) badges.push('<span class="badge sys">Системный</span>');
    if (d.isDefaultComm && !d.isDefault) badges.push('<span class="badge">Связь</span>');
    if (d.state !== 'active') badges.push('<span class="badge warn">Не подключено</span>');
    const vol = d.volume >= 0 ? d.volume : 0;
    return `
      <button class="dev ${sel ? 'sel' : ''} ${d.state !== 'active' ? 'off' : ''}" data-id="${esc(d.id)}" data-name="${esc(d.name)}" ${d.state !== 'active' ? 'disabled' : ''}>
        <span class="radio">${icon('check', '')}</span>
        <span class="row-text">
          <div class="dev-name">${esc(devTitle(d))}</div>
          <div class="dev-sub">${esc(d.adapter || d.name)}</div>
        </span>
        <span class="badges">${badges.join('')}</span>
        <span class="meter" title="Громкость ${vol}%"><i style="width:${vol}%"></i></span>
      </button>`;
  }).join('');

  const warn = hasPinned && !pinnedPresent
    ? `<div class="note warn" style="margin-top:10px">${icon('shield')}<span>Выбранное устройство «${esc(p.deviceName)}» сейчас не подключено. Как только оно появится — станет устройством по умолчанию.</span></div>`
    : '';

  return `
    <div class="view" data-flow="${flow}">
      <div class="vh">
        <div class="vh-icon">${icon(f.icon)}</div>
        <div><h2>${f.title}</h2><p>${f.sub}</p></div>
      </div>
      <div class="group">
        <div class="row">
          ${icon('lock', 'row-icon')}
          <div class="row-text"><b>Удерживать выбранное устройство</b><small>${f.lockText}</small></div>
          <label class="switch"><input type="checkbox" id="lockDevice" ${p.lockDevice ? 'checked' : ''} ${hasPinned ? '' : 'disabled'}><span></span></label>
        </div>
      </div>
      <div>
        <div class="label"><span>Устройства</span><span class="count">${hasPinned ? '' : 'Выберите устройство'}</span></div>
        <div class="group">${rows || '<div class="empty">Нет активных устройств</div>'}</div>
        ${warn}
      </div>
      <div>
        <div class="label"><span>Громкость по умолчанию</span></div>
        <div class="group">
          <div class="vol">
            ${icon(f.icon, 'row-icon')}
            <input type="range" id="vol" min="0" max="100" step="1" value="${p.volume}" style="--p:${p.volume}%">
            <span class="vol-val" id="volVal">${p.volume}%</span>
          </div>
          <div class="row">
            ${icon('lock', 'row-icon')}
            <div class="row-text"><b>Фиксировать громкость</b><small>${f.volText}</small></div>
            <label class="switch"><input type="checkbox" id="lockVolume" ${p.lockVolume ? 'checked' : ''}><span></span></label>
          </div>
        </div>
      </div>
    </div>`;
}

function bindFlow(flow) {
  panel.querySelectorAll('.dev').forEach((el) => {
    el.addEventListener('click', async () => {
      await call('SetPreferred', flow, el.dataset.id, el.dataset.name);
      notify(`По умолчанию: ${el.querySelector('.dev-name').textContent}`);
      await refresh(true);
      updatePins();
    });
  });

  $('#lockDevice', panel)?.addEventListener('change', async (e) => {
    await call('SetLockDevice', flow, e.target.checked);
    notify(e.target.checked ? 'Устройство закреплено' : 'Закрепление снято');
    updatePins();
  });

  $('#lockVolume', panel)?.addEventListener('change', async (e) => {
    await call('SetLockVolume', flow, e.target.checked);
    notify(e.target.checked ? 'Громкость зафиксирована' : 'Фиксация громкости снята');
    updatePins();
  });

  const range = $('#vol', panel);
  const out = $('#volVal', panel);
  let pending;
  range.addEventListener('pointerdown', () => { state.dragging = true; });
  const release = () => { state.dragging = false; };
  range.addEventListener('pointerup', release);
  range.addEventListener('pointercancel', release);
  range.addEventListener('input', () => {
    const v = +range.value;
    range.style.setProperty('--p', v + '%');
    out.textContent = v + '%';
    clearTimeout(pending);
    pending = setTimeout(() => call('SetVolume', flow, v), 40);
  });
  range.addEventListener('wheel', (e) => {
    e.preventDefault();
    range.value = Math.max(0, Math.min(100, +range.value + (e.deltaY < 0 ? 2 : -2)));
    range.dispatchEvent(new Event('input'));
  }, { passive: false });
}

// ---------------------------------------------------------------- hide devices

const STATE_TEXT = { active: 'Активно', disabled: 'Скрыто', unplugged: 'Не подключено' };

async function renderHide() {
  const v = await api().GetAllDevices();
  const pinned = new Set(v.pinned);
  const keep = new Set(v.preferred);
  const hiddenByApp = new Set(v.hiddenIds);
  // "Hide all" keeps the system defaults and the device chosen in this app.
  const hideable = (d) => d.state !== 'disabled' && !d.isDefault && !d.isDefaultComm && !keep.has(d.id);

  const section = (title, flow, list, ic) => {
    const rows = list.map((d) => {
      const hidden = d.state === 'disabled';
      const lock = pinned.has(d.id);
      return `
        <div class="hrow ${hidden ? 'hidden-dev' : ''}">
          ${icon(ic, 'row-icon')}
          <div class="row-text">
            <div class="dev-name">${esc(devTitle(d))}</div>
            <div class="dev-sub">${esc(d.adapter || d.name)}</div>
          </div>
          <span class="badges">
            ${d.isDefault ? '<span class="badge sys">Системный</span>' : ''}
            ${hidden ? `<span class="badge dis">${hiddenByApp.has(d.id) ? 'Скрыто' : 'Отключено'}</span>` : ''}
            ${d.state === 'unplugged' ? '<span class="badge">Не подключено</span>' : ''}
          </span>
          <label class="switch" title="${lock ? 'Это закреплённое устройство — сначала снимите закрепление' : hidden ? 'Показать' : 'Скрыть'}">
            <input type="checkbox" data-id="${esc(d.id)}" data-name="${esc(d.name)}" data-flow="${d.flow}" ${hidden ? '' : 'checked'} ${lock && !hidden ? 'disabled' : ''}><span></span>
          </label>
        </div>`;
    }).join('');
    const shown = list.filter((d) => d.state !== 'disabled').length;
    const canHide = list.filter(hideable).length;
    return `
      <div>
        <div class="label">
          <span>${title} <span class="count">· видно ${shown} из ${list.length}</span></span>
          <button class="link-btn" data-hideall="${flow}" ${canHide ? '' : 'disabled'} title="Скрыть всё, кроме системного и выбранного устройства">${icon('eye-off')}Скрыть все</button>
        </div>
        <div class="group">${rows || '<div class="empty">Нет устройств</div>'}</div>
      </div>`;
  };

  const n = v.hiddenIds.length;
  return `
    <div class="view">
      <div class="vh">
        <div class="vh-icon">${icon('eye-off')}</div>
        <div><h2>Скрыть устройства</h2><p>Выключенные устройства пропадут во всех программах и играх</p></div>
      </div>
      <div class="note">${icon('shield')}<span>Скрытие работает как «Отключить» в панели звука Windows: устройство не удаляется и в любой момент включается обратно этим же переключателем.</span></div>
      ${section('Вывод', 'output', v.output, 'speaker')}
      ${section('Ввод', 'input', v.input, 'mic')}
      <div class="actions">
        <button class="btn primary" id="restoreAll" ${n ? '' : 'disabled'}>${icon('restore')}Вернуть все скрытые${n ? ` (${n})` : ''}</button>
      </div>
    </div>`;
}

function bindHide() {
  panel.querySelectorAll('.hrow input').forEach((el) => {
    el.addEventListener('change', async () => {
      const hide = !el.checked;
      try {
        await call('SetDeviceHidden', el.dataset.id, el.dataset.name, el.dataset.flow, hide);
        notify(`${hide ? 'Скрыто' : 'Возвращено'}: ${el.dataset.name}`);
      } catch {
        el.checked = !el.checked;
      }
      await refresh(true);
    });
  });
  panel.querySelectorAll('[data-hideall]').forEach((el) => {
    el.addEventListener('click', async () => {
      el.disabled = true;
      try {
        const n = await call('HideAllDevices', el.dataset.hideall);
        notify(n ? `Скрыто устройств: ${n}` : 'Нечего скрывать');
      } catch { /* already reported */ }
      await refresh(true);
    });
  });
  $('#restoreAll', panel)?.addEventListener('click', async () => {
    try {
      const n = await call('RestoreAllHidden');
      notify(`Возвращено устройств: ${n}`);
    } catch { /* already reported */ }
    await refresh(true);
  });
}

// ---------------------------------------------------------------- settings

async function renderSettings() {
  const v = await api().GetSettings();
  const s = v.settings;
  const sw = (key, on, title, text, disabled = false) => `
    <div class="row">
      <div class="row-text"><b>${title}</b><small>${text}</small></div>
      <label class="switch"><input type="checkbox" data-key="${key}" ${on ? 'checked' : ''} ${disabled ? 'disabled' : ''}><span></span></label>
    </div>`;

  return `
    <div class="view">
      <div class="vh">
        <div class="vh-icon">${icon('gear')}</div>
        <div><h2>Настройки</h2><p>Запуск и поведение приложения</p></div>
      </div>
      <div class="group">
        ${sw('autorun', v.autorun, 'Автозапуск с Windows', 'Через Планировщик заданий с наивысшими правами — без запроса UAC при входе', !v.elevated && !v.autorun)}
        ${sw('startMinimized', s.startMinimized, 'Запускать свёрнутым в трей', 'При автозапуске панель не появляется — только значок в трее')}
        ${sw('includeComms', s.includeComms, 'Также устройство связи', 'Закреплять выбранное устройство и для звонков (Discord, Teams, Zoom)')}
        ${sw('resetAppDevices', s.resetAppDevices, 'Для всех приложений', 'Сбрасывать устройства, назначенные отдельным программам в параметрах Windows, — все будут использовать выбранное')}
        ${sw('alwaysOnTop', s.alwaysOnTop, 'Поверх всех окон', 'Панель не прячется за другими окнами')}
      </div>
      <div>
        <div class="label"><span>Состояние</span></div>
        <div class="group">
          <div class="row">
            ${icon('shield', 'row-icon')}
            <div class="row-text"><b>Права администратора</b><small>${v.elevated ? 'Приложение запущено с повышенными правами' : 'Запущено без прав администратора — автозапуск недоступен'}</small></div>
            <span class="kv"><span class="dotst ${v.elevated ? '' : 'bad'}"></span>${v.elevated ? 'Есть' : 'Нет'}</span>
          </div>
        </div>
      </div>
      <div>
        <div class="label"><span>О программе</span></div>
        <div class="group">
          <div class="about">
            <img src="img/logo-mark.png" alt="">
            <div class="row-text"><b>AudioManager<span style="color:var(--accent)">Pro</span></b><small>Версия ${esc(v.version)}</small></div>
            <button class="btn" id="checkUpd" ${v.version === 'dev' ? 'disabled title="Локальная сборка — обновления только для релизов с GitHub"' : ''}>${icon('download')}Проверить обновления</button>
          </div>
        </div>
      </div>
      <div class="actions">
        <button class="btn danger" id="quitBtn">${icon('close')}Выйти из приложения</button>
      </div>
    </div>`;
}

function bindSettings() {
  panel.querySelectorAll('input[data-key]').forEach((el) => {
    el.addEventListener('change', async () => {
      try {
        await call('SetSetting', el.dataset.key, el.checked);
        notify('Сохранено');
      } catch {
        el.checked = !el.checked;
      }
    });
  });
  $('#quitBtn', panel).addEventListener('click', confirmClose);
  $('#checkUpd', panel).addEventListener('click', (e) => checkUpdatesNow(e.currentTarget));
}

// ---------------------------------------------------------------- bar

async function updatePins() {
  for (const flow of ['input', 'output']) {
    try {
      const v = await api().GetFlow(flow);
      $(`#pin-${flow}`).classList.toggle('on', !!(v.pref.deviceId && v.pref.lockDevice) || v.pref.lockVolume);
    } catch { /* ignore */ }
  }
}

async function hideToTray() {
  shell.classList.remove('shown');
  await wait(220);
  await api().HideToTray();
  if (modal.open) {
    setModal(false);
    // The dialog grew the collapsed bar; shrink it back while hidden.
    if (modal.grewWindow) await api().Collapse();
  }
}

async function quit() {
  shell.classList.remove('shown');
  await wait(220);
  api().Quit();
}

// ---------------------------------------------------------------- dialogs

const modal = { open: false, grewWindow: false, kind: null };

function setModal(on) {
  modal.open = on;
  if (!on) modal.kind = null;
  $('#modal').classList.toggle('show', on);
  $('#modal').setAttribute('aria-hidden', String(!on));
}

// openDialog shows a dialog, first growing the window when only the bar is visible.
async function openDialog(kind, html, bind) {
  if (modal.open || state.busy) return;
  modal.kind = kind;
  $('#dialog').innerHTML = html;
  bind($('#dialog'));
  modal.grewWindow = !state.open;
  if (modal.grewWindow) {
    state.busy = true;
    const grow = api().Expand(DIALOG_H);
    setTimeout(() => setModal(true), 90);
    await grow;
    state.busy = false;
  } else {
    setModal(true);
  }
  $('#dialog .btn.primary')?.focus();
}

async function closeDialog() {
  if (!modal.open || modal.kind === 'updating') return;
  setModal(false);
  if (modal.grewWindow) {
    await wait(200);
    await api().Collapse();
  }
}

const dialogHTML = ({ icon: ic, tone = '', title, text, actions }) => `
  <div class="dlg-icon ${tone}">${icon(ic)}</div>
  <h3 id="dlgTitle">${title}</h3>
  <p id="dlgText">${text}</p>
  <div class="dlg-actions">${actions}</div>`;

function confirmClose() {
  return openDialog('close', dialogHTML({
    icon: 'power', tone: 'danger',
    title: 'Закрыть AudioManagerPro?',
    text: 'Автоматическая установка звука перестанет работать: устройства и громкость не будут возвращаться, пока приложение закрыто.',
    actions: `
      <button class="btn danger" data-act="quit">Да, закрыть приложение</button>
      <button class="btn primary" data-act="tray">Нет, свернуть в трей</button>`,
  }), (d) => {
    $('[data-act=quit]', d).addEventListener('click', quit);
    $('[data-act=tray]', d).addEventListener('click', hideToTray);
  });
}

// ---------------------------------------------------------------- updates

const updates = { info: null, current: '', dismissed: '' };

function onUpdateAvailable(info) {
  updates.info = info;
  if (shell.classList.contains('shown')) offerUpdate();
}

function offerUpdate() {
  const info = updates.info;
  if (!info || updates.dismissed === info.version || modal.open) return;
  return openDialog('update', dialogHTML({
    icon: 'download',
    title: `Доступна версия ${esc(info.version)}`,
    text: `Сейчас установлена ${esc(updates.current || 'старая версия')}. Обновление скачается с GitHub, после чего приложение перезапустится — настройки сохранятся.`,
    actions: `
      <button class="btn" data-act="later">Позже</button>
      <button class="btn primary" data-act="install">${icon('download')}Обновить</button>`,
  }), (d) => {
    $('[data-act=later]', d).addEventListener('click', () => { updates.dismissed = info.version; closeDialog(); });
    $('[data-act=install]', d).addEventListener('click', () => installUpdate(d));
  });
}

async function installUpdate(d) {
  modal.kind = 'updating';
  const actions = $('.dlg-actions', d);
  const saved = actions.innerHTML;
  actions.innerHTML = '<div class="progress"><i id="updBar"></i></div><span class="progress-val" id="updVal">0%</span>';
  try {
    await api().InstallUpdate(); // the app quits and restarts on success
    $('#dlgText').textContent = 'Перезапуск…';
  } catch (e) {
    modal.kind = 'update';
    $('#dlgText').textContent = `Не удалось обновить: ${e?.message ?? e}`;
    actions.innerHTML = saved;
    $('[data-act=later]', d).addEventListener('click', closeDialog);
    $('[data-act=install]', d).addEventListener('click', () => installUpdate(d));
  }
}

function onUpdateProgress(pct) {
  const bar = $('#updBar');
  if (!bar) return;
  bar.style.width = pct + '%';
  $('#updVal').textContent = pct + '%';
}

async function checkUpdatesNow(btn) {
  btn.disabled = true;
  const label = btn.innerHTML;
  btn.textContent = 'Проверка…';
  try {
    const info = await call('CheckUpdate');
    if (info) {
      updates.info = info;
      updates.dismissed = '';
      await offerUpdate();
    } else {
      notify('Установлена последняя версия');
    }
  } catch { /* already reported */ }
  btn.innerHTML = label;
  btn.disabled = false;
}

function appear() {
  shell.classList.remove('shown');
  void shell.offsetWidth;
  requestAnimationFrame(() => shell.classList.add('shown'));
  refresh();
  if (updates.info) setTimeout(offerUpdate, 450);
}

function init() {
  document.querySelectorAll('[data-panel]').forEach((b) => b.addEventListener('click', () => openPanel(b.dataset.panel)));
  $('#btnMin').addEventListener('click', hideToTray);
  $('#btnClose').addEventListener('click', confirmClose);
  $('#modal').addEventListener('click', (e) => { if (e.target.id === 'modal') closeDialog(); });
  document.addEventListener('keydown', (e) => {
    if (e.key !== 'Escape') return;
    if (modal.open) closeDialog();
    else closePanel();
  });
  document.addEventListener('contextmenu', (e) => e.preventDefault());

  window.runtime.EventsOn('appear', appear);
  window.runtime.EventsOn('request-hide', hideToTray);
  window.runtime.EventsOn('notice', (msg) => { notify(msg); refresh(); });
  window.runtime.EventsOn('update-available', onUpdateAvailable);
  window.runtime.EventsOn('update-progress', onUpdateProgress);
  api().GetSettings().then((v) => { updates.current = v.version; }).catch(() => {});

  setInterval(() => { if (!document.hidden) refresh(); }, 2000);
  updatePins();
  appear();
}

(function start() {
  if (window.go?.main?.App && window.runtime) init();
  else setTimeout(start, 20);
})();
