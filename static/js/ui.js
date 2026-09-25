// UI 组件与工具：toast / 弹窗 / 抽屉 / 菜单 / 开关 / 复选 / 格式化
(function () {
  'use strict';

  const UI = {};

  // ---------- HTML 转义 ----------
  UI.esc = function (s) {
    if (s === null || s === undefined) return '';
    return String(s).replace(/[&<>"']/g, c => ({
      '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;',
    }[c]));
  };

  // ---------- 格式化 ----------
  UI.fmtBytes = function (n) {
    if (n === null || n === undefined || isNaN(+n)) return '-';
    n = +n;
    const u = ['B', 'KB', 'MB', 'GB', 'TB'];
    let i = 0;
    while (n >= 1024 && i < u.length - 1) { n /= 1024; i++; }
    return (i === 0 ? n : n.toFixed(1)) + ' ' + u[i];
  };
  UI.fmtRate = function (bps) {
    if (!bps || +bps === 0) return '-';
    return UI.fmtBytes(+bps) + '/s';
  };
  UI.fmtTime = function (s) {
    if (!s) return '-';
    const d = new Date(s);
    if (isNaN(d.getTime())) return s;
    const p = x => String(x).padStart(2, '0');
    return `${d.getFullYear()}-${p(d.getMonth() + 1)}-${p(d.getDate())} ${p(d.getHours())}:${p(d.getMinutes())}`;
  };
  UI.fmtPct = function (v) {
    const n = +v;
    if (!isFinite(n)) return '-';
    return n.toFixed(1) + '%';
  };

  // ---------- Toast ----------
  const ICONS = {
    ok: '<path d="M5 13l4 4 10-11"/>',
    error: '<path d="M12 4l9 16H3z"/><path d="M12 10v4M12 17h.01"/>',
    warn: '<path d="M12 4l9 16H3z"/><path d="M12 10v4M12 17h.01"/>',
    info: '<circle cx="12" cy="12" r="9"/><path d="M12 11v5M12 8h.01"/>',
  };
  UI.toast = function (msg, type = 'info', ms = 3200) {
    const box = document.getElementById('toasts');
    if (!box) return;
    const el = document.createElement('div');
    el.className = 'toast ' + type;
    el.innerHTML = `<svg viewBox="0 0 24 24">${ICONS[type] || ICONS.info}</svg><span></span>`;
    el.querySelector('span').textContent = msg;
    box.appendChild(el);
    setTimeout(() => {
      el.classList.add('out');
      setTimeout(() => el.remove(), 260);
    }, ms);
  };

  // ---------- 弹窗 ----------
  UI.modal = function ({ title, body, foot, wide, onClose }) {
    const mask = document.createElement('div');
    mask.className = 'modal-mask';
    mask.innerHTML = `
      <div class="modal${wide ? ' wide' : ''}">
        <div class="modal-head"><h3></h3>
          <button type="button" class="icon-btn" data-x><svg><use href="#i-close"/></svg></button>
        </div>
        <div class="modal-body"></div>
        <div class="modal-foot"></div>
      </div>`;
    mask.querySelector('h3').textContent = title || '';
    if (typeof body === 'string') mask.querySelector('.modal-body').innerHTML = body;
    else if (body) mask.querySelector('.modal-body').appendChild(body);
    const footEl = mask.querySelector('.modal-foot');
    (foot || []).forEach(b => {
      if (!b) return;
      const btn = document.createElement('button');
      btn.className = 'btn ' + (b.cls || '');
      btn.type = 'button';
      btn.innerHTML = (b.icon ? `<svg><use href="#${b.icon}"/></svg>` : '') + `<span></span>`;
      btn.querySelector('span').textContent = b.label;
      btn.addEventListener('click', () => b.onClick && b.onClick(close));
      footEl.appendChild(btn);
    });
    if (!(foot || []).length) footEl.remove();
    const close = () => { mask.remove(); onClose && onClose(); };
    mask.addEventListener('mousedown', e => { if (e.target === mask) close(); });
    mask.querySelector('[data-x]').addEventListener('click', close);
    document.body.appendChild(mask);
    return { mask, close };
  };

  // 确认框
  UI.confirm = function (title, msg, danger) {
    return new Promise(resolve => {
      const m = UI.modal({
        title,
        body: `<p style="font-size:12.5px;color:var(--fg-dim);line-height:1.8"></p>`,
        foot: [
          { label: '取消', onClick: (close) => { close(); resolve(false); } },
          { label: '确认', cls: danger ? 'flow-danger' : 'primary', onClick: (close) => { close(); resolve(true); } },
        ],
        onClose: () => resolve(false),
      });
      m.mask.querySelector('p').textContent = msg;
    });
  };

  // ---------- 右侧抽屉 ----------
  UI.drawer = function ({ title, sub, tabs }) {
    const mask = document.createElement('div');
    mask.className = 'drawer-mask';
    const d = document.createElement('div');
    d.className = 'drawer';
    d.innerHTML = `
      <div class="drawer-head">
        <div style="flex:1;min-width:0">
          <h3 class="d-title"></h3>
          <p class="sub d-sub"></p>
        </div>
        <button type="button" class="icon-btn" data-x><svg><use href="#i-close"/></svg></button>
      </div>
      <div class="tabs d-tabs" hidden></div>
      <div class="drawer-body d-body"></div>
      <div class="drawer-foot d-foot" hidden></div>`;
    d.querySelector('.d-title').textContent = title || '';
    d.querySelector('.d-sub').textContent = sub || '';
    const close = () => { mask.remove(); d.remove(); onClose && onClose(); };
    let onClose = null;
    mask.addEventListener('mousedown', close);
    d.querySelector('[data-x]').addEventListener('click', close);
    document.body.appendChild(mask);
    document.body.appendChild(d);

    const api = {
      el: d,
      body: d.querySelector('.d-body'),
      foot: d.querySelector('.d-foot'),
      close,
      onClose(fn) { onClose = fn; },
      setTabs(names, onSwitch, active = 0) {
        const tb = d.querySelector('.d-tabs');
        tb.hidden = !names || !names.length;
        tb.innerHTML = '';
        names.forEach((n, i) => {
          const b = document.createElement('button');
          b.type = 'button';
          b.className = 'tab' + (i === active ? ' active' : '');
          b.textContent = n;
          b.addEventListener('click', () => {
            tb.querySelectorAll('.tab').forEach(t => t.classList.remove('active'));
            b.classList.add('active');
            onSwitch(i);
          });
          tb.appendChild(b);
        });
      },
      setFoot(buttons) {
        this.foot.hidden = !buttons || !buttons.length;
        this.foot.innerHTML = '';
        (buttons || []).forEach(b => {
          const btn = document.createElement('button');
          btn.className = 'btn ' + (b.cls || '');
          btn.type = 'button';
          btn.innerHTML = (b.icon ? `<svg><use href="#${b.icon}"/></svg>` : '') + '<span></span>';
          btn.querySelector('span').textContent = b.label;
          btn.addEventListener('click', () => b.onClick && b.onClick());
          this.foot.appendChild(btn);
        });
      },
    };
    return api;
  };

  // ---------- 浮出菜单 ----------
  UI.menu = function (anchor, items) {
    document.querySelectorAll('.menu').forEach(m => m.remove());
    const menu = document.createElement('div');
    menu.className = 'menu';
    items.forEach(it => {
      if (it === '-') {
        const sep = document.createElement('div');
        sep.className = 'menu-sep';
        menu.appendChild(sep);
        return;
      }
      const el = document.createElement('div');
      el.className = 'menu-item' + (it.danger ? ' danger' : '') + (it.disabled ? ' disabled' : '');
      el.innerHTML = (it.icon ? `<svg><use href="#${it.icon}"/></svg>` : '') + '<span></span>';
      el.querySelector('span').textContent = it.label;
      el.addEventListener('click', () => { cleanup(); it.onClick && it.onClick(); });
      menu.appendChild(el);
    });
    document.body.appendChild(menu);
    const r = anchor.getBoundingClientRect();
    const mw = menu.offsetWidth, mh = menu.offsetHeight;
    let x = Math.min(r.left, window.innerWidth - mw - 8);
    let y = r.bottom + 4;
    if (y + mh > window.innerHeight - 8) y = Math.max(8, r.top - mh - 4);
    menu.style.left = x + 'px';
    menu.style.top = y + 'px';
    const cleanup = () => { document.removeEventListener('mousedown', onDoc, true); menu.remove(); };
    const onDoc = (e) => { if (!menu.contains(e.target)) cleanup(); };
    setTimeout(() => document.addEventListener('mousedown', onDoc, true), 0);
  };

  // ---------- 开关 ----------
  UI.switchEl = function (on, onChange) {
    const b = document.createElement('button');
    b.type = 'button';
    b.className = 'switch' + (on ? ' on' : '');
    b.addEventListener('click', () => {
      b.classList.toggle('on');
      onChange && onChange(b.classList.contains('on'));
    });
    return b;
  };

  // ---------- 复选 ----------
  UI.checkEl = function (on, onChange) {
    const s = document.createElement('span');
    s.className = 'check' + (on ? ' on' : '');
    s.addEventListener('click', (e) => {
      e.stopPropagation();
      s.classList.toggle('on');
      onChange && onChange(s.classList.contains('on'));
    });
    return s;
  };

  // 状态徽章
  UI.stateBadge = function (state) {
    const map = { running: '运行中', stopped: '已停止', exited: '已退出', created: '已创建', paused: '已暂停', restarting: '重启中', dead: '已死亡' };
    return `<span class="badge ${UI.esc(state)}"><i class="dot ${UI.esc(state)}"></i>${UI.esc(map[state] || state)}</span>`;
  };
  UI.updBadge = function (status, hasUpdate) {
    if (hasUpdate) return '<span class="badge updatable">可更新</span>';
    if (status === 'up-to-date') return '<span class="badge up-to-date">最新</span>';
    if (status === 'unknown') return '<span class="badge unknown">未知</span>';
    return '';
  };

  // 空状态
  UI.empty = function (icon, title, desc) {
    return `<div class="empty"><div class="empty-art"><svg><use href="#${icon}"/></svg></div><h3></h3><p></p></div>`;
  };
  UI.fillEmpty = function (container, icon, title, desc) {
    container.innerHTML = `<div class="empty"><div class="empty-art"><svg><use href="#${icon}"/></svg></div><h3></h3><p></p></div>`;
    container.querySelector('h3').textContent = title;
    container.querySelector('p').textContent = desc || '';
  };
  UI.loading = function (container, text) {
    container.innerHTML = `<div class="loading-block"><div class="spinner"></div><span></span></div>`;
    container.querySelector('span').textContent = text || '加载中…';
  };

  // 防抖
  UI.debounce = function (fn, ms) {
    let t;
    return function (...args) {
      clearTimeout(t);
      t = setTimeout(() => fn.apply(this, args), ms || 250);
    };
  };

  window.UI = UI;
})();
