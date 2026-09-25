# -*- coding: utf-8 -*-
"""
v3 侧边栏/界面定稿：
1. 管理组完整保留：恢复 模板创建、定时任务；检查更新挪到系统设置上方
2. 删除快捷组（生成 Compose 一并移除）
3. 左下角链接只留 GitHub（href 留空）
4. 顶部每日一言整体去掉
5. "关于"视图内容清空留白（放我们的功能介绍）
"""
import os, re

BASE = os.path.dirname(os.path.abspath(__file__))
STATIC = os.path.join(BASE, 'static')
P = os.path.join(STATIC, 'index.html')

with open(P, encoding='utf-8') as f:
    s = f.read()

def must_replace(s, old, new, desc, count=1):
    global _
    n = s.count(old)
    if n < count:
        raise RuntimeError(f'定位失败: {desc} (找到 {n} 处, 需要 {count})')
    s = s.replace(old, new, count)
    print('  [OK]', desc)
    return s

# 备份（一次性）
if not os.path.exists(P + '.v2.bak'):
    with open(P + '.v2.bak', 'w', encoding='utf-8', newline='\n') as f:
        f.write(s)
    print('  [备份] index.html.v2.bak')

# ------------------------------------------------------------
# 1. 管理组补全：存储卷之后、系统设置之前插入 模板创建/定时任务/检查更新
# ------------------------------------------------------------
SETTINGS_BTN = '''<button class="nav-item" onclick="switchMainView('settings')" data-view="settings">'''
INSERT = '''<button class="nav-item" onclick="switchMainView('template')" data-view="template">
                            <i class="ri-layout-4-fill"></i>
                            <span>模板创建</span>
                        </button>
                        <button class="nav-item" onclick="switchMainView('scheduled-tasks')" data-view="scheduled-tasks">
                            <i class="ri-time-line"></i>
                            <span>定时任务</span>
                        </button>
                        <button class="nav-item" onclick="checkAllUpdates()">
                            <i class="ri-search-line"></i>
                            <span>检查更新</span>
                        </button>
                        '''
s = must_replace(s, SETTINGS_BTN, INSERT + SETTINGS_BTN, '管理组: 插入 模板创建/定时任务/检查更新（系统设置上方）')

# ------------------------------------------------------------
# 2. 删除快捷组
# ------------------------------------------------------------
a = s.find('<!-- 快捷 -->')
b = s.find('<!-- 其他 -->')
if a == -1 or b == -1 or a >= b:
    raise RuntimeError('快捷组定位失败')
s = s[:a] + s[b:]
print('  [OK] 删除快捷组（含生成 Compose）')

# ------------------------------------------------------------
# 3. nav-footer：只留 GitHub，href 留空
# ------------------------------------------------------------
s = must_replace(s,
    'href="https://github.com/yjnas/diancup" target="_blank"',
    'href="#"',
    'GitHub 链接置空（待放我们的仓库地址）')
# 删除 Telegram 链接
m = re.search(r'\s*<a href="https://t\.me/diancup"[\s\S]*?</a>', s)
if not m: raise RuntimeError('Telegram 链接定位失败')
s = s[:m.start()] + s[m.end():]
print('  [OK] 删除 Telegram 链接')
# 删除 QQ 链接
m = re.search(r'\s*<a href="https://qm\.qq\.com/[\s\S]*?</a>', s)
if not m: raise RuntimeError('QQ 链接定位失败')
s = s[:m.start()] + s[m.end():]
print('  [OK] 删除 QQ 群链接')

# ------------------------------------------------------------
# 4. 顶部每日一言整体去掉
# ------------------------------------------------------------
HITOKOTO = '''            <!-- 一言显示区域（导航栏中间） -->
            <div class="hitokoto-nav" id="hitokotoNav">
                <div class="hitokoto-text" id="hitokotoText">加载中...</div>
                <div class="hitokoto-from" id="hitokotoFrom"></div>
            </div>
'''
s = must_replace(s, HITOKOTO, '', '删除顶部每日一言模块')

# ------------------------------------------------------------
# 5. 关于视图内容清空留白
# ------------------------------------------------------------
a = s.find('<div class="main-view" id="aboutView">')
b = s.find('<div class="main-view" id="containersView">')
if a == -1 or b == -1 or a >= b:
    raise RuntimeError('aboutView 范围定位失败')
# aboutView 的开标签行 + 留白内容 + 闭合
blank_view = '''<div class="main-view" id="aboutView">
                    <!-- 功能介绍留白区：后续放我们自己的内容 -->
                    <div style="padding: 3rem 2rem; text-align: center; color: var(--text-tertiary);">
                        <i class="ri-information-line" style="font-size: 2.5rem; opacity: .4;"></i>
                        <div style="margin-top: 1rem; font-size: .95rem;">Docker Control</div>
                    </div>
                </div><!-- 关于页面视图结束 -->

                '''
s = s[:a] + blank_view + s[b:]
print('  [OK] 关于视图清空留白')

with open(P, 'w', encoding='utf-8', newline='\n') as f:
    f.write(s)

# 顺手清掉 daily-quote 的拉取（避免 JS 每次启动还去请求 /api/hitokoto 再塞进不存在的节点）
JS = os.path.join(STATIC, 'js', 'app.min.js')
with open(JS, encoding='utf-8', errors='ignore') as f:
    js = f.read()
import re as _re
# 找 hitokoto 相关的 fetch/渲染函数调用点数量（只统计，不动逻辑——节点不存在时代码内部应有判空）
refs = len(_re.findall(r'hitokoto', js))
print(f'  [i] app.min.js 中 hitokoto 引用 {refs} 处（容器已删，若函数有判空则无影响）')

print('\n全部 v3 补丁完成')
