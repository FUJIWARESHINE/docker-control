# -*- coding: utf-8 -*-
"""v0.5.1 补丁：彻底跳过首启免责声明弹窗（每次进入都要点一次的元凶）
checkDisclaimerStatus 的逻辑是：请求 /api/disclaimer/status 失败或未同意 → 弹窗。
后端任何抖动都会导致弹窗复现。自研面板不需要这个声明，直接短路。
"""
import os

BASE = os.path.join(os.path.dirname(__file__), 'static')
P = os.path.join(BASE, 'js', 'app.min.js')

s = open(P, encoding='utf-8', errors='ignore').read()

OLD = ('async function checkDisclaimerStatus(){if(!disclaimerState.hasChecked&&!disclaimerState.isShowing)'
       '{disclaimerState.hasChecked=!0;try{const t=await fetch("/api/disclaimer/status");'
       'if((await t.json()).agreed_forever)return;showDisclaimerModal()}catch(t){showDisclaimerModal()}}}')
NEW = ('async function checkDisclaimerStatus(){disclaimerState.hasChecked=!0;'
       'disclaimerState.agreed=!0;return!0}')

n = s.count(OLD)
if n == 1:
    s = s.replace(OLD, NEW)
    open(P, 'w', encoding='utf-8', newline='\n').write(s)
    print('免责声明弹窗短路: OK')
elif 'disclaimerState.hasChecked=!0;disclaimerState.agreed=!0' in s:
    print('已打过补丁，跳过')
else:
    print('警告: 未匹配到目标代码（n=%d），检查 app.min.js' % n)
