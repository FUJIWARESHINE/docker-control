// 更新中心：检查更新 / 一键更新（WS 进度）/ 自动更新
(function () {
  'use strict';

  let progressHandler = null;
  let listHandler = null;

  window.Views = window.Views || {};
  Views.updates = {
    title: '更新中心',
    async mount(content) {
      content.innerHTML = `<div class="page">
        <div class="toolbar">
          <span class="pill" id="upLast">从未检查</span>
          <div class="spacer"></div>
          <button type="button" class="btn" id="upBtnSettings"><svg><use href="#i-gear"/></svg><span>自动更新设置</span></button>
          <button type="button" class="btn primary" id="upBtnCheck"><svg><use href="#i-refresh"/></svg><span>检查更新</span></button>
        </div>
        <div class="table-card"><div class="tscroll" style="max-height:calc(100vh - 250px)">
          <table class="tbl"><thead><tr>
            <th>容器</th><th>镜像</th><th>本地 digest</th><th>远端 digest</th><th>状态</th><th style="text-align:right">操作</th>
          </tr></thead><tbody id="upRows"></tbody></table>
        </div></div>
        <p class="muted" style="margin-top:12px;font-size:11px">
          检查原理：对比本地与远端 registry 的镜像 manifest digest。仅对 docker.io 及匿名可读仓库有效；配置了镜像加速或私有仓库的结果会显示「未知」。
        </p></div>`;

      const rows = content.querySelector('#upRows');

      // WS 更新进度 → 顶部进度条
      const bar = document.getElementById('wsProgress');
      const barFill = bar ? bar.querySelector('i') : null;
      progressHandler = (p) => {
        if (!bar) return;
        bar.hidden = false;
        barFill.style.width = (p.percentage || 0) + '%';
        document.getElementById('pageSub').textContent = `[${p.container}] ${p.message || ''}`;
        if (p.status === 'success' || p.status === 'error') {
          setTimeout(() => { bar.hidden = true; barFill.style.width = '0'; document.getElementById('pageSub').textContent = ''; }, 2500);
          UI.toast(p.message || (p.status === 'success' ? '更新完成' : '更新失败'), p.status === 'success' ? 'ok' : 'error');
          load();
        }
      };
      WS.on('update_progress', progressHandler);

      async function load() {
        try {
          const cfg = await API.get('/api/updates/settings');
          const d = cfg.data || {};
          const last = d.last_check ? '上次检查: ' + UI.fmtTime(d.last_check) : '从未检查';
          content.querySelector('#upLast').innerHTML = last + (d.auto_update_containers && d.auto_update_containers.length ? ` · <b>${d.auto_update_containers.length}</b> 个自动更新` : '');
        } catch (e) { /* */ }
      }

      async function check() {
        const btn = content.querySelector('#upBtnCheck');
        btn.disabled = true;
        btn.querySelector('span').textContent = '检查中…';
        UI.loading(rows.closest('.table-card'), '正在对比 registry digest，通常需要 5-30 秒…');
        try {
          const s = await API.post('/api/updates/check', {});
          const list = s.data || [];
          rows.innerHTML = '';
          const card = rows.closest('.table-card');
          card.innerHTML = `<div class="tscroll" style="max-height:calc(100vh - 250px)">
            <table class="tbl"><thead><tr>
              <th>容器</th><th>镜像</th><th>本地 digest</th><th>远端 digest</th><th>状态</th><th style="text-align:right">操作</th>
            </tr></thead><tbody id="upRows"></tbody></table></div>`;
          const tbody = card.querySelector('#upRows');
          bindRows(tbody);
          if (!list.length) return UI.fillEmpty(card, 'i-box', '没有容器', '');
          list.forEach(r => {
            const tr = document.createElement('tr');
            const short = (d) => d ? `<span class="mono dim">${UI.esc(String(d).slice(0, 19))}…</span>` : '<span class="dim">-</span>';
            tr.innerHTML = `
              <td class="name">${UI.esc(r.container_name)}</td>
              <td class="mono dim">${UI.esc(r.image)}</td>
              <td>${short(r.local_digest)}</td>
              <td>${short(r.remote_digest)}</td>
              <td>${UI.updBadge(r.status, r.has_update)}</td>
              <td><div class="actions">
                <button type="button" class="btn tiny primary" data-op="apply" ${r.has_update ? '' : 'disabled'}><span>更新</span></button>
                <button type="button" class="btn tiny" data-op="auto"><span>自动</span></button>
              </div></td>`;
            tr.querySelectorAll('button[data-op]').forEach(b => b.addEventListener('click', () => rowOp(b.dataset.op, r)));
            tbody.appendChild(tr);
          });
          load();
        } catch (e) { UI.toast(e.message, 'error'); }
        btn.disabled = false;
        btn.querySelector('span').textContent = '检查更新';
      }

      function bindRows(tbody) {
        // 由 check() 内部绑定
      }

      async function rowOp(op, r) {
        if (op === 'apply') {
          if (!await UI.confirm('更新容器', `拉取新镜像并重建容器「${r.container_name}」？配置会保留，过程约数秒到数分钟。`)) return;
          try { await API.post(`/api/updates/${encodeURIComponent(r.container_name)}/apply`, {}); UI.toast('更新任务已启动', 'ok'); }
          catch (e) { UI.toast(e.message, 'error'); }
        } else {
          try {
            const cur = await API.get('/api/updates/settings');
            const lst = (cur.data.auto_update_containers || []);
            const has = lst.includes(r.container_name);
            await API.post(`/api/updates/${encodeURIComponent(r.container_name)}/auto`, { enabled: !has });
            UI.toast(has ? '已关闭自动更新' : '已开启自动更新', 'ok');
            load();
          } catch (e) { UI.toast(e.message, 'error'); }
        }
      }

      content.querySelector('#upBtnCheck').addEventListener('click', check);
      content.querySelector('#upBtnSettings').addEventListener('click', async () => {
        let cur = { update_interval_days: 7, update_interval_hours: 0 };
        try { cur = (await API.get('/api/updates/settings')).data || cur; } catch (e) { /* */ }
        const m = UI.modal({
          title: '自动更新设置',
          body: `<div class="field-row">
              <div class="field"><label>检查间隔（天）</label><input type="number" id="upDays" min="0" /></div>
              <div class="field"><label>额外小时</label><input type="number" id="upHours" min="0" /></div>
            </div>
            <p class="muted">开启「自动」的容器将按此间隔自动拉取新镜像并重建。0 天 + 0 小时视为每周。</p>`,
          foot: [
            { label: '取消', onClick: (c) => c() },
            { label: '保存', cls: 'primary', icon: 'i-check', onClick: async (c) => {
              try {
                await API.put('/api/updates/settings', {
                  update_interval_days: +m.mask.querySelector('#upDays').value || 0,
                  update_interval_hours: +m.mask.querySelector('#upHours').value || 0,
                });
                UI.toast('设置已保存', 'ok'); c(); load();
              } catch (e) { UI.toast(e.message, 'error'); }
            } },
          ],
        });
        m.mask.querySelector('#upDays').value = cur.update_interval_days ?? 7;
        m.mask.querySelector('#upHours').value = cur.update_interval_hours ?? 0;
      });

      await load();
    },
    unmount() {
      if (progressHandler) { WS.off('update_progress', progressHandler); progressHandler = null; }
      const bar = document.getElementById('wsProgress');
      if (bar) bar.hidden = true;
    },
  };
})();
