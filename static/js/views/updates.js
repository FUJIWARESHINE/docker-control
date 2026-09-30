// 更新中心：检查更新 / 一键更新（WS 进度）/ 容器自动更新 / 镜像定时清理
(function () {
  'use strict';

  let progressHandler = null;
  let autoList = [];

  window.Views = window.Views || {};
  Views.updates = {
    title: '更新中心',
    async mount(content) {
      content.innerHTML = `<div class="page">
        <div class="grid c2" style="align-items:stretch">
          <div class="card">
            <h4>容器自动更新</h4>
            <p class="muted" style="margin:4px 0 10px">
              在「容器」页点每行左侧的 <b>★ 星标</b> 即可收藏，被收藏的容器才会跟随下面的间隔自动更新；
              没收藏的一律不动。
            </p>
            <div class="field-row">
              <div class="field"><label>更新间隔（天）</label><input type="number" id="upDays" min="0" /></div>
              <div class="field"><label>额外小时</label><input type="number" id="upHours" min="0" /></div>
            </div>
            <p class="muted" style="margin-top:8px">
              已收藏 <b id="upAutoCount">0</b> 个容器 · 0 天 + 0 小时视为<b>每周</b>
            </p>
            <div id="upAutoState" class="muted" style="margin-top:6px"></div>
            <div style="display:flex;gap:8px;margin-top:12px;flex-wrap:wrap">
              <button type="button" class="btn primary" id="upSave"><svg><use href="#i-check"/></svg><span>保存设置</span></button>
              <button type="button" class="btn" id="upBtnPick"><svg><use href="#i-box"/></svg><span>去容器页收藏</span></button>
            </div>
          </div>

          <div class="card">
            <h4>镜像自动清理</h4>
            <p class="muted" style="margin:4px 0 10px">
              定时删除<b>没有被任何容器使用</b>的镜像。带 <span class="mono">-bak</span> 后缀的更新回滚镜像始终保留。
            </p>
            <label class="row" style="display:flex;align-items:center;gap:8px;margin-bottom:10px">
              <span class="check" id="prEnabled"></span><span>启用定时清理</span>
            </label>
            <div class="field-row">
              <div class="field"><label>清理间隔（小时）</label><input type="number" id="prHours" min="1" /></div>
              <div class="field"><label>清理范围</label>
                <select id="prScope">
                  <option value="dangling">仅悬空镜像（安全）</option>
                  <option value="all">未使用的镜像（含已打标签）</option>
                </select>
              </div>
            </div>
            <div id="prState" class="muted" style="margin-top:6px"></div>
            <div style="display:flex;gap:8px;margin-top:12px;flex-wrap:wrap">
              <button type="button" class="btn primary" id="prSave"><svg><use href="#i-check"/></svg><span>保存设置</span></button>
              <button type="button" class="btn" id="prNow"><svg><use href="#i-broom"/></svg><span>立即清理一次</span></button>
            </div>
          </div>
        </div>

        <div class="toolbar" style="margin-top:16px">
          <span class="pill" id="upLast">从未检查</span>
          <div class="spacer"></div>
          <button type="button" class="btn primary" id="upBtnCheck"><svg><use href="#i-refresh"/></svg><span>检查更新</span></button>
        </div>
        <div class="table-card"><div class="tscroll" style="max-height:calc(100vh - 250px)">
          <table class="tbl"><thead><tr>
            <th style="width:34px" title="收藏 = 加入自动更新"></th>
            <th>容器</th><th>镜像</th><th>本地 digest</th><th>远端 digest</th><th>状态</th><th style="text-align:right">操作</th>
          </tr></thead><tbody id="upRows"></tbody></table>
        </div></div>
        <p class="muted" style="margin-top:12px;font-size:11px">
          检查原理：对比本地与远端 registry 的镜像 manifest digest。私有仓库或配置了镜像加速时结果会显示「未知」。
        </p></div>`;

      const rows = content.querySelector('#upRows');
      const cardEl = rows.closest('.table-card');
      const prChk = content.querySelector('#prEnabled');
      let lastResults = [];

      // WS 更新进度 → 顶部进度条
      const bar = document.getElementById('wsProgress');
      const barFill = bar ? bar.querySelector('i') : null;
      progressHandler = (p) => {
        if (!bar) return;
        bar.hidden = false;
        barFill.style.width = (p.percentage || 0) + '%';
        document.getElementById('pageSub').textContent = `[${p.container}] ${p.message || ''}`;
        // Skipped = 拉取后发现镜像没变，容器根本没重启，也算一次终结
        if (p.status === 'success' || p.status === 'error' || p.status === 'Skipped') {
          setTimeout(() => { bar.hidden = true; barFill.style.width = '0'; document.getElementById('pageSub').textContent = ''; }, 2500);
          UI.toast(p.message || (p.status === 'success' ? '更新完成' : '更新失败'),
            p.status === 'success' ? 'ok' : (p.status === 'error' ? 'error' : 'info'));
        }
      };
      WS.on('update_progress', progressHandler);

      async function load() {
        try {
          const cfg = await API.get('/api/updates/settings');
          const d = cfg.data || {};
          autoList = d.auto_update_containers || [];

          // 容器自动更新卡片
          content.querySelector('#upDays').value = d.update_interval_days ?? 7;
          content.querySelector('#upHours').value = d.update_interval_hours ?? 0;
          content.querySelector('#upAutoCount').textContent = autoList.length;
          content.querySelector('#upAutoState').innerHTML = autoList.length
            ? `上次运行：${UI.fmtTime(d.auto_update_last_run)} · 下次运行：<b>${UI.fmtTime(d.auto_update_next_run)}</b>`
            : '<span class="dim">还没有收藏任何容器，调度器处于待命状态。</span>';

          // 镜像自动清理卡片
          const on = !!d.auto_prune_enabled;
          prChk.classList.toggle('on', on);
          content.querySelector('#prHours').value = d.auto_prune_interval_hours || 24;
          content.querySelector('#prScope').value = d.auto_prune_all ? 'all' : 'dangling';
          content.querySelector('#prState').innerHTML = on
            ? `上次运行：${UI.fmtTime(d.auto_prune_last_run)} · 释放 <b>${d.auto_prune_last_freed ? UI.fmtBytes(d.auto_prune_last_freed) : '-'}</b> · 下次运行：<b>${UI.fmtTime(d.auto_prune_next_run)}</b>`
            : '<span class="dim">未启用，磁盘上的旧镜像会一直留着。</span>';

          lastResults = Array.isArray(d.last_results) ? d.last_results : [];
          const last = d.last_check ? '上次检查: ' + UI.fmtTime(d.last_check) : '从未检查';
          content.querySelector('#upLast').innerHTML = last +
            (autoList.length ? ` · <b>${autoList.length}</b> 个自动更新` : '');
        } catch (e) { /* */ }
      }

      // renderResults 把检查结果铺成表格。刷新页面时用落盘的上次结果直接回显，
      // 点「检查更新」时用新结果覆盖。
      function renderResults(list) {
        cardEl.innerHTML = `<div class="tscroll" style="max-height:calc(100vh - 250px)">
          <table class="tbl"><thead><tr>
            <th style="width:34px" title="收藏 = 加入自动更新"></th>
            <th>容器</th><th>镜像</th><th>本地 digest</th><th>远端 digest</th><th>状态</th><th style="text-align:right">操作</th>
          </tr></thead><tbody id="upRows"></tbody></table></div>`;
        const tbody = cardEl.querySelector('#upRows');
        if (!list.length) { UI.fillEmpty(cardEl, 'i-box', '没有容器', ''); return; }
        list.forEach(r => {
          const tr = document.createElement('tr');
          const short = (d) => d ? `<span class="mono dim">${UI.esc(String(d).slice(0, 19))}…</span>` : '<span class="dim">-</span>';
          const starred = autoList.includes(r.container_name);
          tr.innerHTML = `
            <td><button type="button" class="icon-btn star${starred ? ' on' : ''}" data-op="auto"
                  title="${starred ? '已收藏 · 点此取消自动更新' : '收藏 · 加入自动更新'}"><svg><use href="#i-star"/></svg></button></td>
            <td class="name">${UI.esc(r.container_name)}</td>
            <td class="mono dim">${UI.esc(r.image)}</td>
            <td>${short(r.local_digest)}</td>
            <td>${short(r.remote_digest)}</td>
            <td>${UI.updBadge(r.status, r.has_update)}</td>
            <td><div class="actions">
              <button type="button" class="btn tiny primary" data-op="apply" ${r.has_update ? '' : 'disabled'}><span>更新</span></button>
            </div></td>`;
          tr.querySelectorAll('button[data-op]').forEach(b => b.addEventListener('click', () => rowOp(b.dataset.op, r, b)));
          tbody.appendChild(tr);
        });
      }

      async function check() {
        const btn = content.querySelector('#upBtnCheck');
        btn.disabled = true;
        btn.querySelector('span').textContent = '检查中…';
        UI.loading(cardEl, '正在对比 registry digest，通常需要 5-30 秒…');
        try {
          const s = await API.post('/api/updates/check', {});
          await load();
          renderResults(s.data || []);
        } catch (e) { UI.toast(e.message, 'error'); }
        btn.disabled = false;
        btn.querySelector('span').textContent = '检查更新';
      }

      async function rowOp(op, r, btn) {
        if (op === 'apply') {
          if (!await UI.confirm('更新容器', `拉取新镜像并重建容器「${r.container_name}」？配置会保留，过程约数秒到数分钟。`, false)) return;
          try { await API.post(`/api/updates/${encodeURIComponent(r.container_name)}/apply`, {}); UI.toast('更新任务已启动', 'ok'); }
          catch (e) { UI.toast(e.message, 'error'); }
          return;
        }
        // 收藏切换
        const has = autoList.includes(r.container_name);
        try {
          await API.post(`/api/updates/${encodeURIComponent(r.container_name)}/auto`, { enabled: !has });
          UI.toast(has ? '已取消自动更新' : '已收藏，加入自动更新', has ? 'info' : 'ok');
          btn.classList.toggle('on', !has);
          btn.title = !has ? '已收藏 · 点此取消自动更新' : '收藏 · 加入自动更新';
          await load();
        } catch (e) { UI.toast(e.message, 'error'); }
      }

      content.querySelector('#upBtnCheck').addEventListener('click', check);
      content.querySelector('#upBtnPick').addEventListener('click', () => { location.hash = '#containers'; });

      content.querySelector('#upSave').addEventListener('click', async () => {
        const btn = content.querySelector('#upSave');
        btn.disabled = true;
        try {
          await API.put('/api/updates/settings', {
            update_interval_days: +content.querySelector('#upDays').value || 0,
            update_interval_hours: +content.querySelector('#upHours').value || 0,
          });
          UI.toast('自动更新间隔已保存', 'ok');
          await load();
        } catch (e) { UI.toast(e.message, 'error'); }
        btn.disabled = false;
      });

      prChk.addEventListener('click', () => prChk.classList.toggle('on'));

      content.querySelector('#prSave').addEventListener('click', async () => {
        const btn = content.querySelector('#prSave');
        btn.disabled = true;
        try {
          await API.put('/api/updates/settings', {
            auto_prune_enabled: prChk.classList.contains('on'),
            auto_prune_all: content.querySelector('#prScope').value === 'all',
            auto_prune_interval_hours: Math.max(1, +content.querySelector('#prHours').value || 24),
          });
          UI.toast('镜像清理设置已保存', 'ok');
          await load();
        } catch (e) { UI.toast(e.message, 'error'); }
        btn.disabled = false;
      });

      content.querySelector('#prNow').addEventListener('click', async () => {
        const all = content.querySelector('#prScope').value === 'all';
        const msg = all
          ? '删除所有没被容器使用的镜像（含已打标签的）？带 -bak 的回滚镜像会保留。'
          : '删除悬空镜像（没有标签的中间层）？这是安全操作。';
        if (!await UI.confirm('立即清理镜像', msg, all)) return;
        const btn = content.querySelector('#prNow');
        btn.disabled = true;
        try {
          const r = await API.post('/api/images/prune' + (all ? '?all=1' : ''), {});
          const d = r.data || {};
          UI.toast(`已删除 ${d.deleted || 0} 个镜像，释放 ${UI.fmtBytes(d.freed || 0)}`, 'ok');
        } catch (e) { UI.toast(e.message, 'error'); }
        btn.disabled = false;
      });

      await load();
      // 有上次检查结果就直接回显，避免表格只剩一行表头；从没检查过才引导去点按钮
      if (lastResults.length) {
        renderResults(lastResults);
      } else {
        UI.fillEmpty(cardEl, 'i-refresh', '还没有检查过更新',
          '点右上角「检查更新」开始对比本地与 registry 的镜像 digest');
      }
    },
    unmount() {
      if (progressHandler) { WS.off('update_progress', progressHandler); progressHandler = null; }
      const bar = document.getElementById('wsProgress');
      if (bar) bar.hidden = true;
    },
  };
})();
