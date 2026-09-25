// 镜像页：列表 / 拉取 / tag / 删除 / 清理
(function () {
  'use strict';

  window.Views = window.Views || {};
  Views.images = {
    title: '镜像管理',
    async mount(content) {
      content.innerHTML = `
        <div class="page">
          <div class="toolbar">
            <div class="searchbox"><svg><use href="#i-search"/></svg><input id="imSearch" placeholder="搜索镜像" /></div>
            <span class="pill" id="imCount"></span>
            <div class="spacer"></div>
            <button type="button" class="btn" id="imBtnPrune"><svg><use href="#i-trash"/></svg><span>清理未使用</span></button>
            <button type="button" class="btn" id="imBtnLoad"><svg><use href="#i-up"/></svg><span>导入 tar</span></button>
            <button type="button" class="btn primary" id="imBtnPull"><svg><use href="#i-down"/></svg><span>拉取镜像</span></button>
          </div>
          <div class="table-card"><div class="tscroll" style="max-height:calc(100vh - 200px)">
            <table class="tbl"><thead><tr>
              <th>镜像标签</th><th>ID</th><th>大小</th><th>创建时间</th><th>使用状态</th><th style="text-align:right">操作</th>
            </tr></thead><tbody id="imRows"></tbody></table>
          </div></div>
        </div>`;

      const rows = content.querySelector('#imRows');
      const search = content.querySelector('#imSearch');
      let list = [];
      search.addEventListener('input', UI.debounce(render, 200));

      async function load() {
        try {
          const s = await API.get('/api/images');
          list = s.data || [];
          content.querySelector('#imCount').innerHTML = `<b>${list.length}</b> 个镜像`;
          render();
        } catch (e) { UI.toast(e.message, 'error'); }
      }

      function filtered() {
        const q = (search.value || '').toLowerCase().trim();
        if (!q) return list;
        return list.filter(i => (i.fullTag || '').toLowerCase().includes(q));
      }

      function render() {
        const data = filtered();
        rows.innerHTML = '';
        if (!data.length) return UI.fillEmpty(rows.closest('.table-card'), 'i-layers', '没有镜像', '拉取一个镜像试试');
        data.forEach(im => {
          const tr = document.createElement('tr');
          tr.innerHTML = `
            <td class="name mono">${UI.esc(im.fullTag)}</td>
            <td class="mono dim">${UI.esc(im.id)}</td>
            <td class="dim">${UI.fmtBytes(im.size)}</td>
            <td class="dim">${UI.fmtTime(im.created)}</td>
            <td>${im.used ? `<span class="badge ok">使用中</span>` : `<span class="badge idle">未使用</span>`}</td>
            <td><div class="actions">
              <button type="button" class="icon-btn" data-op="tag" title="打标签"><svg><use href="#i-edit"/></svg></button>
              <button type="button" class="icon-btn del" data-op="delete" title="删除"><svg><use href="#i-trash"/></svg></button>
            </div></td>`;
          tr.querySelectorAll('button[data-op]').forEach(btn => {
            btn.addEventListener('click', () => op(btn.dataset.op, im));
          });
          rows.appendChild(tr);
        });
      }

      async function op(kind, im) {
        if (kind === 'delete') {
          const used = im.used ? '该镜像正在被容器使用，' : '';
          if (!await UI.confirm('删除镜像', `${used}确定删除「${im.fullTag}」？`, true)) return;
          try {
            await API.del(`/api/images/${encodeURIComponent(im.id)}?force=1`);
            UI.toast('镜像已删除', 'ok'); load();
          } catch (e) { UI.toast(e.message, 'error'); }
        } else if (kind === 'tag') {
          const m = UI.modal({
            title: '给镜像打标签',
            body: `<div class="field"><label>新 repo（如 nginx / myrepo/app）</label><input id="tgRepo" /></div>
                   <div class="field"><label>tag（默认 latest）</label><input id="tgTag" placeholder="latest" /></div>`,
            foot: [
              { label: '取消', onClick: (c) => c() },
              { label: '确认', cls: 'primary', icon: 'i-check', onClick: async (c) => {
                const repo = m.mask.querySelector('#tgRepo').value.trim();
                const tag = m.mask.querySelector('#tgTag').value.trim() || 'latest';
                if (!repo) return UI.toast('请填写 repo', 'warn');
                try { await API.post(`/api/images/${encodeURIComponent(im.id)}/tag`, { repo, tag }); UI.toast('标签已添加', 'ok'); c(); load(); }
                catch (e) { UI.toast(e.message, 'error'); }
              } },
            ],
          });
        }
      }

      content.querySelector('#imBtnPull').addEventListener('click', () => {
        const m = UI.modal({
          title: '拉取镜像',
          body: `<div class="field"><label>镜像名（如 nginx、redis:7-alpine）</label><input id="plImg" placeholder="nginx" /></div>`,
          foot: [
            { label: '取消', onClick: (c) => c() },
            { label: '开始拉取', cls: 'primary', icon: 'i-down', onClick: async (c) => {
              const img = m.mask.querySelector('#plImg').value.trim();
              if (!img) return UI.toast('请填写镜像名', 'warn');
              try { await API.post('/api/images/pull', { image: img }); UI.toast('拉取任务已启动，进度见顶部进度条', 'ok'); c(); }
              catch (e) { UI.toast(e.message, 'error'); }
            } },
          ],
        });
      });

      content.querySelector('#imBtnPrune').addEventListener('click', async () => {
        if (!await UI.confirm('清理镜像', '删除所有未被容器使用的镜像？', true)) return;
        try {
          const s = await API.post('/api/images/prune', {});
          UI.toast('清理完成，释放 ' + UI.fmtBytes((s.data && s.data.SpaceReclaimed) || 0), 'ok');
          load();
        } catch (e) { UI.toast(e.message, 'error'); }
      });

      content.querySelector('#imBtnLoad').addEventListener('click', () => {
        const inp = document.createElement('input');
        inp.type = 'file'; inp.accept = '.tar,.tar.gz,.tgz';
        inp.addEventListener('change', async () => {
          if (!inp.files.length) return;
          UI.toast('正在上传导入，请稍候…', 'info', 8000);
          try { await API.upload('/api/images/load', inp.files[0]); UI.toast('镜像导入完成', 'ok'); load(); }
          catch (e) { UI.toast(e.message, 'error'); }
        });
        inp.click();
      });

      await load();
    },
  };
})();
