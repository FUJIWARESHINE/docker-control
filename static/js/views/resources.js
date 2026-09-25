// 资源页：网络 / 存储卷 / 端口
(function () {
  'use strict';

  window.Views = window.Views || {};
  Views.networks = {
    title: '网络管理',
    async mount(content) {
      content.innerHTML = `<div class="page">
        <div class="toolbar">
          <span class="pill" id="netCount"></span>
          <div class="spacer"></div>
          <button type="button" class="btn flow-danger" id="netPrune"><svg><use href="#i-trash"/></svg><span>清理未使用网络</span></button>
        </div>
        <div class="grid c2" id="netGrid"></div></div>`;
      const grid = content.querySelector('#netGrid');
      async function load() {
        try {
          const s = await API.get('/api/networks');
          const list = s.data || [];
          content.querySelector('#netCount').innerHTML = `<b>${list.length}</b> 个网络`;
          if (!list.length) return UI.fillEmpty(grid, 'i-net', '没有网络', '');
          grid.innerHTML = '';
          list.forEach(n => {
            const card = document.createElement('div');
            card.className = 'card';
            const cons = (n.containers || []).map(c =>
              `<span class="badge ${UI.esc(c.status === 'running' ? 'ok' : 'stopped')}" style="margin:2px">${UI.esc(c.name)} ${UI.esc((c.ipv4 || '').split('/')[0])}</span>`).join('');
            card.innerHTML = `
              <div style="display:flex;align-items:center;gap:8px">
                <h4 style="flex:1"></h4>
                <span class="badge ${n.internal ? 'warn' : 'brand'}">${n.internal ? 'internal' : UI.esc(n.driver)}</span>
                <button type="button" class="icon-btn del"><svg><use href="#i-trash"/></svg></button>
              </div>
              <p class="muted mono" style="margin:4px 0 8px">${UI.esc(n.id)} · ${UI.fmtTime(n.created)}</p>
              <div>${cons || '<p class="muted">无接入容器</p>'}</div>`;
            card.querySelector('h4').textContent = n.name;
            card.querySelector('.icon-btn').addEventListener('click', async () => {
              if (!await UI.confirm('删除网络', `确定删除网络「${n.name}」？`, true)) return;
              try { await API.del('/api/networks/' + encodeURIComponent(n.name)); UI.toast('已删除', 'ok'); load(); }
              catch (e) { UI.toast(e.message, 'error'); }
            });
            grid.appendChild(card);
          });
        } catch (e) { UI.toast(e.message, 'error'); }
      }
      content.querySelector('#netPrune').addEventListener('click', async () => {
        if (!await UI.confirm('清理网络', '删除所有未被使用的网络？', true)) return;
        try { await API.post('/api/networks/prune', {}); UI.toast('清理完成', 'ok'); load(); }
        catch (e) { UI.toast(e.message, 'error'); }
      });
      await load();
    },
  };

  Views.volumes = {
    title: '存储卷管理',
    async mount(content) {
      content.innerHTML = `<div class="page">
        <div class="toolbar">
          <span class="pill" id="volCount"></span>
          <div class="spacer"></div>
          <button type="button" class="btn flow-danger" id="volPrune"><svg><use href="#i-trash"/></svg><span>清理未使用卷</span></button>
        </div>
        <div class="table-card"><div class="tscroll" style="max-height:calc(100vh - 200px)">
          <table class="tbl"><thead><tr>
            <th>卷名</th><th>驱动</th><th>挂载点</th><th>引用</th><th>创建时间</th><th style="text-align:right">操作</th>
          </tr></thead><tbody id="volRows"></tbody></table>
        </div></div></div>`;
      const rows = content.querySelector('#volRows');
      async function load() {
        try {
          const s = await API.get('/api/volumes');
          const list = s.data || [];
          content.querySelector('#volCount').innerHTML = `<b>${list.length}</b> 个卷`;
          rows.innerHTML = '';
          if (!list.length) return UI.fillEmpty(rows.closest('.table-card'), 'i-db', '没有存储卷', '');
          list.forEach(v => {
            const tr = document.createElement('tr');
            tr.innerHTML = `
              <td class="name mono">${UI.esc(v.name)}</td>
              <td class="dim">${UI.esc(v.driver)}</td>
              <td class="mono dim">${UI.esc(v.mountpoint)}</td>
              <td>${v.usage.ref_count > 0 ? `<span class="badge ok">${v.usage.ref_count} 个容器</span>` : '<span class="badge idle">未使用</span>'}</td>
              <td class="dim">${UI.fmtTime(v.created)}</td>
              <td><div class="actions"><button type="button" class="icon-btn del"><svg><use href="#i-trash"/></svg></button></div></td>`;
            tr.querySelector('.icon-btn').addEventListener('click', async () => {
              if (!await UI.confirm('删除卷', `确定删除「${v.name}」？数据将不可恢复。`, true)) return;
              try { await API.del('/api/volumes/' + encodeURIComponent(v.name)); UI.toast('已删除', 'ok'); load(); }
              catch (e) { UI.toast(e.message, 'error'); }
            });
            rows.appendChild(tr);
          });
        } catch (e) { UI.toast(e.message, 'error'); }
      }
      content.querySelector('#volPrune').addEventListener('click', async () => {
        if (!await UI.confirm('清理卷', '删除所有未被使用的存储卷？', true)) return;
        try { await API.post('/api/volumes/prune', {}); UI.toast('清理完成', 'ok'); load(); }
        catch (e) { UI.toast(e.message, 'error'); }
      });
      await load();
    },
  };

  Views.ports = {
    title: '端口扫描',
    async mount(content) {
      content.innerHTML = `<div class="page">
        <div class="toolbar">
          <span class="pill" id="portCount"></span>
          <div class="spacer"></div>
          <button type="button" class="btn primary" id="portRefresh"><svg><use href="#i-refresh"/></svg><span>重新扫描</span></button>
        </div>
        <div class="table-card"><div class="tscroll" style="max-height:calc(100vh - 200px)">
          <table class="tbl"><thead><tr>
            <th style="width:90px">端口</th><th style="width:70px">协议</th><th>备注</th><th>来源</th><th>容器 / 内部端口</th><th style="text-align:right">操作</th>
          </tr></thead><tbody id="portRows"></tbody></table>
        </div></div></div>`;
      const rows = content.querySelector('#portRows');
      async function load() {
        try {
          const s = await API.get('/api/ports');
          const list = s.data || [];
          content.querySelector('#portCount').innerHTML = `<b>${list.length}</b> 个端口`;
          rows.innerHTML = '';
          if (!list.length) return UI.fillEmpty(rows.closest('.table-card'), 'i-plug', '未发现监听端口', '');
          list.forEach(p => {
            const tr = document.createElement('tr');
            tr.innerHTML = `
              <td class="name mono">${p.port}</td>
              <td><span class="badge ${p.protocol === 'tcp' ? 'brand' : 'cyan'}">${UI.esc(p.protocol.toUpperCase())}</span></td>
              <td class="dim svc">${UI.esc(p.service || '-')}</td>
              <td>${p.host_only ? '<span class="badge unknown">Host</span>' : '<span class="badge ok">Container</span>'}</td>
              <td class="mono dim">${UI.esc(p.container ? `${p.container} (${p.container_port})` : '-')}</td>
              <td><div class="actions"><button type="button" class="icon-btn"><svg><use href="#i-edit"/></svg></button></div></td>`;
            tr.querySelector('.icon-btn').addEventListener('click', () => {
              const m = UI.modal({
                title: `端口备注 · ${p.port}/${p.protocol}`,
                body: `<div class="field"><label>备注名称</label><input id="pnName" /></div>`,
                foot: [
                  { label: '取消', onClick: (c) => c() },
                  { label: '保存', cls: 'primary', icon: 'i-check', onClick: async (c) => {
                    const name = m.mask.querySelector('#pnName').value.trim();
                    try { await API.post(`/api/ports/${p.port}/name`, { name }); UI.toast('备注已保存', 'ok'); c(); load(); }
                    catch (e) { UI.toast(e.message, 'error'); }
                  } },
                ],
              });
              m.mask.querySelector('#pnName').value = p.service || '';
            });
            rows.appendChild(tr);
          });
        } catch (e) { UI.toast(e.message, 'error'); }
      }
      content.querySelector('#portRefresh').addEventListener('click', load);
      await load();
    },
  };
})();
