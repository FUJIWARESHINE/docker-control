# -*- coding: utf-8 -*-
"""
v4 端口页改造：按 tradis 的端口机制重做
- 双栏布局：TCP（蓝徽章）/ UDP（橙徽章）各自面板 + 独立滚动 + 计数 x/y
- 每行：端口大数字(PORT) | 来源徽章(Container 绿 / Host 蓝) | 状态(使用中) | 服务/进程 | 备注 | 复制
- 备注：复用 diancup 后端 /api/ports/{port}/custom-name（editPortName），持久化
- 搜索/空态逻辑保持原有函数不变
"""
import os

BASE = os.path.dirname(os.path.abspath(__file__))
STATIC = os.path.join(BASE, 'static')

# ------------------------------------------------------------
# 1. portsView 外壳：grid 卡片流 → block（双栏渲染函数接管）
# ------------------------------------------------------------
def patch_html(s):
    old = '''<div id="managementPortsList" style="flex: 1; overflow-y: auto; display: grid; grid-template-columns: repeat(auto-fill, minmax(280px, 320px)); gap: 0.75rem; align-content: start;">'''
    new = '''<div id="managementPortsList" style="flex: 1; min-height: 0; overflow: hidden; display: flex;">'''
    if old not in s:
        raise RuntimeError('portsView 外壳定位失败')
    return s.replace(old, new, 1)

# ------------------------------------------------------------
# 2. renderManagementPorts 重写（tradis 双栏）
# ------------------------------------------------------------
NEW_RP = r'''function renderManagementPorts(t){
  const root=document.getElementById("managementPortsList");
  if(!t||0===t.length)return void(root.innerHTML='<div style="flex:1;display:flex;align-items:center;justify-content:center;color:var(--text-tertiary);">暂无端口数据</div>');
  const esc=s=>String(s==null?'':s).replace(/&/g,'&amp;').replace(/</g,'&lt;').replace(/>/g,'&gt;').replace(/"/g,'&quot;');
  const total=t.length;
  const panel=(proto,title,sub,badgeCls)=>{
    const rows=t.filter(p=>(p.protocol||'tcp').toLowerCase()===proto).sort((a,b)=>a.port-b.port);
    const body=rows.length?rows.map(p=>{
      const hasC=p.container&&p.container.length>0;
      const src=hasC?'<span class="tp-src container">Container</span>':'<span class="tp-src host">Host</span>';
      const svc=hasC?esc(p.container):((p.service&&p.service!=='未识别')?esc(p.service):'-');
      const note=p.is_custom?esc(p.service):'';
      const svcs=hasC&&p.service&&p.service!=='未识别'&&p.service!==p.container?esc(p.service):'';
      return '<div class="tp-row">'
        +'<div class="tp-port"><div class="tp-num">'+p.port+'</div><div class="tp-type">PORT</div></div>'
        +'<div>'+src+'</div>'
        +'<div class="tp-status"><span class="tp-dot"></span>使用中</div>'
        +'<div class="tp-svc" title="'+svc+'"><span class="tp-svc-name">'+svc+'</span>'+(svcs?'<span class="tp-svc-sub">'+svcs+'</span>':'')+'</div>'
        +'<div class="tp-note'+(p.is_custom?' has':'')+'" onclick="editPortName('+p.port+', \''+esc((p.service||'').replace(/'/g,"\\'"))+'\', '+(p.is_custom?'true':'false')+')">'+(note||'添加备注...')+'</div>'
        +'<div class="tp-op"><button class="tp-copy" onclick="navigator.clipboard&&navigator.clipboard.writeText(\''+p.port+'\').catch(function(){});this.classList.add(\'done\');setTimeout(()=>this.classList.remove(\'done\'),800)" title="复制端口号"><i class="ri-file-copy-line"></i></button></div>'
        +'</div>';
    }).join(''):'<div class="tp-empty"><i class="ri-inbox-line"></i> 暂无 '+proto.toUpperCase()+' 端口</div>';
    return '<div class="tp-panel">'
      +'<div class="tp-panel-head">'
      +'<span class="tp-badge '+badgeCls+'">'+proto.toUpperCase()+'</span>'
      +'<div class="tp-panel-titles"><div class="tp-panel-title">'+title+'</div><div class="tp-panel-sub">'+sub+'</div></div>'
      +'<span class="tp-count">'+rows.length+' / '+total+'</span>'
      +'</div>'
      +'<div class="tp-cols"><span>端口</span><span>来源</span><span>状态</span><span>服务 / 进程</span><span>备注</span><span style="text-align:right;">操作</span></div>'
      +'<div class="tp-list">'+body+'</div>'
      +'</div>';
  };
  root.innerHTML='<div class="tp-wrap">'
    +panel('tcp','TCP 端口','传输控制协议','tcp')
    +panel('udp','UDP 端口','用户数据报协议','udp')
    +'</div>';
}'''

def replace_rp(s):
    a = s.find('function renderManagementPorts')
    b = s.find('async function editPortName')
    if a == -1 or b == -1 or a >= b:
        raise RuntimeError('renderManagementPorts 边界定位失败')
    return s[:a] + NEW_RP + '\n' + s[b:]

