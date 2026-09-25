# -*- coding: utf-8 -*-
"""
给 diancup 前端打 tradis 风格皮肤补丁：
1. THEME_COLORS.min.js 注册 "tradis-blue" 主题并设为默认
2. style.min.css 尾部追加全局微调（字体/滚动条/焦点环/:root 变量兜底）
3. index.html / login.html 标题与 theme-color 更新
"""
import io, os, re, sys

BASE = r"D:\WorkBuddy_Project\Docker Control\panel\static"

TRADIS_THEME = '''"tradis-blue":{name:"Tradis 蓝",type:"light",colors:{primary:"#3b82f6",primaryHover:"#2563eb",primaryActive:"#1d4ed8",primaryLight:"#dbeafe",primaryText:"#ffffff",success:"#10b981",successHover:"#059669",successActive:"#047857",successLight:"#d1fae5",successText:"#ffffff",warning:"#f59e0b",warningHover:"#d97706",warningActive:"#b45309",warningLight:"#fef3c7",warningText:"#ffffff",danger:"#ef4444",dangerHover:"#dc2626",dangerActive:"#b91c1c",dangerLight:"#fee2e2",dangerText:"#ffffff",info:"#3b82f6",infoHover:"#2563eb",infoActive:"#1d4ed8",infoLight:"#dbeafe",infoText:"#ffffff",bg:"#f8fafc",bgSecondary:"#f1f5f9",bgTertiary:"#e2e8f0",bgElevated:"#ffffff",bgOverlay:"rgba(15, 23, 42, 0.35)",bgMask:"rgba(15, 23, 42, 0.55)",text:"#111827",textSecondary:"#475569",textTertiary:"#64748b",textPlaceholder:"#94a3b8",textDisabled:"#94a3b8",textInverse:"#ffffff",border:"#cbd5e1",borderLight:"#e2e8f0",borderDark:"#94a3b8",borderFocus:"#3b82f6",link:"#3b82f6",linkHover:"#2563eb",linkActive:"#1d4ed8",linkVisited:"#6d28d9",codeBg:"#f1f5f9",codeBorder:"#e2e8f0",codeText:"#334155",inputBg:"#ffffff",inputBorder:"#cbd5e1",inputFocusBorder:"#3b82f6",inputPlaceholder:"#94a3b8",inputDisabledBg:"#f1f5f9",inputDisabledText:"#94a3b8",buttonDisabledBg:"#f1f5f9",buttonDisabledText:"#94a3b8",buttonDisabledBorder:"#e2e8f0",selectedBg:"#dbeafe",selectedBorder:"#3b82f6",activeBg:"#eff6ff",activeBorder:"#2563eb",divider:"#e2e8f0",shadow:"rgba(15, 23, 42, 0.06)",shadowMedium:"rgba(15, 23, 42, 0.09)",shadowLarge:"rgba(15, 23, 42, 0.12)",shadowXlarge:"rgba(15, 23, 42, 0.16)"}},'''

