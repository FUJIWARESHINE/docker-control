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
              <h4>镜像拉取代理</h4>
              <p class="muted" style="margin:4px 0 10px">镜像由宿主机 dockerd 拉取，此处配置会写入 dockerd 并重启它生效。<b>运行时由宿主机拉取</b>，面板不参与下载。</p>
              <label class="row" style="display:flex;align-items:center;gap:8px;margin-bottom:10px">
                <input type="checkbox" id="pxEnabled" /> <span>启用代理</span>
              </label>
              <div class="field"><label>HTTP 代理</label><input id="pxHttp" placeholder="http://127.0.0.1:7890" /></div>
              <div class="field" style="margin-top:10px"><label>HTTPS 代理</label><input id="pxHttps" placeholder="http://127.0.0.1:7890" /></div>
              <div class="field" style="margin-top:10px"><label>不走代理的地址（NO_PROXY，留空则自动附加镜像加速器）</label><input id="pxNo" placeholder="localhost,127.0.0.1,.internal" /></div>
              <div id="pxState" class="muted" style="margin-top:10px"></div>
              <div style="display:flex;gap:8px;margin-top:12px">
                <button type="button" class="btn primary" id="pxSave"><svg><use href="#i-check"/></svg><span>保存并生效</span></button>
                <button type="button" class="btn ghost" id="pxClear"><svg><use href="#i-trash"/></svg><span>清除代理</span></button>
              </div>
            </div>
            <div class="card">
              <h4>Telegram 机器人</h4>
              <p class="muted" style="margin:4px 0 10px">按钮式交互：发送 /start 弹出菜单，可查看状态、容器、项目、检查更新、重启服务。<b>写操作需二次确认</b>，且不会展示或操作面板自身。</p>
              <label class="row" style="display:flex;align-items:center;gap:8px;margin-bottom:10px">
                <input type="checkbox" id="tgEnabled" /> <span>启用机器人</span>
              </label>
              <div class="field"><label>Bot Token</label><input id="tgToken" placeholder="123456:ABC-DEF..." /></div>
              <div class="field" style="margin-top:10px"><label>Chat ID <span class="dim">（向 @userinfobot 发消息可获取）</span></label><input id="tgChat" placeholder="123456789" /></div>
              <div style="display:flex;gap:14px;margin-top:12px;flex-wrap:wrap">
                <label style="display:flex;align-items:center;gap:6px"><input type="checkbox" id="tgNotifyDown" /> <span>容器停止推送</span></label>
                <label style="display:flex;align-items:center;gap:6px"><input type="checkbox" id="tgNotifyUpdate" /> <span>更新可用推送</span></label>
                <label style="display:flex;align-items:center;gap:6px"><input type="checkbox" id="tgNotifyBoot" /> <span>面板启动推送</span></label>
              </div>
              <div id="tgState" class="muted" style="margin-top:10px"></div>
              <div style="display:flex;gap:8px;margin-top:12px">
                <button type="button" class="btn primary" id="tgSave"><svg><use href="#i-check"/></svg><span>保存配置</span></button>
                <button type="button" class="btn ghost" id="tgTest"><svg><use href="#i-log"/></svg><span>发送测试消息</span></button>
              </div>
              <p class="muted" style="margin:10px 0 0;font-size:11px">可用指令：/start /status /containers /projects /images /updates /version /proxy /logs &lt;容器名&gt;</p>
            </div>
            <div class="card">
              <h4>容器别名</h4>
              <p class="muted" style="margin:4px 0 10px">为容器设置显示别名（JSON：容器名 → {alias}）。</p>
              <div class="field"><textarea id="stAlias" style="min-height:120px"></textarea></div>
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

      // ---- 镜像拉取代理 ----
      const pxEnabled = content.querySelector('#pxEnabled');
      const pxHttp = content.querySelector('#pxHttp');
      const pxHttps = content.querySelector('#pxHttps');
      const pxNo = content.querySelector('#pxNo');
      const pxState = content.querySelector('#pxState');
      async function loadProxy() {
        try {
          const s = await API.get('/api/proxy');
          const d = s.data || {};
          pxEnabled.checked = !!d.enabled;
          pxHttp.value = d.http_proxy || '';
          pxHttps.value = d.https_proxy || '';
          pxNo.value = d.no_proxy || '';
          pxState.textContent = d.applied ? `配置文件已存在：${d.host_path || ''}` : '当前未配置代理（dockerd 直连拉取）';
        } catch (e) { pxState.textContent = '读取失败: ' + e.message; }
      }
      // 保存代理会重启 dockerd，面板自身也会被重启。后端已改为「先响应、后重启」，
      // 因此这里能正常拿到响应；随后轮询等待面板恢复并刷新状态。
      async function applyProxy(payload, okMsg) {
        let restarting = false;
        try {
          const s = await API.post('/api/proxy', payload);
          restarting = !!s.restarting;
          UI.toast(s.message || okMsg, 'ok');
        } catch (e) {
          // 网络中断：面板可能已被重启带走
          restarting = true;
          UI.toast('配置已提交，Docker 正在重启…', 'warn');
        }
        if (!restarting) { loadProxy(); return; }
        pxState.textContent = '正在重启 Docker，等待恢复…';
        const retry = async (left) => {
          await new Promise(r => setTimeout(r, 2500));
          try {
            await loadProxy();
            UI.toast('Docker 已重启完成', 'ok');
          } catch (_) {
            if (left > 0) return retry(left - 1);
            pxState.textContent = '等待超时，请手动刷新页面';
          }
        };
        retry(10);
      }
      content.querySelector('#pxSave').addEventListener('click', async () => {
        if (!await UI.confirm('保存代理', '将写入 dockerd 配置并重启 Docker（所有容器会重启，restart=always 的会自动恢复）。继续？', true)) return;
        applyProxy({
          enabled: pxEnabled.checked,
          http_proxy: pxHttp.value.trim(),
          https_proxy: pxHttps.value.trim(),
          no_proxy: pxNo.value.trim(),
        }, '已保存');
      });
      content.querySelector('#pxClear').addEventListener('click', async () => {
        if (!await UI.confirm('清除代理', '删除代理配置并重启 Docker？', true)) return;
        applyProxy({ action: 'clear', enabled: false }, '已清除');
      });
      await loadProxy();

      // ---- Telegram 机器人 ----
      const tgEnabled = content.querySelector('#tgEnabled');
      const tgToken = content.querySelector('#tgToken');
      const tgChat = content.querySelector('#tgChat');
      const tgDown = content.querySelector('#tgNotifyDown');
      const tgUpd = content.querySelector('#tgNotifyUpdate');
      const tgBoot = content.querySelector('#tgNotifyBoot');
      const tgState = content.querySelector('#tgState');
      async function loadTG() {
        try {
          const s = await API.get('/api/telegram');
          const d = s.data || {};
          tgEnabled.checked = !!d.enabled;
          tgToken.value = d.token || '';
          tgChat.value = d.chat_id || '';
          tgDown.checked = !!d.notify_down;
          tgUpd.checked = !!d.notify_update;
          tgBoot.checked = !!d.notify_boot;
          tgState.textContent = '运行状态：' + (d.status || '未知');
        } catch (e) { tgState.textContent = '读取失败: ' + e.message; }
      }
      content.querySelector('#tgSave').addEventListener('click', async () => {
        try {
          await API.post('/api/telegram', {
            enabled: tgEnabled.checked,
            token: tgToken.value.trim(),
            chat_id: tgChat.value.trim(),
            notify_down: tgDown.checked,
            notify_update: tgUpd.checked,
            notify_boot: tgBoot.checked,
          });
          UI.toast('配置已保存', 'ok');
          setTimeout(loadTG, 1200);
        } catch (e) { UI.toast(e.message, 'error'); }
      });
      content.querySelector('#tgTest').addEventListener('click', async () => {
        try {
          await API.post('/api/telegram/test', {});
          UI.toast('测试消息已发送，请查看 Telegram', 'ok');
        } catch (e) { UI.toast(e.message, 'error'); }
      });
      await loadTG();
      const tgTimer = setInterval(loadTG, 15000);
      content.addEventListener('dc-unmount', () => clearInterval(tgTimer), { once: true });

      // ---- 容器别名 ----
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
