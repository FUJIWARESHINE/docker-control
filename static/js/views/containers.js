// 容器页：列表 / 批量操作 / 详情抽屉（概览/日志/统计/inspect/compose）
(function () {
  'use strict';

  let list = [];
  let followTimer = null;

  window.Views = window.Views || {};
  Views.containers = {
    title: '容器管理',
    async mount(content) {
      content.innerHTML = `
        <div class="page">
          <div class="toolbar">
            <div class="searchbox"><svg><use href="#i-search"/></svg><input id="ctSearch" placeholder="搜索容器 / 镜像 / 别名" /></div>
            <span class="pill" id="ctCount"></span>
            <div class="spacer"></div>
            <button type="button" class="btn" id="ctBtnStart"><svg><use href="#i-play"/></svg><span>启动</span></button>
            <button type="button" class="btn" id="ctBtnStop"><svg><use href="#i-stop"/></svg><span>停止</span></button>
            <button type="button" class="btn" id="ctBtnRestart"><svg><use href="#i-restart"/></svg><span>重启</span></button>
            <button type="button" class="btn flow-danger" id="ctBtnDelete"><svg><use href="#i-trash"/></svg><span>删除</span></button>
            <button type="button" class="btn primary" id="ctBtnRefresh"><svg><use href="#i-refresh"/></svg><span>刷新</span></button>
          </div>
          <div class="table-card"><div class="tscroll" style="max-height:calc(100vh - 210px)">
            <table class="tbl"><thead><tr>
              <th style="width:34px"><span class="check" id="ctAll"></span></th>
              <th>名称</th><th>镜像</th><th>状态</th><th>端口</th><th>更新</th><th>创建时间</th><th style="text-align:right">操作</th>
            </tr></thead><tbody id="ctRows"></tbody></table>
          </div></div>
        </div>`;

      const rows = content.querySelector('#ctRows');
      const search = content.querySelector('#ctSearch');
      search.addEventListener('input', UI.debounce(render, 200));

      content.querySelector('#ctAll').addEventListener('click', function () {
        this.classList.toggle('on');
        const on = this.classList.contains('on');
        rows.querySelectorAll('.check').forEach(c => c.classList.toggle('on', on));
        renderSel();
      });

      const bind = (id, action, needConfirm) => content.querySelector(id).addEventListener('click', async () => {
        const names = selected();
        if (!names.length) return UI.toast('请先勾选容器', 'warn');
        if (needConfirm) {
          const okGo = await UI.confirm(`批量${action === 'delete' ? '删除' : action}`, `对 ${names.length} 个容器执行「${action}」？${action === 'delete' ? '该操作不可撤销。' : ''}`, action === 'delete');
          if (!okGo) return;
        }
        for (const n of names) {
          try {
            if (action === 'delete') await API.post(`/api/containers/${encodeURIComponent(n)}/delete`, { container_name: n });
            else await API.post(`/api/containers/${encodeURIComponent(n)}/${action}`, { container_name: n });
          } catch (e) { UI.toast(`${n}: ${e.message}`, 'error'); }
        }
        UI.toast(`批量${action}完成`, 'ok');
        load();
      });
      bind('#ctBtnStart', 'start');
      bind('#ctBtnStop', 'stop');
      bind('#ctBtnRestart', 'restart');
      bind('#ctBtnDelete', 'delete', true);

      content.querySelector('#ctBtnRefresh').addEventListener('click', load);

      rows.addEventListener('click', (e) => {
        const tr = e.target.closest('tr');
        if (!tr) return;
        const name = tr.dataset.name;
        const btn = e.target.closest('button[data-op]');
        if (btn) {
          const op = btn.dataset.op;
          if (op === 'detail') return openDetail(name);
          rowAction(name, op);
          return;
        }
        if (e.target.closest('.check')) return;
      });

      async function load() {
        UI.loading(rows, '加载容器列表…');
        try {
          const s = await API.get('/api/containers?all=1');
          list = s.data || [];
          render();
          content.querySelector('#ctCount').innerHTML = `<b>${list.length}</b> 个容器`;
        } catch (e) {
          UI.fillEmpty(rows, 'i-warn', '加载失败', e.message);
        }
      }

      function filtered() {
        const q = (search.value || '').toLowerCase().trim();
        if (!q) return list;
        return list.filter(c =>
          (c.name || '').toLowerCase().includes(q) ||
          (c.alias || '').toLowerCase().includes(q) ||
          (c.image || '').toLowerCase().includes(q));
      }

      function render() {
        const data = filtered();
        rows.innerHTML = '';
        if (!data.length) {
          UI.fillEmpty(rows, 'i-box', list.length ? '没有匹配的容器' : '当前主机上没有可管理的容器', '');
          return;
        }
        data.forEach(c => {
          const tr = document.createElement('tr');
          tr.dataset.name = c.name;
          const ports = (c.ports || []).filter(p => p.host_port)
            .map(p => `${p.host_port}→${p.container_port}/${p.protocol}`).join('  ') || '-';
          tr.innerHTML = `
            <td><span class="check"></span></td>
            <td class="name" style="cursor:pointer">
              ${UI.esc(c.alias || c.name)}
              ${c.compose_project ? `<span class="badge brand" style="margin-left:6px">${UI.esc(c.compose_project)}</span>` : ''}
            </td>
            <td class="mono">${UI.esc(c.image)}</td>
            <td>${UI.stateBadge(c.state)}</td>
            <td class="mono">${UI.esc(ports)}</td>
            <td>${UI.updBadge(c.update_status, c.has_update)}${c.auto_update ? ' <span class="badge cyan">自动</span>' : ''}</td>
            <td class="dim">${UI.fmtTime(c.created)}</td>
            <td><div class="actions">
              <button type="button" class="icon-btn" data-op="start" title="启动"><svg><use href="#i-play"/></svg></button>
              <button type="button" class="icon-btn" data-op="stop" title="停止"><svg><use href="#i-stop"/></svg></button>
              <button type="button" class="icon-btn" data-op="restart" title="重启"><svg><use href="#i-restart"/></svg></button>
              <button type="button" class="icon-btn" data-op="detail" title="详情"><svg><use href="#i-info"/></svg></button>
              <button type="button" class="icon-btn del" data-op="delete" title="删除"><svg><use href="#i-trash"/></svg></button>
            </div></td>`;
          tr.querySelector('.name').addEventListener('click', () => openDetail(c.name));
          tr.querySelector('.check').addEventListener('click', function () {
            this.classList.toggle('on');
          });
          rows.appendChild(tr);
        });
      }

      function rowsHandler(e) {
        const btn = e.target.closest('button[data-op]');
        const tr = e.target.closest('tr');
        if (!tr) return;
        const name = tr.dataset.name;
        if (btn) {
          const op = btn.dataset.op;
          if (op === 'detail') openDetail(name);
          else rowAction(name, op);
        }
      }

      async function rowAction(name, op) {
        if (op === 'delete') {
          const okGo = await UI.confirm('删除容器', `确定删除容器「${name}」？该操作不可撤销。`, true);
          if (!okGo) return;
        }
        try {
          if (op === 'delete') await API.post(`/api/containers/${encodeURIComponent(name)}/delete`, { container_name: name });
          else await API.post(`/api/containers/${encodeURIComponent(name)}/${op}`, { container_name: name });
          UI.toast(`${name} ${op} 成功`, 'ok');
          load();
        } catch (e) { UI.toast(e.message, 'error'); }
      }

      function selected() {
        return [...rows.querySelectorAll('tbody tr, tr')].
          filter(tr => tr.querySelector('.check') && tr.querySelector('.check').classList.contains('on')).
          map(tr => tr.dataset.name).
          filter(Boolean);
      }
      function renderSel() { }

      await load();
    },
    unmount() {
      clearInterval(followTimer);
      followTimer = null;
    },
  };

  // ---------- 详情抽屉 ----------

  async function openDetail(name) {
    const d = UI.drawer({ title: name, sub: '容器详情' });
    d.setTabs(['概览', '日志', '统计', 'Inspect', 'Compose'], (i) => showTab(i));
    d.setFoot([
      { label: '启动', icon: 'i-play', onClick: () => act('start') },
      { label: '停止', icon: 'i-stop', onClick: () => act('stop') },
      { label: '重启', icon: 'i-restart', cls: 'warn', onClick: () => act('restart') },
      { label: '删除', icon: 'i-trash', cls: 'flow-danger', onClick: async () => {
        if (await UI.confirm('删除容器', `确定删除「${name}」？`, true)) { act('delete'); d.close(); }
      } },
    ]);
    let cur = 0;

    async function act(op) {
      try {
        await API.post(`/api/containers/${encodeURIComponent(name)}/${op}`, { container_name: name });
        UI.toast(`${name} ${op} 成功`, 'ok');
      } catch (e) { UI.toast(e.message, 'error'); }
    }

    async function showTab(i) {
      cur = i;
      clearInterval(followTimer);
      const body = d.body;
      UI.loading(body);
      try {
        if (i === 0) await tabOverview(body);
        else if (i === 1) await tabLogs(body, name);
        else if (i === 2) await tabStats(body, name);
        else if (i === 3) {
          const s = await API.get(`/api/containers/${encodeURIComponent(name)}/inspect`);
          body.innerHTML = `<div class="logview" style="min-height:200px;max-height:calc(100vh - 220px)">${UI.esc(JSON.stringify(s.data, null, 2))}</div>`;
        } else if (i === 4) {
          const s = await API.get(`/api/containers/${encodeURIComponent(name)}/compose`);
          body.innerHTML = `
            <div class="field"><label>docker-compose.yml（由容器配置自动生成）</label>
            <textarea style="min-height:300px" readonly></textarea></div>`;
          body.querySelector('textarea').value = s.data.compose;
          body.querySelector('textarea').addEventListener('click', function () { this.select(); });
        }
      } catch (e) {
        body.innerHTML = `<div class="empty"><h3>加载失败</h3><p>${UI.esc(e.message)}</p></div>`;
      }
    }

    async function tabOverview(body) {
      const s = await API.get(`/api/containers/${encodeURIComponent(name)}/inspect`);
      const c = s.data;
      const rp = c.restart_policy || {};
      const nets = ((c.network || {}).networks) || [];
      body.innerHTML = `
        <div class="grid c2">
          <div class="card"><h4>基本信息</h4>
            <dl class="kv" style="margin-top:10px">
              <dt>名称</dt><dd class="name"></dd>
              <dt>ID</dt><dd class="mono"></dd>
              <dt>状态</dt><dd></dd>
              <dt>镜像</dt><dd class="mono"></dd>
              <dt>主机名</dt><dd></dd>
              <dt>工作目录</dt><dd></dd>
              <dt>重启策略</dt><dd></dd>
            </dl></div>
          <div class="card"><h4>端口与网络</h4><div class="port-net" style="margin-top:10px"></div></div>
        </div>
        <div class="card"><h4>挂载卷</h4><div class="mounts" style="margin-top:10px"></div></div>
        <div class="card"><h4>环境变量（${(c.env || []).length}）</h4><div class="logview envs" style="max-height:180px;margin-top:10px"></div></div>`;
      const kv = body.querySelectorAll('dd');
      kv[0].textContent = c.alias || c.name || '';
      kv[1].textContent = c.id || '';
      kv[2].innerHTML = UI.stateBadge(c.state);
      kv[3].textContent = c.image || '';
      kv[4].textContent = c.hostname || '-';
      kv[5].textContent = c.working_dir || '-';
      kv[6].textContent = (rp.Name || 'no') + ((rp.MaximumRetryCount || 0) > 0 ? ` (max ${rp.MaximumRetryCount})` : '');
      const pn = body.querySelector('.port-net');
      const ports = c.ports || [];
      pn.innerHTML = (ports.length ? '' : '<p class="muted">无端口映射</p>') +
        ports.map(p => `<div class="mono" style="padding:2px 0">${UI.esc((p.host_ip || '0.0.0.0') + ':' + (p.host_port || '-'))} → ${UI.esc(String(p.container_port) + '/' + p.protocol)}</div>`).join('') +
        `<div style="margin-top:10px">` + nets.map(n => `<span class="badge brand" style="margin:2px">${UI.esc(n.name)} ${UI.esc(n.ip_address || '')}</span>`).join('') + '</div>';
      const mounts = c.mounts || [];
      body.querySelector('.mounts').innerHTML = mounts.length ?
        mounts.map(m => `<div class="mono" style="padding:3px 0;border-bottom:1px solid var(--stroke)">${UI.esc(m.source || m.name || '')} <span class="dim">→ ${UI.esc(m.destination)} (${UI.esc(m.type)}${m.rw ? ', rw' : ', ro'})</span></div>`).join('') :
        '<p class="muted">无挂载</p>';
      body.querySelector('.envs').textContent = (c.env || []).join('\n');
    }

    async function tabLogs(body, cname) {
      body.innerHTML = `
        <div class="toolbar" style="margin-bottom:10px">
          <select id="ctLogTail" style="height:29px;border-radius:8px;border:1px solid var(--stroke);background:rgba(0,0,0,.3);color:var(--fg);font-family:inherit">
            <option value="100">最近 100 行</option><option value="300" selected>最近 300 行</option><option value="1000">最近 1000 行</option>
          </select>
          <span class="check" id="ctLogFollow"></span><span class="dim">实时跟随</span>
          <div class="spacer"></div>
          <button type="button" class="btn tiny" id="ctLogCopy"><svg><use href="#i-copy"/></svg><span>复制</span></button>
        </div>
        <div class="logview" style="min-height:300px;max-height:calc(100vh - 260px)"></div>`;
      const view = body.querySelector('.logview');
      const tail = body.querySelector('#ctLogTail');
      const followChk = body.querySelector('#ctLogFollow');
      const loadLogs = async () => {
        const t = await API.text(`/api/containers/${encodeURIComponent(cname)}/logs?tail=${tail.value}&follow=0`);
        view.textContent = t || '(无日志)';
        view.scrollTop = view.scrollHeight;
      };
      tail.addEventListener('change', loadLogs);
      body.querySelector('#ctLogCopy').addEventListener('click', () => {
        navigator.clipboard.writeText(view.textContent).then(() => UI.toast('已复制', 'ok'));
      });
      followChk.addEventListener('click', function () {
        this.classList.toggle('on');
        clearInterval(followTimer);
        if (this.classList.contains('on')) followTimer = setInterval(loadLogs, 3000);
      });
      await loadLogs();
    }

    async function tabStats(body, cname) {
      body.innerHTML = `
        <div class="grid c2" style="margin-bottom:14px">
          <div class="card"><h4>CPU</h4><div class="meter" style="margin-top:10px"><div class="m-head cpu-t">-</div><div class="m-bar" style="height:8px"><i></i></div></div></div>
          <div class="card"><h4>内存</h4><div class="meter" style="margin-top:10px"><div class="m-head mem-t">-</div><div class="m-bar" style="height:8px"><i></i></div></div></div>
          <div class="card"><h4>网络下行</h4><p class="num rx" style="font-size:22px;font-weight:800;margin-top:8px;color:var(--brand-2)">-</p></div>
          <div class="card"><h4>网络上行</h4><p class="num tx" style="font-size:22px;font-weight:800;margin-top:8px;color:var(--cyan)">-</p></div>
        </div>`;
      const cpuBar = body.querySelectorAll('.m-bar > i')[0];
      const memBar = body.querySelectorAll('.m-bar > i')[1];
      const tick = async () => {
        try {
          const s = await API.get('/api/stats');
          const st = (s.stats || []).find(x => x.name === cname);
          if (!st) return;
          body.querySelector('.cpu-t').textContent = UI.fmtPct(st.cpu_percent);
          body.querySelector('.mem-t').textContent = `${UI.fmtBytes(st.mem_usage)} · ${UI.fmtPct(st.mem_percent)}`;
          cpuBar.style.width = Math.min(100, +st.cpu_percent || 0) + '%';
          memBar.style.width = Math.min(100, +st.mem_percent || 0) + '%';
          body.querySelector('.rx').textContent = UI.fmtRate(st.net_rx_rate);
          body.querySelector('.tx').textContent = UI.fmtRate(st.net_tx_rate);
        } catch (e) { /* */ }
      };
      await tick();
      followTimer = setInterval(tick, 3000);
    }

    d.setTabsTrigger = null;
    await showTab(0);
    // tab 切换绑定（setTabs 已先声明）
    d.onClose(() => clearInterval(followTimer));
  }
})();
