/**
 * CodeMirror5 中文汉化插件
 * 汉化搜索、替换等界面文本
 */

(function() {
    'use strict';
    
    // 等待 CodeMirror 加载完成后进行汉化
    function initChinese() {
        if (typeof CodeMirror === 'undefined') {
            setTimeout(initChinese, 100);
            return;
        }
        
        // 中文翻译映射
        const chineseTranslations = {
            'Search:': '搜索:',
            'Replace:': '替换:',
            'Replace all:': '全部替换:',
            'With:': '替换为:',
            'Jump to line:': '跳转到行:',
            '(Use /re/ syntax for regexp search)': '(使用 /re/ 语法进行正则搜索)',
            'No matches': '未找到匹配项',
            'Search string not found': '未找到搜索内容',
            'Replaced $ matches': '已替换 $ 处匹配项',
            'All': '全部',
            'Stop': '停止',
            'Yes': '是',
            'No': '否'
        };
        
        // 重写 phrase 方法来实现汉化
        const originalPhrase = CodeMirror.prototype.phrase || function(text) { return text; };
        CodeMirror.prototype.phrase = function(text) {
            // 如果有中文翻译，使用中文，否则使用原文
            return chineseTranslations[text] || originalPhrase.call(this, text);
        };
        
        // 为所有 CodeMirror 实例添加 phrase 方法
        CodeMirror.defineExtension('phrase', function(text) {
            return chineseTranslations[text] || text;
        });
        
        // 汉化快捷键提示
        const chineseKeyMap = {
            'Ctrl-F': '搜索',
            'Ctrl-H': '替换',
            'Ctrl-G': '跳转到行',
            'Ctrl-S': '保存',
            'Ctrl-Z': '撤销',
            'Ctrl-Y': '重做',
            'Ctrl-/': '切换注释',
            'Ctrl-Q': '折叠/展开代码',
            'F3': '查找下一个',
            'Shift-F3': '查找上一个',
            'Ctrl-A': '全选',
            'Ctrl-C': '复制',
            'Ctrl-V': '粘贴',
            'Ctrl-X': '剪切'
        };
        
        // 添加中文提示功能
        window.getChineseKeyHint = function(key) {
            return chineseKeyMap[key] || key;
        };
        
        // 扩展 CodeMirror 实例方法
        if (CodeMirror.defineExtension) {
            // 添加中文提示方法
            CodeMirror.defineExtension('showChineseHint', function(message) {
            });
            
            // 添加中文确认对话框
            CodeMirror.defineExtension('confirmChinese', function(message, callback) {
                if (confirm(message)) {
                    callback();
                }
            });
        }
    }
    
    // 开始初始化
    initChinese();
    
})();