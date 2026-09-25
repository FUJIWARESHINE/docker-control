# -*- coding: utf-8 -*-
"""
v2 改造：纯 Docker 管理版
1. 侧边栏精简：只留 Docker 相关入口
2. 仪表板精简：隐藏依赖 /host 挂载的系统信息/资源监控栏，聚焦容器
3. 容器状态监控横条化（tradis 风格行式列表 + tradis 风格操作按钮）
4. docker-compose.yml 安全加固（去 privileged / 去 /:/host / 去 host 网络）
"""
import os, re, shutil

BASE = os.path.dirname(os.path.abspath(__file__))
STATIC = os.path.join(BASE, 'static')

def patch(path, fn, desc, backup=True):
    p = os.path.join(STATIC, path)
    with open(p, encoding='utf-8') as f:
        s = f.read()
    if backup and not os.path.exists(p + '.orig'):
        shutil.copy2(p, p + '.orig')
    ns = fn(s)
    if ns == s:
        print('  [!] 无变化:', desc)
        return
    with open(p, 'w', encoding='utf-8', newline='\n') as f:
        f.write(ns)
    print('  [OK]', desc)

# ============================================================
# 1. 侧边栏精简（index.html）
# ============================================================
def slim_sidebar(s):
    # 1a-0. 品牌替换
    n = s.count('DIANCUP')
    if n:
        s = s.replace('DIANCUP', 'Docker Control')
        print(f'  [OK] 品牌替换 {n} 处')
    # 1a. 删除"探索"整组（SSH终端/模板市场/昱君探针）
    a = s.find('<!-- 探索 -->')
    b = s.find('<!-- 快捷 -->')
    if a != -1 and b != -1 and a < b:
        s = s[:a] + s[b:]
        print('  [OK] 删除"探索"整组')
    # 1b. 按钮级删除
    pats = [
        r'<button class="nav-item"[^>]*data-view="(?:files|host-monitor|template|scheduled-tasks|market|probe)"[\s\S]*?</button>\s*',
        r'<button class="nav-item"[^>]*onclick="window\.location\.href=\'/ssh\'"[\s\S]*?</button>\s*',
        r'<button class="nav-item"[^>]*onclick="checkAllUpdates\(\)"[\s\S]*?</button>\s*',
    ]
    for pat in pats:
        s, n = re.subn(pat, '', s)
        if n: print(f'  [OK] 删除 {n} 个导航按钮')
    # 1c. "常用"组改名为"概览"
    s = s.replace('<span>常用</span>', '<span>概览</span>')
    return s