SKIN_CSS = '''
/* ====== TRADIS-STYLE SKIN (docker-control 二次开发) ====== */
:root{
  --primary:#3b82f6;--primary-hover:#2563eb;--primary-active:#1d4ed8;--primary-light:#dbeafe;
  --success:#10b981;--success-hover:#059669;--success-active:#047857;--success-light:#d1fae5;
  --danger:#ef4444;--danger-hover:#dc2626;--danger-active:#b91c1c;--danger-light:#fee2e2;
  --warning:#f59e0b;--warning-hover:#d97706;--warning-active:#b45309;--warning-light:#fef3c7;
  --info:#3b82f6;--info-hover:#2563eb;--info-active:#1d4ed8;--info-light:#dbeafe;
  --bg:#f8fafc;--bg-secondary:#f1f5f9;--bg-tertiary:#e2e8f0;--bg-elevated:#ffffff;
  --text:#111827;--text-secondary:#475569;--text-tertiary:#64748b;
  --border:#cbd5e1;--border-light:#e2e8f0;
  --radius-sm:8px;--radius-md:12px;--radius-lg:16px;
}
body{font-family:"Inter",-apple-system,BlinkMacSystemFont,"Segoe UI","PingFang SC","Microsoft YaHei UI",system-ui,sans-serif;
     -webkit-font-smoothing:antialiased;letter-spacing:.01em}
code,pre,kbd,.mono,[class*="terminal"],[class*="log"]{
  font-family:"JetBrains Mono","Cascadia Code",ui-monospace,Consolas,monospace}
button,.btn,[class*="btn"],[class*="button"]{border-radius:10px;transition:all .18s cubic-bezier(.4,0,.2,1)}
.card,[class*="card"],[class*="panel"],[class*="container-card"]{border-radius:14px}
::-webkit-scrollbar{width:8px;height:8px}
::-webkit-scrollbar-track{background:transparent}
::-webkit-scrollbar-thumb{background:#cbd5e1;border-radius:8px}
::-webkit-scrollbar-thumb:hover{background:#94a3b8}
:focus-visible{outline:2px solid rgba(59,130,246,.45);outline-offset:2px}
'''

def patch(path, fn, label):
    p = os.path.join(BASE, path)
    src = open(p, encoding='utf-8', errors='ignore').read()
    out = fn(src)
    if out is None:
        print(f'[跳过] {label}')
        return
    open(p, 'w', encoding='utf-8', newline='\n').write(out)
    print(f'[完成] {label}')

# 1. THEME_COLORS：插入 tradis-blue + 改默认主题
def patch_theme(s):
    if '"tradis-blue"' in s:
        print('  已存在 tradis-blue')
    else:
        s = s.replace('const THEME_COLORS={', 'const THEME_CODES_IGNORE={' if False else 'const THEME_COLORS={' + TRADIS_THEME, 1)
    if 'DEFAULT_THEME="elegant-white"' in s:
        s = s.replace('DEFAULT_THEME="elegant-white"', 'DEFAULT_THEME="tradis-blue"')
        print('  默认主题 -> tradis-blue')
    return s
patch('js/THEME_COLORS.min.js', patch_theme, 'THEME_COLORS.min.js 注册 tradis-blue 主题')

# 2. style.min.css 追加皮肤
def patch_css(s):
    if 'TRADIS-STYLE SKIN' in s:
        print('  皮肤已存在')
        return None
    return s + SKIN_CSS
patch('css/style.min.css', patch_css, 'style.min.css 追加 tradis 皮肤')

# 3. 标题/主题色
def patch_html(s):
    s = s.replace('<title>DIANCUP</title>', '<title>Docker Control</title>')
    s = s.replace('<meta name="theme-color" content="#2196f3">', '<meta name="theme-color" content="#3b82f6">')
    s = s.replace('content="DIANCUP"', 'content="Docker Control"')
    return s
for f in ['index.html', 'login.html', 'mobile.html', 'navigation.html']:
    patch(f, patch_html, f'{f} 标题与主题色')

# 4. 清除所有页面/JS 里硬编码的旧默认主题（否则内联样式会覆盖新主题）
import glob as _glob
patched = 0
for f in _glob.glob(os.path.join(BASE, '**', '*'), recursive=True):
    if not f.endswith(('.html', '.js')) or 'THEME_COLORS' in f or 'theme.min' in f:
        continue
    if not os.path.isfile(f):
        continue
    src = open(f, encoding='utf-8', errors='ignore').read()
    n = src.count('elegant-white')
    if n:
        open(f, 'w', encoding='utf-8', newline='\n').write(src.replace('elegant-white', 'tradis-blue'))
        patched += n
print(f'[完成] 清除硬编码默认主题 {patched} 处 -> tradis-blue')

print('\n全部补丁完成 ->', BASE)
