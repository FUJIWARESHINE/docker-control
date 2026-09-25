// 自动化：定时任务 + 部署模板
(function () {
  'use strict';

  window.Views = window.Views || {};
  Views.tasks = {
    title: '定时任务',
    async mount(content) {
      content.innerHTML = `<div class="page">
        <div class="toolbar">
          <span class="pill" id="tkCount"></span>
          <div class="spacer"></div>
          <button type="button" class="btn primary" id="tkAdd"><svg><use href="#i-plus"/></svg><span>新建任务</span></button>
        </div>
        <div class="table-card"><div class="tscroll" style="max-height:calc(100vh - 200px)">
          <table class="tbl"><thead><tr>
            <th>目标</th><th>类型</th><th>动作</th><th>周期表达式</th><th>状态</th><th style="text-align:right">操作</th>
          </tr></thead><tbody id="tkRows"></tbody></table>
        </div></div>
        <p class="muted" style="margin-top:12px;font-size:11px">周期表达式示例：<span class="mono">every 1h30m</span>、<span class="mono">every 24h</span>。动作支持 start / stop / restart / update。</p></div>`;
      const rows = content.querySelector('#tkRows');

      async function load() {
        try {
          const s = await API.get('/api/tasks');
          const list = s.tasks || [];
          content.querySelector('#tkCount').innerHTML = `<b>${list.length}</b> 个任务`;
          rows.innerHTML = '';
          if (!list.length) return UI.fillEmpty(rows.closest('.table-card'), 'i-clock', '还没有定时任务', '点击「新建任务」创建第一个');
          list.forEach(t => {
            const tr = document.createElement('tr');
            tr.innerHTML = `
              <td class="name">${UI.esc(t.name)}</td>
              <td><span class="badge ${t.type === 'compose' ? 'cyan' : 'brand'}">${UI.esc(t.type)}</span></td>
              <td class="dim">${UI.esc(t.action)}</td>
              <td class="mono dim">${UI.esc(t.cron_expression || '-')}</td>
              <td>${t.enabled ? '<span class="badge ok">启用</span>' : '<span class="badge idle">停用</span>'}</td>
              <td><div class="actions">
                <button type="button" class="btn tiny ok" data-op="execute"><span>立即执行</span></button>
                <button type="button" class="btn tiny" data-op="toggle"><span>${t.enabled ? '停用' : '启用'}</span></button>
                <button type="button" class="icon-btn del"><svg><use href="#i-trash"/></svg></button>
              </div></td>`;
            tr.querySelectorAll('[data-op]').forEach(b => b.addEventListener('click', async () => {
              try {
                if (b.dataset.op === 'execute') await API.post(`/api/tasks/${t.id}/execute`, {});
                else await API.put('/api/tasks', { id: t.id, enabled: !t.enabled });
                UI.toast('已执行', 'ok'); load();
              } catch (e) { UI.toast(e.message, 'error'); }
            }));
            tr.querySelector('.icon-btn').addEventListener('click', async () => {
              try { await API.del(`/api/tasks/${t.id}`); UI.toast('任务已删除', 'ok'); load(); }
              catch (e) { UI.toast(e.message, 'error'); }
            });
            rows.appendChild(tr);
          });
        } catch (e) { UI.toast(e.message, 'error'); }
      }

      content.querySelector('#tkAdd').addEventListener('click', () => {
        const m = UI.modal({
          title: '新建定时任务',
          body: `
            <div class="field-row">
              <div class="field"><label>类型</label>
                <select id="tkType"><option value="container">容器</option><option value="compose">Compose 项目</option></select></div>
              <div class="field"><label>目标名称</label><input id="tkName" placeholder="容器名 / 项目名" /></div>
            </div>
            <div class="field-row">
              <div class="field"><label>动作</label>
                <select id="tkAction"><option value="restart">restart</option><option value="start">start</option><option value="stop">stop</option><option value="update">update</option></select></div>
              <div class="field"><label>周期表达式</label><input id="tkCron" placeholder="every 24h" /></div>
            </div>`,
          foot: [
            { label: '取消', onClick: (c) => c() },
            { label: '创建', cls: 'primary', icon: 'i-check', onClick: async (c) => {
              const name = m.mask.querySelector('#tkName').value.trim();
              if (!name) return UI.toast('请填写目标名称', 'warn');
              try {
                await API.post('/api/tasks', {
                  type: m.mask.querySelector('#tkType').value,
                  container_name: name,
                  action: m.mask.querySelector('#tkAction').value,
                  cron_expression: m.mask.querySelector('#tkCron').value.trim(),
                });
                UI.toast('任务已创建', 'ok'); c(); load();
              } catch (e) { UI.toast(e.message, 'error'); }
            } },
          ],
        });
      });

      await load();
    },
  };

  Views.templates = {
    title: '部署模板',
    async mount(content) {
      content.innerHTML = `<div class="page">
        <div class="toolbar">
          <span class="pill" id="tpCount"></span>
          <div class="spacer"></div>
          <button type="button" class="btn primary" id="tpAdd"><svg><use href="#i-plus"/></svg><span>新建模板</span></button>
        </div>
        <div class="grid c3" id="tpGrid"></div></div>`;
      const grid = content.querySelector('#tpGrid');

      async function load() {
        try {
          const s = await API.get('/api/templates');
          const list = s.data || [];
          content.querySelector('#tpCount').innerHTML = `<b>${list.length}</b> 个模板`;
          if (!list.length) return UI.fillEmpty(grid, 'i-file', '还没有模板', '模板保存容器的创建参数，方便一键复用');
          grid.innerHTML = '';
          list.forEach(t => {
            const card = document.createElement('div');
            card.className = 'card';
            const img = (t.image || (t.config && t.config.image) || '');
            card.innerHTML = `
              <div style="display:flex;align-items:center;gap:8px">
                <h4 style="flex:1"></h4>
                <button type="button" class="icon-btn del"><svg><use href="#i-trash"/></svg></button>
              </div>
              <p class="muted mono" style="margin:4px 0 10px">${UI.esc(img)}</p>
              <button type="button" class="btn small primary" style="width:100%"><span>使用模板创建容器</span></button>`;
            card.querySelector('h4').textContent = t.name || '';
            card.querySelector('.icon-btn').addEventListener('click', async () => {
              if (!await UI.confirm('删除模板', `确定删除「${t.name}」？`, true)) return;
              try { await API.del('/api/templates/' + encodeURIComponent(t.name)); UI.toast('已删除', 'ok'); load(); }
              catch (e) { UI.toast(e.message, 'error'); }
            });
            card.querySelector('.btn').addEventListener('click', () => useTemplate(t));
            grid.appendChild(card);
          });
        } catch (e) { UI.toast(e.message, 'error'); }
      }

      function useTemplate(t) {
        const cfg = t.config || t;
        const m = UI.modal({
          title: '使用模板「' + (t.name || '') + '」创建容器',
          wide: true,
          body: `
            <div class="field"><label>容器名</label><input id="tcName" /></div>
            <div class="field"><label>创建参数（JSON，Engine /containers/create spec）</label>
              <textarea id="tcSpec" style="min-height:260px"></textarea></div>`,
          foot: [
            { label: '取消', onClick: (c) => c() },
            { label: '创建并启动', cls: 'primary', icon: 'i-play', onClick: async (c) => {
              let spec;
              try { spec = JSON.parse(m.mask.querySelector('#tcSpec').value); }
              catch (e) { return UI.toast('JSON 格式错误: ' + e.message, 'error'); }
              spec.name = m.mask.querySelector('#tcName').value.trim() || spec.name;
              try { await API.post('/api/containers', spec); UI.toast('容器已创建', 'ok'); c(); }
              catch (e) { UI.toast(e.message, 'error'); }
            } },
          ],
        });
        m.mask.querySelector('#tcName').value = t.name || '';
        m.mask.querySelector('#tcSpec').value = JSON.stringify(cfg, null, 2);
      }

      content.querySelector('#tpAdd').addEventListener('click', () => {
        const m = UI.modal({
          title: '新建模板',
          body: `
            <div class="field"><label>模板名称</label><input id="tpName" /></div>
            <div class="field"><label>创建参数（JSON）</label><textarea id="tpSpec" style="min-height:260px" placeholder='{"Image":"nginx:latest","HostConfig":{"RestartPolicy":{"Name":"unless-stopped"}}}'></textarea></div>`,
          foot: [
            { label: '取消', onClick: (c) => c() },
            { label: '保存', cls: 'primary', icon: 'i-check', onClick: async (c) => {
              const name = m.mask.querySelector('#tpName').value.trim();
              if (!name) return UI.toast('请填写名称', 'warn');
              let spec;
              try { spec = JSON.parse(m.mask.querySelector('#tpSpec').value); }
              catch (e) { return UI.toast('JSON 格式错误: ' + e.message, 'error'); }
              try { await API.post('/api/templates', { name, config: spec }); UI.toast('模板已保存', 'ok'); c(); load(); }
              catch (e) { UI.toast(e.message, 'error'); }
            } },
          ],
        });
      });

      await load();
    },
  };
})();
