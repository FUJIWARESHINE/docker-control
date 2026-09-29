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
})();
