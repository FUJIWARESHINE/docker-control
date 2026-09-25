// 仪表板：统计卡 + 容器资源横条 + 实时日志
(function () {
  'use strict';

  let statsTimer = null;
  let logWSHandler = null;

  window.Views = window.Views || {};
  Views.dashboard = {
    title: '仪表板',
    async mount(content) {
      content.innerHTML = `
        <div class="page">
          <div class="grid c4" id="statCards" style="margin-bottom:16px"></div>
          <div class="grid" style="grid-template-columns:1fr;gap:14px">
            <section>
              <div class="toolbar" style="margin-bottom:10px">
                <h4 style="font-size:12px;letter-spacing:1px;text-transform:uppercase;color:var(--fg-mute)">容器资源占用</h4>
                <div class="spacer"></div>
                <span class="pill" id="dashRefreshTip">每 4 秒自动刷新</span>
              </div>
              <div class="table-card"><div class="tscroll">
                <table class="tbl"><thead><tr>
                  <th>容器</th><th style="width:22%">CPU</th><th style="width:26%">内存</th><th style="width:14%">下行</th><th style="width:14%">上行</th>
                </tr></thead><tbody id="statRows"></tbody></table>
              </div></div>
            </section>
            <section>
              <div class="toolbar" style="margin-bottom:10px">
                <h4 style="font-size:12px;letter-spacing:1px;text-transform:uppercase;color:var(--fg-mute)">实时日志</h4>
                <div class="spacer"></div>
                <button type="button" class="btn tiny" id="btnDashClearLog"><span>清屏</span></button>
              </div>
              <div class="logview" id="dashLog" style="max-height:300px;min-height:160px"></div>
            </section>
          </div>
        </div>`;

      const rows = content.querySelector('#statRows');
      const log = content.querySelector('#dashLog');
      content.querySelector('#btnDashClearLog').addEventListener('click', () => log.textContent = '');

      logWSHandler = (d) => {
        const line = document.createElement('div');
        const ts = document.createElement('span'); ts.className = 'ts';
        ts.textContent = `[${d.timestamp || ''}] [${d.level || ''}] `;
        line.appendChild(ts);
        line.appendChild(document.createTextNode(d.message || ''));
        log.appendChild(line);
        while (log.children.length > 200) log.firstChild.remove();
        log.scrollTop = log.scrollHeight;
      };
      WS.on('log', logWSHandler);
      // 初始历史日志
      API.get('/api/logs').then(s => {
        (s.data || []).slice(-40).forEach(d => logWSHandler(d));
      }).catch(() => { });

      const loadStats = async () => {
        try {
          const [csR, imgsR, netsR, volsR, statsR] = await Promise.allSettled([
            API.get('/api/containers'),
            API.get('/api/images'),
            API.get('/api/networks'),
            API.get('/api/volumes'),
            API.get('/api/stats'),
          ]);
          const val = (r, def) => (r.status === 'fulfilled' ? r.value : def);
          const list = val(csR, { data: [] }).data || [];
          const running = list.filter(c => c.state === 'running').length;
          renderCards(content, {
            containers: list.length, running,
            images: (val(imgsR, { data: [] }).data || []).length,
            networks: (val(netsR, { data: [] }).data || []).length,
            volumes: (val(volsR, { data: [] }).data || []).length,
            ports: list.reduce((n, c) => n + (c.ports || []).filter(p => p.host_port).length, 0),
          });
          renderStatRows(rows, list, val(statsR, { stats: [] }).stats || []);
        } catch (e) { /* 静默 */ }
      };
      await loadStats();
      statsTimer = setInterval(loadStats, 4000);
    },
    unmount() {
      clearInterval(statsTimer);
      if (logWSHandler) WS.off('log', logWSHandler);
      statsTimer = null;
    },
  };

  function renderCards(content, n) {
    const cards = [
      ['容器', n.containers, 'i-box', `${n.running} 运行中`],
      ['镜像', n.images, 'i-layers', ''],
      ['网络', n.networks, 'i-net', ''],
      ['存储卷', n.volumes, 'i-db', ''],
    ];
    const box = content.querySelector('#statCards');
    box.innerHTML = '';
    cards.forEach(([lbl, num, icon, extra]) => {
      const el = document.createElement('div');
      el.className = 'stat-card';
      el.innerHTML = `<svg><use href="#${icon}"/></svg><div class="num"></div><div class="lbl"></div>`;
      el.querySelector('.num').textContent = num;
      el.querySelector('.lbl').textContent = extra ? `${lbl} · ${extra}` : lbl;
      box.appendChild(el);
    });
  }

  function renderStatRows(rows, list, stats) {
    const byId = {};
    stats.forEach(s => byId[s.container_id] = s);
    if (!rows.dataset.built || rows.dataset.names !== JSON.stringify(list.map(c => c.id))) {
      rows.innerHTML = '';
      list.filter(c => c.state === 'running').forEach(c => {
        const tr = document.createElement('tr');
        tr.innerHTML = `
          <td class="name">${UI.esc(c.alias || c.name)}</td>
          <td><div class="meter"><div class="m-bar"><i></i></div></div></td>
          <td><div class="meter"><div class="m-bar"><i></i></div></div></td>
          <td class="dim rx">-</td>
          <td class="dim tx">-</td>`;
        rows.appendChild(tr);
      });
      rows.dataset.built = '1';
      rows.dataset.names = JSON.stringify(list.map(c => c.id));
    }
    const trs = rows.querySelectorAll('tr');
    let i = 0;
    list.filter(c => c.state === 'running').forEach(c => {
      const s = byId[c.id] || {};
      const tr = trs[i++];
      if (!tr) return;
      const cpu = tr.querySelectorAll('.m-bar > i')[0];
      const mem = tr.querySelectorAll('.m-bar > i')[1];
      const cpuV = +s.cpu_percent || 0;
      const memV = +s.mem_percent || 0;
      cpu.style.width = Math.min(100, cpuV) + '%';
      cpu.classList.toggle('hot', cpuV > 60);
      mem.style.width = Math.min(100, memV) + '%';
      mem.classList.toggle('hot', memV > 80);
      tr.children[1].querySelector('.m-head') || null;
      tr.querySelector('.rx').textContent = UI.fmtRate(s.net_rx_rate);
      tr.querySelector('.tx').textContent = UI.fmtRate(s.net_tx_rate);
      // CPU / 内存数值放在 meter 头
      ensureMeterHead(tr.children[1], 'CPU ' + UI.fmtPct(cpuV));
      ensureMeterHead(tr.children[2], UI.fmtBytes(s.mem_usage) + ' · ' + UI.fmtPct(memV));
    });
  }

  function ensureMeterHead(td, text) {
    let head = td.querySelector('.m-head');
    const meter = td.querySelector('.meter');
    if (!head) {
      head = document.createElement('div');
      head.className = 'm-head';
      meter.insertBefore(head, meter.firstChild);
    }
    head.textContent = text;
  }
})();