# ============================================================
# 2. 仪表板精简（CSS 注入，DOM 保留避免 JS 空引用报错）
# ============================================================
DASHBOARD_CSS = r'''
/* ===== v2: 纯 Docker 管理 ===== */
/* 仪表板：隐藏依赖 /host 的系统信息与资源监控栏，容器统计占满 */
.dashboard-three-column{display:block!important;}
.dashboard-column:not(.dashboard-column-right){display:none!important;}
.dashboard-column-right .stats-grid-4 .stat-card{padding:1rem 1.25rem;}
/* 容器状态监控：tradis 风格横条列表 */
.containers-grid{display:flex!important;flex-direction:column;gap:.5rem!important;}
.tcx-head,.tcx-row{
  display:grid!important;
  grid-template-columns:minmax(190px,1.5fr) minmax(170px,1.7fr) 96px minmax(150px,1.1fr) minmax(120px,1fr) 148px;
  align-items:center;gap:.75rem;
}
.tcx-head{
  padding:.45rem .9rem;font-size:.72rem;color:var(--text-tertiary);
  text-transform:uppercase;letter-spacing:.04em;user-select:none;
}
.tcx-head .tcx-h{display:flex;align-items:center;gap:.25rem;}
.tcx-row{
  background:var(--bg-elevated, var(--bg-secondary));border:1px solid var(--border);
  border-radius:.65rem;padding:.55rem .9rem;cursor:default;
  transition:box-shadow .15s ease, transform .15s ease, border-color .15s ease;
  position:relative;overflow:hidden;
}
.tcx-row:hover{box-shadow:0 4px 16px var(--shadow, rgba(0,0,0,.08));border-color:var(--primary-light, var(--primary));transform:translateY(-1px);}
.tcx-row.tcx-stopped{opacity:.68;}
.tcx-row.tcx-stopped .tcx-dot{background:var(--text-tertiary)!important;}
.tcx-row.tcx-has-update{border-left:3px solid var(--warning);}
/* 名称列 */
.tcx-name{display:flex;align-items:center;gap:.6rem;min-width:0;}
.tcx-dot{width:.55rem;height:.55rem;border-radius:50%;background:var(--success);flex-shrink:0;box-shadow:0 0 0 3px var(--success-light, rgba(16,185,129,.15));}
.tcx-n{font-weight:600;font-size:.88rem;color:var(--text-primary);white-space:nowrap;overflow:hidden;text-overflow:ellipsis;}
.tcx-sub{font-size:.7rem;color:var(--text-tertiary);white-space:nowrap;overflow:hidden;text-overflow:ellipsis;}
.tcx-nwrap{min-width:0;}
/* 镜像列 */
.tcx-image{font-family:'JetBrains Mono',monospace;font-size:.74rem;color:var(--text-secondary);white-space:nowrap;overflow:hidden;text-overflow:ellipsis;}
/* 状态列 */
.tcx-state{display:flex;align-items:center;gap:.3rem;font-size:.78rem;font-weight:500;color:var(--success);white-space:nowrap;}
.tcx-row.tcx-stopped .tcx-state{color:var(--text-tertiary);}
.tcx-row.tcx-has-update .tcx-state{color:var(--warning);}
/* 资源列 */
.tcx-res{display:flex;align-items:center;gap:.9rem;font-size:.76rem;white-space:nowrap;}
.tcx-res .tcx-cpu{display:inline-flex;align-items:center;gap:.25rem;color:var(--info,#3b82f6);}
.tcx-res .tcx-mem{display:inline-flex;align-items:center;gap:.25rem;color:var(--success);}
/* 端口列 */
.tcx-ports{display:flex;flex-wrap:wrap;gap:.25rem;min-width:0;}
.tcx-port{
  font-family:'JetBrains Mono',monospace;font-size:.72rem;color:var(--primary);
  background:var(--primary-light, rgba(59,130,246,.08));padding:.1rem .4rem;border-radius:.35rem;
  white-space:nowrap;overflow:hidden;text-overflow:ellipsis;max-width:100%;
}
/* 操作列（tradis 风格按钮） */
.tcx-actions{display:flex;align-items:center;justify-content:flex-end;gap:.4rem;}
.tcx-btn{
  width:2rem;height:2rem;border-radius:.5rem;border:1px solid transparent;
  display:inline-flex;align-items:center;justify-content:center;
  cursor:pointer;font-size:.95rem;transition:all .15s ease;background:var(--bg-tertiary);color:var(--text-secondary);
}
.tcx-btn:hover{transform:translateY(-1px);}
.tcx-btn:active{transform:translateY(0);}
.tcx-btn.tcx-stop{background:var(--danger-light, rgba(239,68,68,.1));color:var(--danger);}
.tcx-btn.tcx-stop:hover{background:var(--danger);color:#fff;}
.tcx-btn.tcx-start{background:var(--success-light, rgba(16,185,129,.1));color:var(--success);}
.tcx-btn.tcx-start:hover{background:var(--success);color:#fff;}
.tcx-btn.tcx-restart{background:var(--warning-light, rgba(245,158,11,.12));color:var(--warning);}
.tcx-btn.tcx-restart:hover{background:var(--warning);color:#fff;}
.tcx-btn.tcx-more:hover{background:var(--primary-light, rgba(59,130,246,.1));color:var(--primary);}
.tcx-btn:disabled{opacity:.45;cursor:not-allowed;transform:none;}
/* 更多菜单 */
.tcx-more-wrap{position:relative;}
.tcx-menu{
  position:absolute;top:calc(100% + .35rem);right:0;z-index:9999;min-width:130px;
  background:var(--bg-elevated);border:1px solid var(--border);border-radius:.55rem;
  box-shadow:0 8px 24px var(--shadow, rgba(0,0,0,.15));padding:.35rem;display:flex;flex-direction:column;gap:.1rem;
  backdrop-filter:blur(10px);
}
.tcx-menu button{
  background:transparent;border:none;cursor:pointer;text-align:left;
  padding:.42rem .6rem;border-radius:.4rem;font-size:.8rem;color:var(--text-primary);
  display:flex;align-items:center;gap:.45rem;transition:background .15s;width:100%;
}
.tcx-menu button:hover{background:var(--bg-tertiary);}
.tcx-menu button:disabled{opacity:.45;cursor:not-allowed;}
/* 窄屏自适应： progressively 隐藏列 */
@media (max-width:1280px){.tcx-head,.tcx-row{grid-template-columns:minmax(180px,1.4fr) minmax(150px,1.5fr) 90px 148px;}.tcx-image,.tcx-h-img{display:none!important;}}
@media (max-width:900px){.tcx-head,.tcx-row{grid-template-columns:minmax(160px,1.5fr) 90px 148px;}.tcx-res,.tcx-h-res{display:none!important;}}
'''

