// 应用入口：路由 / 侧栏 / 主题 / 登录守卫
(function () {
  'use strict';

  const NAV = [
    { group: '总览' },
    { id: 'dashboard', view: 'dashboard', icon: 'i-gauge', label: '仪表板' },
    { group: '工作负载' },
    { id: 'containers', view: 'containers', icon: 'i-box', label: '容器' },
    { id: 'images', view: 'images', icon: 'i-layers', label: '镜像' },
    { id: 'compose', view: 'compose', icon: 'i-stack', label: 'Compose' },
    { group: '资源' },
    { id: 'networks', view: 'networks', icon: 'i-net', label: '网络' },
    { id: 'volumes', view: 'volumes', icon: 'i-db', label: '存储卷' },
    { id: 'ports', view: 'ports', icon: 'i-plug', label: '端口' },
    { group: '运维' },
    { id: 'updates', view: 'updates', icon: 'i-refresh', label: '更新中心' },
    { id: 'tasks', view: 'tasks', icon: 'i-clock', label: '定时任务' },
    { id: 'templates', view: 'templates', icon: 'i-file', label: '模板' },
    { group: '系统' },
    { id: 'settings', view: 'settings', icon: 'i-gear', label: '系统设置' },
  ];

  let current = null;
  const contentEl = document.getElementById('content');

  function token() { return localStorage.getItem('dc-token') || ''; }

  async function guard() {
    if (!token()) { location.href = '/static/login.html'; return false; }
    try {
      const s = await fetch('/api/auth/status').then(r => r.json());
      if (s && s.version) {
        document.getElementById('sideVer').textContent = 'v' + s.version;
      }
      // 实际鉴权探测
      const probe = await fetch('/api/containers', { headers: { Authorization: 'Bearer ' + token() } });
      if (probe.status === 401) { localStorage.removeItem('dc-token'); location.href = '/static/login.html'; return false; }
    } catch (e) { /* 后端不可达也继续渲染 */ }
    return true;
  }

  function renderNav() {
    const nav = document.getElementById('nav');
    nav.innerHTML = '';
    NAV.forEach(item => {
      if (item.group) {
        const g = document.createElement('div');
        g.className = 'nav-group';
        g.textContent = item.group;
        nav.appendChild(g);
        return;
      }
      const b = document.createElement('button');
      b.type = 'button';
      b.className = 'nav-item' + (current === item.id ? ' active' : '');
      b.innerHTML = `<svg><use href="#${item.icon}"/></svg><span></span>`;
      b.querySelector('span').textContent = item.label;
      b.dataset.id = item.id;
      b.addEventListener('click', () => go(item.id));
      nav.appendChild(b);
    });
  }

  async function go(id) {
    const item = NAV.find(n => n.id === id);
    if (!item) return;
    if (current && Views[NAV.find(n => n.id === current).view] && Views[NAV.find(n => n.id === current).view].unmount) {
      try { Views[NAV.find(n => n.id === current).view].unmount(); } catch (e) { console.error(e); }
    }
    current = id;
    document.querySelectorAll('.nav-item').forEach(el => el.classList.toggle('active', el.dataset.id === id));
    document.getElementById('pageTitle').textContent = item.label;
    document.getElementById('pageSub').textContent = '';
    history.replaceState(null, '', '#' + id);
    UI.loading(contentEl);
    try {
      await Views[item.view].mount(contentEl);
    } catch (e) {
      console.error(e);
      UI.fillEmpty(contentEl, 'i-warn', '页面加载失败', e.message);
    }
  }

  function initTheme() {
    const btn = document.getElementById('btnTheme');
    const sync = () => {
      const dark = document.documentElement.dataset.theme !== 'light';
      btn.querySelector('use').setAttribute('href', dark ? '#i-moon' : '#i-sun');
    };
    sync();
    btn.addEventListener('click', () => {
      const next = document.documentElement.dataset.theme === 'light' ? 'dark' : 'light';
      document.documentElement.dataset.theme = next;
      try { localStorage.setItem('dc-theme', next); } catch (e) { }
      sync();
    });
  }

  window.addEventListener('hashchange', () => {
    const hash = (location.hash || '').replace('#', '');
    if (NAV.find(n => n.id === hash) && hash !== current) go(hash);
  });

  window.addEventListener('DOMContentLoaded', async () => {
    if (!await guard()) return;
    initTheme();
    renderNav();
    document.getElementById('btnLogout').addEventListener('click', async () => {
      try { await API.post('/api/auth/logout', {}); } catch (e) { }
      WS.close();
      localStorage.removeItem('dc-token');
      location.href = '/static/login.html';
    });
    WS.connect();
    const hash = (location.hash || '').replace('#', '');
    go(NAV.find(n => n.id === hash) ? hash : 'dashboard');
  });
})();
