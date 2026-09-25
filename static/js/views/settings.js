// 系统设置：修改密码 / 别名 / API Key / 操作日志 / 系统信息 / 备份 / 重启
(function () {
  'use strict';

  window.Views = window.Views || {};
  Views.settings = {
    title: '系统设置',
    async mount(content) {
      content.innerHTML = `<div class="page">
        <div class="grid c2" style="align-items:start">
          <section style="display:flex;flex-direction:column;gap:14px">
            <div class="card">
              <h4>修改密码</h4>
              <div class="field" style="margin-top:10px"><label>原密码</label><input type="password" id="stOld" /></div>
              <div class="field" style="margin-top:10px"><label>新密码（至少 6 位）</label><input type="password" id="stNew" /></div>
              <button type="button" class="btn primary" id="stBtnPw" style="margin-top:12px"><svg><use href="#i-key"/></svg><span>确认修改</span></button>
            </div>
            <div class="card">
              <h4>系统信息</h4>
              <dl class="kv" id="stInfo" style="margin-top:10px"></dl>
              <div style="display:flex;gap:8px;margin-top:14px">
                <button type="button" class="btn" id="stBtnBackup"><svg><use href="#i-down"/></svg><span>下载配置备份</span></button>
                <button type="button" class="btn flow-danger" id="stBtnRestart"><svg><use href="#i-restart"/></svg><span>重启面板</span></button>
              </div>
            </div>
            <div class="card">
              <h4>API Key</h4>
              <p class="muted" style="margin:4px 0 10px">通过请求头 <span class="mono">X-API-Key</span> 调用面板 API，无需登录。</p>
              <div id="stKeys" style="display:flex;flex-direction:column;gap:6px"></div>
              <button type="button" class="btn small primary" id="stBtnKey" style="margin-top:10px"><svg><use href="#i-plus"/></svg><span>生成新 Key</span></button>
            </div>
          </section>
          <section style="display:flex;flex-direction:column;gap:14px">
            <div class="card">
              <h4>容器别名</h4>
              <p class="muted" style="margin:4px 0 10px">为容器设置显示别名（JSON：容器名 → {alias}）。</p>
              <div class="field"><textarea id="stAlias" style="min-height:150px"></textarea></div>
              <button type="button" class="btn small primary" id="stBtnAlias" style="margin-top:10px"><svg><use href="#i-check"/></svg><span>保存别名</span></button>
            </div>
            <div class="card">
              <h4>操作日志</h4>
              <div class="logview" id="stLogs" style="max-height:340px;margin-top:10px"></div>
            </div>
          </section>
        </div></div>`;

      // 密码
      content.querySelector('#stBtnPw').addEventListener('click', async () => {
        const oldpw = content.querySelector('#stOld').value;
        const newpw = content.querySelector('#stNew').value;
        if (newpw.length < 6) return UI.toast('新密码至少 6 位', 'warn');
        try { await API.post('/api/auth/password', { old_password: oldpw, new_password: newpw }); UI.toast('密码修改成功', 'ok'); }
        catch (e) { UI.toast(e.message, 'error'); }
      });

      // 系统信息 + 备份 + 重启
      API.get('/api/system/info').then(s => {
        const d = s.data || {};
        const kv = content.querySelector('#stInfo');
        const rows = [['面板版本', 'v' + d.version], ['Docker 版本', d.docker_version || '-'], ['Docker OS/Arch', (d.docker_os || '-') + '/' + (d.docker_arch || '-')]];
        rows.forEach(([k, v]) => {
          const dt = document.createElement('dt'); dt.textContent = k;
          const dd = document.createElement('dd'); dd.textContent = v;
          kv.appendChild(dt); kv.appendChild(dd);
        });
      }).catch(() => { });
      content.querySelector('#stBtnBackup').addEventListener('click', async () => {
        try {
          const blob = await API.blob('/api/backup');
          const a = document.createElement('a');
          a.href = URL.createObjectURL(blob);
          a.download = 'docker-control-backup.json';
          a.click();
          URL.revokeObjectURL(a.href);
        } catch (e) { UI.toast(e.message, 'error'); }
      });
      content.querySelector('#stBtnRestart').addEventListener('click', async () => {
        if (!await UI.confirm('重启面板', '面板进程将退出并由容器编排自动拉起，确认继续？', true)) return;
        try { await API.post('/api/system/restart', {}); UI.toast('重启指令已提交', 'ok'); }
        catch (e) { UI.toast(e.message, 'error'); }
      });

      // API Key
      const keysBox = content.querySelector('#stKeys');
      async function loadKeys() {
        try {
          const s = await API.get('/api/apikeys');
          keysBox.innerHTML = '';
          (s.data || []).forEach(k => {
            const row = document.createElement('div');
            row.style.cssText = 'display:flex;align-items:center;gap:8px;padding:7px 10px;border:1px solid var(--stroke);border-radius:10px';
            row.innerHTML = `
              <span class="mono" style="flex:1;overflow:hidden;text-overflow:ellipsis;white-space:nowrap"></span>
              <span class="badge ${k.enabled ? 'ok' : 'idle'}">${k.enabled ? '启用' : '停用'}</span>
              <button type="button" class="icon-btn" title="复制"><svg><use href="#i-copy"/></svg></button>
              <button type="button" class="icon-btn" title="${k.enabled ? '停用' : '启用'}"><svg><use href="#i-check"/></svg></button>
              <button type="button" class="icon-btn del"><svg><use href="#i-trash"/></svg></button>`;
            row.querySelector('.mono').textContent = k.key.slice(0, 12) + '…' + k.key.slice(-4);
            const [bCopy, bToggle, bDel] = row.querySelectorAll('.icon-btn');
            bCopy.addEventListener('click', () => { navigator.clipboard.writeText(k.key).then(() => UI.toast('Key 已复制', 'ok')); });
            bToggle.addEventListener('click', async () => { await API.post(`/api/apikeys/${k.id}/toggle`, {}); loadKeys(); });
            bDel.addEventListener('click', async () => { await API.del(`/api/apikeys/${k.id}`); UI.toast('已删除', 'ok'); loadKeys(); });
            keysBox.appendChild(row);
          });
          if (!(s.data || []).length) keysBox.innerHTML = '<p class="muted">暂无 API Key</p>';
        } catch (e) { /* */ }
      }
      content.querySelector('#stBtnKey').addEventListener('click', async () => {
        if (!await UI.confirm('生成 API Key', '生成新的 API Key？（生成后请立即复制保存）')) return;
        try {
          const s = await API.post('/api/apikeys', {});
          const m = UI.modal({
            title: '新 API Key',
            body: `<div class="logview" style="user-select:all"></div>`,
            foot: [{ label: '关闭', onClick: (c) => c() }],
          });
          m.mask.querySelector('.logview').textContent = s.data.key;
          loadKeys();
        } catch (e) { UI.toast(e.message, 'error'); }
      });
      await loadKeys();

      // 别名
      const aliasBox = content.querySelector('#stAlias');
      API.get('/api/aliases').then(s => aliasBox.value = JSON.stringify(s.data || {}, null, 2)).catch(() => { });
      content.querySelector('#stBtnAlias').addEventListener('click', async () => {
        let obj;
        try { obj = JSON.parse(aliasBox.value || '{}'); }
        catch (e) { return UI.toast('JSON 格式错误: ' + e.message, 'error'); }
        try { await API.put('/api/aliases', obj); UI.toast('别名已保存', 'ok'); }
        catch (e) { UI.toast(e.message, 'error'); }
      });

      // 操作日志
      const logs = content.querySelector('#stLogs');
      API.get('/api/logs').then(s => {
        logs.textContent = (s.data || []).map(l => `[${l.timestamp}] [${l.level}] ${l.message}`).join('\n') || '(暂无日志)';
        logs.scrollTop = logs.scrollHeight;
      }).catch(() => { });
    },
  };
})();