# ============================================================
# 3. renderContainers 替换（横条渲染，函数签名/数据接口与原版一致）
# ============================================================
NEW_RC = r'''function renderContainers(){
  const root=document.getElementById("containers-container");
  if(!root)return;
  const empty=h=>{root.innerHTML='<div style="text-align:center;padding:2rem;color:var(--text-tertiary);grid-column:1/-1;"><div><i class="ri-inbox-line"></i> '+h+'</div></div>'};
  if(0===appState.containers.length)return empty('暂无容器');
  let list=appState.containers.filter(t=>!t.labels||"true"!==t.labels["diancup.self"]);
  if("autoupdate"===appState.currentFilter)list=list.filter(t=>t.auto_update);
  else if("updatable"===appState.currentFilter)list=list.filter(t=>t.has_update&&shouldCheckUpdate(t));
  else if("running"===appState.currentFilter)list=list.filter(t=>"running"===t.state);
  else if("stopped"===appState.currentFilter)list=list.filter(t=>"running"!==t.state);
  if(appState.searchTerm){const q=appState.searchTerm.toLowerCase();list=list.filter(c=>{const al=appState.containerAliases[c.name]||"";return c.name.toLowerCase().includes(q)||c.image.toLowerCase().includes(q)||al.toLowerCase().includes(q)})}
  if(appState.dashboardSortBy)list.sort((a,b)=>{switch(appState.dashboardSortBy){
    case"name":return a.name.localeCompare(b.name);
    case"created":return parseInt(b.id.substring(0,12),16)-parseInt(a.id.substring(0,12),16);
    case"cpu":return(parseFloat(b.cpuUsage)||0)-(parseFloat(a.cpuUsage)||0);
    case"memory":return(parseFloat(b.memoryUsage)||0)-(parseFloat(a.memoryUsage)||0);
    default:{const au=(a.has_update&&shouldCheckUpdate(a))?3:(a.auto_update?2:1),bu=(b.has_update&&shouldCheckUpdate(b))?3:(b.auto_update?2:1);return bu-au||a.name.localeCompare(b.name)}
  }});
  if(0===list.length)return empty(appState.searchTerm?'没有找到匹配的容器':'没有符合条件的容器');
  const prog=appState.containerProgressMap,aliases=appState.containerAliases;
  const memStr=m=>{if(!(m>0))return'-';const mb=m/1048576;return mb>=1024?(mb/1024).toFixed(1)+'G':mb>=1?mb.toFixed(0)+'M':mb.toFixed(0)+'M'};
  const esc=s=>String(s).replace(/&/g,'&amp;').replace(/</g,'&lt;').replace(/>/g,'&gt;').replace(/"/g,'&quot;');
  const head='<div class="tcx-head">'
    +'<div class="tcx-h">名称</div>'
    +'<div class="tcx-h tcx-h-img">镜像</div>'
    +'<div class="tcx-h">状态</div>'
    +'<div class="tcx-h tcx-h-res">资源</div>'
    +'<div class="tcx-h">端口</div>'
    +'<div class="tcx-h" style="justify-content:flex-end;">操作</div>'
    +'</div>';
  const rows=list.map(c=>{
    const running="running"===c.state, updating=prog.has(c.name);
    const checkable=shouldCheckUpdate(c), hasUpd=checkable&&c.has_update;
    const alias=aliases[c.name]||c.name, isAlias=!!aliases[c.name];
    const cpu=c.cpuUsage||0;
    const cpuColor=cpu>80?'var(--danger)':cpu>50?'var(--warning)':'var(--success)';
    const ports=(c.ports||[]).filter(p=>p&&p.host_port).map(p=>'<span class="tcx-port" title="容器端口 '+esc(p.container_port||'')+'">'+esc(String(p.host_port))+':'+esc(String(p.container_port||'').split('/')[0])+'</span>').join('');
    const stateTxt=updating?'更新中':hasUpd?'可更新':running?'运行中':'已停止';
    const dotStyle=hasUpd?'style="background:var(--warning);box-shadow:0 0 0 3px var(--warning-light,rgba(245,158,11,.2));"':'';
    const autoUpd=c.auto_update;
    return '<div class="tcx-row'+(running?'':' tcx-stopped')+(hasUpd?' tcx-has-update':'')+(updating?' updating':'')+'"'
      +' data-container-name="'+esc(c.name)+'" data-container-id="'+esc(c.id||'')+'" oncontextmenu="return false;">'
      +'<div class="tcx-name"><span class="tcx-dot"'+dotStyle+'></span>'
      +'<div class="tcx-nwrap"><div class="tcx-n">'+(updating?'<i class="ri-loader-4-line container-updating-icon" style="color:var(--primary);margin-right:.25rem;"></i>':'')
      +(autoUpd?'<i class="ri-star-fill" style="font-size:.7rem;color:var(--warning);margin-right:.25rem;" title="自动更新"></i>':'')
      +esc(alias)+(isAlias?' <span class="tcx-sub">('+esc(c.name)+')</span>':'')+'</div>'
      +'<div class="tcx-sub">'+esc((c.id||'').substring(0,12))+'</div></div></div>'
      +'<div class="tcx-image" title="'+esc(c.image)+'">'+esc(c.image)+'</div>'
      +'<div class="tcx-state"><span>'+stateTxt+'</span></div>'
      +'<div class="tcx-res">'
      +'<span class="tcx-cpu"><i class="ri-cpu-line"></i><span class="tooltip-cpu" style="color:'+cpuColor+';">'+cpu.toFixed(1)+'%</span></span>'
      +'<span class="tcx-mem"><i class="ri-database-2-line"></i><span class="tooltip-mem">'+memStr(c.memoryUsage)+'</span></span></div>'
      +'<div class="tcx-ports">'+(ports||'<span class="tcx-sub">-</span>')+'</div>'
      +'<div class="tcx-actions">'
      +'<button class="tcx-btn '+(running?'tcx-stop':'tcx-start')+'" onclick="'+(running?'stopContainer':'startContainer')+"('"+c.name+"', event)\""+(updating?' disabled':'')+' title="'+(running?'停止':'启动')+'"><i class="'+(running?'ri-stop-fill':'ri-play-fill')+'"></i></button>'
      +'<button class="tcx-btn tcx-restart" onclick="restartContainer('+"'"+c.name+"', event)\""+(updating?' disabled':'')+' title="重启"><i class="ri-refresh-fill"></i></button>'
      +'<div class="tcx-more-wrap">'
      +'<button class="tcx-btn tcx-more" onclick="toggleContainerOptions('+"'"+c.name+"')\""+' title="更多选项"><i class="ri-arrow-down-s-line"></i></button>'
      +'<div id="options-'+c.name+'" class="tcx-menu" style="display:none;">'
      +(checkable?'<button onclick="toggleContainerAutoUpdate('+"'"+c.name+"', "+(!autoUpd)+')"><i class="'+(autoUpd?'ri-star-fill':'ri-star-line')+'" style="color:var(--warning);"></i>'+(autoUpd?'关闭自动更新':'开启自动更新')+'</button>':'')
      +'<button onclick="editContainerAlias('+"'"+c.name+"')"+'"><i class="ri-edit-line"></i>别名</button>'
      +'<button onclick="openIconSelector('+"'"+c.name+"')"+'"><i class="ri-image-line"></i>图标</button>'
      +(checkable?('<button onclick="checkContainerUpdate('+"'"+c.name+"')"+'"><i class="ri-search-eye-line"></i>检查更新</button>'
      +'<button onclick="updateContainer('+"'"+c.name+"')"+'"'+(hasUpd?'':' disabled')+'><i class="ri-upload-cloud-2-line"></i>更新容器</button>'):'')
      +'</div></div></div></div>';
  }).join('');
  root.innerHTML=head+rows;
}'''

