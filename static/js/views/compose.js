// Compose 项目页
(function () {
  'use strict';

  window.Views = window.Views || {};
  Views.compose = {
    title: 'Compose 项目',
    async mount(content) {
      content.innerHTML = `<div class="page">
        <div class="toolbar">
          <span class="pill" id="cpCount"></span>
          <div class="spacer"></div>
          <button type="button" class="btn primary" id="cpRefresh"><svg><use href="#i-refresh"/></svg><span>刷新</span></button>
        </div>
        <div class="grid c2" id="cpGrid"></div></div>`;
      const grid = content.querySelector('#cpGrid');

      async function load() {
        UI.loading(grid);
        try {
          const s = await API.get('/api/compose');
          const list = s.data || [];
          content.querySelector('#cpCount').innerHTML = `<b>${list.length}</b> 个项目`;
          if (!list.length) return UI.fillEmpty(grid, 'i-stack', '没有 Compose 项目', '由 docker compose 启动的项目会出现在这里');
          grid.innerHTML = '';
          list.forEach(p => {
            const card = document.createElement('div');
            card.className = 'card';
            const cons = (p.containers || []).map(c =>
              `<span class="badge ${UI.esc(c.state === 'running' ? 'ok' : 'stopped')}" style="margin:2px">${UI.esc(c.name)}</span>`).join('');
            card.innerHTML = `
              <div style="display:flex;align-items:center;gap:8px">
                <h4 style="flex:1"></h4>
                ${UI.stateBadge(p.state)}
                <span class="badge brand">${p.running_count}/${p.container_count}</span>
              </div>
              <p class="muted mono" style="margin:4px 0 8px">${UI.esc(p.compose_filename || '')} · ${UI.esc(p.path || '')}</p>
              <div style="margin-bottom:12px">${cons}</div>
              <div style="display:flex;gap:6px;flex-wrap:wrap">
                <button type="button" class="btn small ok" data-act="start"><svg><use href="#i-play"/></svg><span>启动</span></button>
                <button type="button" class="btn small" data-act="stop"><svg><use href="#i-stop"/></svg><span>停止</span></button>
                <button type="button" class="btn small" data-act="restart"><svg><use href="#i-restart"/></svg><span>重启</span></button>
                <button type="button" class="btn small warn" data-act="down"><svg><use href="#i-trash"/></svg><span>Down</span></button>
                <button type="button" class="btn small ghost" data-act="file"><svg><use href="#i-edit"/></svg><span>编辑</span></button>
                <button type="button" class="btn small ghost" data-act="logs"><svg><use href="#i-log"/></svg><span>日志</span></button>
              </div>`;
            card.querySelector('h4').textContent = p.name;
            card.querySelectorAll('button[data-act]').forEach(btn => {
              btn.addEventListener('click', () => act(btn.dataset.act, p));
            });
            grid.appendChild(card);
          });
        } catch (e) { UI.toast(e.message, 'error'); }
      }

      async function act(kind, p) {
        if (kind === 'file') {
          try {
            const s = await API.get(`/api/compose/file?project_name=${encodeURIComponent(p.name)}`);
            const info = s.data || {};
            const m = UI.modal({
              title: '编辑 ' + (info.path || p.name),
              wide: true,
              body: `<div class="field"><textarea style="min-height:380px"></textarea></div>`,
              foot: [
                { label: '取消', onClick: (c) => c() },
                { label: '保存', cls: 'primary', icon: 'i-check', onClick: async (c) => {
                  try {
                    await API.post('/api/compose/file', { project_name: p.name, content: m.mask.querySelector('textarea').value });
                    UI.toast('已保存', 'ok'); c();
                  } catch (e) { UI.toast(e.message, 'error'); }
                } },
              ],
            });
            m.mask.querySelector('textarea').value = info.content || '';
          } catch (e) { UI.toast(e.message, 'error'); }
          return;
        }
        if (kind === 'logs') {
          try {
            const t = await API.text(`/api/compose/logs?project=${encodeURIComponent(p.name)}&tail=200`);
            const m = UI.modal({
              title: p.name + ' 日志', wide: true,
              body: `<div class="logview" style="min-height:300px;max-height:60vh"></div>`,
              foot: [{ label: '关闭', onClick: (c) => c() }],
            });
            m.mask.querySelector('.logview').textContent = t || '(无日志)';
          } catch (e) { UI.toast(e.message, 'error'); }
          return;
        }
        if (kind === 'down' && !await UI.confirm('Down 项目', `停止并移除项目「${p.name}」的所有容器？`, true)) return;
        try {
          const s = await API.post(`/api/compose/${encodeURIComponent(p.name)}/${kind}`, { project_name: p.name });
          UI.toast(s.message || '操作成功', 'ok');
          load();
        } catch (e) { UI.toast(e.message, 'error'); }
      }

      content.querySelector('#cpRefresh').addEventListener('click', load);
      await load();
    },
  };
})();