# ------------------------------------------------------------
# 3. CSS（tradis 端口页风格）
# ------------------------------------------------------------
PORT_CSS = r'''
/* ===== v4: tradis 风格端口页 ===== */
.tp-wrap{flex:1;display:grid;grid-template-columns:1fr 1fr;gap:1rem;min-height:0;padding:.25rem .25rem .5rem;}
@media (max-width:1100px){.tp-wrap{grid-template-columns:1fr;overflow-y:auto;}}
.tp-panel{background:var(--bg-elevated,var(--bg-secondary));border:1px solid var(--border);border-radius:.75rem;display:flex;flex-direction:column;min-height:0;overflow:hidden;}
.tp-panel-head{display:flex;align-items:center;gap:.7rem;padding:.85rem 1rem;border-bottom:1px solid var(--border);}
.tp-badge{font-size:.7rem;font-weight:700;letter-spacing:.03em;padding:.2rem .55rem;border-radius:.4rem;color:#fff;}
.tp-badge.tcp{background:var(--primary,#3b82f6);}
.tp-badge.udp{background:var(--warning,#f59e0b);}
.tp-panel-titles{line-height:1.2;}
.tp-panel-title{font-weight:700;font-size:.9rem;color:var(--text-primary);}
.tp-panel-sub{font-size:.68rem;color:var(--text-tertiary);}
.tp-count{margin-left:auto;font-size:.8rem;font-weight:600;color:var(--text-secondary);background:var(--bg-tertiary);padding:.15rem .6rem;border-radius:.5rem;}
.tp-cols{display:grid;grid-template-columns:86px 82px 74px minmax(96px,1.2fr) minmax(84px,1fr) 40px;gap:.4rem;align-items:center;padding:.45rem .8rem;font-size:.7rem;color:var(--text-tertiary);border-bottom:1px solid var(--border);user-select:none;}
.tp-list{flex:1;overflow-y:auto;min-height:0;}
.tp-row{display:grid;grid-template-columns:86px 82px 74px minmax(96px,1.2fr) minmax(84px,1fr) 40px;gap:.5rem;align-items:center;padding:.5rem .8rem;border-bottom:1px solid var(--border);transition:background .12s;}
.tp-row:last-child{border-bottom:none;}
.tp-row:hover{background:var(--bg-tertiary);}
.tp-port{line-height:1.15;}
.tp-num{font-weight:700;font-size:.95rem;color:var(--text-primary);}
.tp-type{font-size:.6rem;letter-spacing:.05em;color:var(--text-tertiary);}
.tp-src{font-size:.68rem;font-weight:600;padding:.15rem .5rem;border-radius:.4rem;white-space:nowrap;}
.tp-src.host{background:var(--primary-light,rgba(59,130,246,.1));color:var(--primary,#3b82f6);}
.tp-src.container{background:var(--success-light,rgba(16,185,129,.12));color:var(--success);}
.tp-status{display:flex;align-items:center;gap:.3rem;font-size:.75rem;color:var(--success);white-space:nowrap;}
.tp-dot{width:.5rem;height:.5rem;border-radius:50%;background:var(--success);box-shadow:0 0 0 2.5px var(--success-light,rgba(16,185,129,.18));flex-shrink:0;}
.tp-svc{min-width:0;line-height:1.25;}
.tp-svc-name{font-size:.8rem;font-weight:500;color:var(--text-primary);display:block;white-space:nowrap;overflow:hidden;text-overflow:ellipsis;}
.tp-svc-sub{font-size:.68rem;color:var(--text-tertiary);display:block;white-space:nowrap;overflow:hidden;text-overflow:ellipsis;}
.tp-note{font-size:.75rem;color:var(--text-tertiary);cursor:pointer;white-space:nowrap;overflow:hidden;text-overflow:ellipsis;border-bottom:1px dashed transparent;transition:border-color .15s;}
.tp-note:hover{border-bottom-color:var(--primary);color:var(--primary);}
.tp-note.has{color:var(--text-secondary);}
.tp-op{display:flex;justify-content:flex-end;}
.tp-copy{width:1.8rem;height:1.8rem;border-radius:.45rem;border:1px solid var(--border);background:var(--bg-secondary);color:var(--text-secondary);cursor:pointer;display:inline-flex;align-items:center;justify-content:center;font-size:.85rem;transition:all .15s;}
.tp-copy:hover{border-color:var(--primary);color:var(--primary);background:var(--primary-light,rgba(59,130,246,.08));}
.tp-copy.done{border-color:var(--success);color:var(--success);}
.tp-empty{text-align:center;padding:2.5rem 1rem;color:var(--text-tertiary);font-size:.85rem;}
.tp-empty i{font-size:1.6rem;display:block;margin-bottom:.4rem;opacity:.5;}
'''

# ------------------------------------------------------------
# 执行
# ------------------------------------------------------------
def patch_file(path, fn, desc):
    p = os.path.join(STATIC, path)
    with open(p, encoding='utf-8') as f:
        s = f.read()
    ns = fn(s)
    with open(p, 'w', encoding='utf-8', newline='\n') as f:
        f.write(ns)
    print('  [OK]', desc)

print('== v4 端口页改造 ==')
patch_file('index.html', patch_html, 'portsView 外壳改为双栏容器')
patch_file('js/app.min.js', replace_rp, 'app.min.js 重写 renderManagementPorts（tradis 双栏）')
patch_file('css/style.min.css', lambda s: s + PORT_CSS, 'style.min.css 追加端口页样式', )
print('\n全部 v4 补丁完成')