def replace_render(s):
    a = s.find('function renderContainers()')
    b = s.find('function setupContainerEventDelegation()')
    if a == -1 or b == -1 or a >= b:
        raise RuntimeError('renderContainers 定位失败')
    return s[:a] + NEW_RC + '\n' + s[b:]

# ============================================================
# 执行
# ============================================================
print('== 1/3 侧边栏精简 ==')
patch('index.html', slim_sidebar, 'index.html 侧边栏')

print('== 2/3 横条 CSS + renderContainers ==')
def css_add(s):
    return s + DASHBOARD_CSS
patch('css/style.min.css', css_add, 'style.min.css 追加横条样式', backup=False)
patch('js/app.min.js', replace_render, 'app.min.js 替换 renderContainers')

print('== 3/3 docker-compose.yml 安全加固 ==')
compose = '''# Docker Control（diancup 二次开发基底 + tradis 风格 UI · 纯 Docker 管理版）
# 部署：把本目录（panel/）整个传到 NAS/服务器，改 DOCKER_DIR 后 docker compose up -d
#
# 安全加固说明（相比 diancup 原版）：
#   - 移除 privileged: true   —— 管理 Docker 只需要 docker.sock，不需要全部内核能力
#   - 移除 /:/host 挂载       —— 纯 Docker 管理不读宿主机文件系统；主机监控类功能已从 UI 移除
#   - 移除 network_mode: host —— 改用 bridge + 端口映射，容器不暴露在宿主机网络栈
#   - no-new-privileges + cap_drop: ALL —— 最小权限运行
#   ⚠ 代价：依赖 /host 的功能（主机监控/文件管理/系统信息部分字段）不再可用，UI 已同步移除
services:
  diancup:
    image: yjnas/diancup:latest
    container_name: diancup
    restart: unless-stopped
    ports:
      - "9527:9527"                   # Web UI
    volumes:
      - /var/run/docker.sock:/var/run/docker.sock   # ★ 唯一必需挂载：Docker Engine API
      - ./config:/config              # 配置与 SQLite 数据库
      # ★ 皮肤覆盖：用改过的 static 盖掉镜像内原版前端（无需重新构建镜像）
      - ./static:/app/static
    environment:
      - DOCKER_DIR=/your/docker/path  # ★ 改成你的 Docker compose 配置目录
      - WEB_PORT=9527
      - TZ=Asia/Shanghai
    security_opt:
      - no-new-privileges:true
    cap_drop:
      - ALL
    labels:
      - diancup.self=true             # diancup 要求，不可删
'''
with open(os.path.join(BASE, 'docker-compose.yml'), 'w', encoding='utf-8', newline='\n') as f:
    f.write(compose)
print('  [OK] docker-compose.yml 已重写（无 privileged / 无 /:/host / bridge 网络）')

print('\n全部 v2 补丁完成')
