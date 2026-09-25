/**
 * CodeMirror5 简化封装
 * 直接使用 CodeMirror API
 */

(function() {
    'use strict';
    
    // 语言模式映射
    const LANGUAGE_MAP = {
        'yaml': 'text/x-yaml',
        'yml': 'text/x-yaml',
        'html': 'text/html',
        'htm': 'text/html',
        'xml': 'application/xml',
        'toml': 'text/x-toml',
        'json': 'application/json',
        'css': 'text/css',
        'javascript': 'text/javascript',
        'js': 'text/javascript',
        'text': 'text/plain',
        'txt': 'text/plain',
        'plaintext': 'text/plain'
    };
    
    // 主题映射
    const THEME_MAP = {
        'light': 'default',
        'default': 'default',
        'dark': 'monokai',
        'monokai': 'monokai',
        'vs': 'default',
        'vs-dark': 'monokai'
    };
    
    /**
     * 创建 CodeMirror 编辑器
     * @param {string|HTMLElement} container - 容器元素或ID
     * @param {Object} options - 配置选项
     * @returns {CodeMirror} CodeMirror 实例
     */
    window.createCodeMirror = function(container, options = {}) {
        const containerEl = typeof container === 'string' 
            ? document.getElementById(container) 
            : container;
            
        if (!containerEl) {
            throw new Error('容器元素不存在');
        }
        
        // 清空容器
        containerEl.innerHTML = '';
        
        // 解析配置
        const config = {
            value: options.value || '',
            mode: LANGUAGE_MAP[options.language?.toLowerCase()] || 'text/plain',
            theme: THEME_MAP[options.theme?.toLowerCase()] || 'default',
            lineNumbers: options.lineNumbers !== false,
            readOnly: options.readOnly || false,
            tabSize: options.tabSize || 2,
            indentUnit: options.indentUnit || 2,
            indentWithTabs: options.indentWithTabs || false,
            lineWrapping: options.lineWrapping === undefined ? true : options.lineWrapping, // 默认启用自动换行
            
            // 启用插件功能
            matchBrackets: true,
            autoCloseBrackets: true,
            styleActiveLine: true,
            foldGutter: true,
            gutters: ["CodeMirror-linenumbers", "CodeMirror-foldgutter"],
            
            // 快捷键
            extraKeys: {
                "Ctrl-S": function(cm) {
                    const event = new CustomEvent('codemirror-save', { 
                        detail: { editor: cm, value: cm.getValue() } 
                    });
                    window.dispatchEvent(event);
                },
                "Ctrl-F": "findPersistent",
                "Ctrl-H": "replace",
                "Ctrl-/": "toggleComment",
                "Ctrl-Q": function(cm) { cm.foldCode(cm.getCursor()); }
            }
        };
        
        // 创建编辑器
        const editor = CodeMirror(containerEl, config);
        
        // 设置编辑器高度和样式
        editor.setSize(null, options.height || "100%");
        
        // 确保容器有最小高度
        if (containerEl.style.height === '' || containerEl.style.height === 'auto') {
            containerEl.style.minHeight = '300px';
            containerEl.style.height = '100%';
        }
        
        
        // 添加便捷方法
        editor.setLanguage = function(lang) {
            this.setOption('mode', LANGUAGE_MAP[lang?.toLowerCase()] || 'text/plain');
        };
        
        editor.setTheme = function(theme) {
            this.setOption('theme', THEME_MAP[theme?.toLowerCase()] || 'default');
        };
        
        editor.toggleWrap = function() {
            const current = this.getOption('lineWrapping');
            this.setOption('lineWrapping', !current);
            return !current;
        };
        
        editor.format = function() {
            const mode = this.getOption('mode');
            if (mode === 'application/json') {
                try {
                    const formatted = JSON.stringify(JSON.parse(this.getValue()), null, 2);
                    this.setValue(formatted);
                } catch (e) {
                }
            }
        };
        
        // 添加 dispose 方法用于销毁
        editor.dispose = function() {
            if (this.toTextArea) {
                this.toTextArea();
            }
        };
        
        return editor;
    };
})();
