# -*- coding: utf-8 -*-
"""v0.5 自研后端兼容补丁：
1. updateDashboardContainerStatsDOM 支持 tcx- 横条布局（原版只认 .container-item 旧卡片）
   —— 自研后端 stats/batch 推送后，仪表板资源列实时更新
"""
import os

BASE = os.path.join(os.path.dirname(__file__), 'static')
P = os.path.join(BASE, 'js', 'app.min.js')

s = open(P, encoding='utf-8', errors='ignore').read()

OLD = 'function updateDashboardContainerStatsDOM(t){const e=document.querySelector'
NEW = (
    'function updateDashboardContainerStatsDOM(t){'
    'if(t&&t.id){'
    'const row=document.querySelector(\'.tcx-row[data-container-id="\'+t.id+\'"]\');'
    'if(row){'
    'const n=t.cpuUsage||0,o=t.memoryUsage||0;'
    'const cpu=row.querySelector(".tooltip-cpu"),mem=row.querySelector(".tooltip-mem");'
    'let ic="var(--success)";n>50&&(ic="var(--warning)");n>80&&(ic="var(--danger)");'
    'let r="0M";if(o>0){const mb=o/1048576,g=mb/1024;r=g>=1?g.toFixed(1)+"G":mb.toFixed(0)+"M"}'
    'if(cpu){cpu.textContent=n.toFixed(1)+"%";cpu.style.color=ic}'
    'if(mem){mem.textContent=r}'
    'updateContainerResourceHistory(t.id,n,o);drawContainerChart(t.id);updateContainerTooltip(t.id,n,o);'
    'return}}'
    'const e=document.querySelector'
)

n = s.count(OLD)
if n == 0 and 'tcx-row[data-container-id' in s:
    print('已打过补丁，跳过')
elif n != 1:
    print('警告: 匹配到 %d 处，预期 1 处，跳过' % n)
else:
    s = s.replace(OLD, NEW)
    open(P, 'w', encoding='utf-8', newline='\n').write(s)
    print('updateDashboardContainerStatsDOM tcx 兼容补丁: OK')
