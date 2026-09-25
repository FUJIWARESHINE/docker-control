/**
 * Docker Compose 文件验证器
 * 提供 YAML 语法验证和 Docker Compose schema 验证
 */

(function() {
    'use strict';
    
    /**
     * YAML 解析器（简化版）
     */
    class SimpleYAMLParser {
        parse(yamlText) {
            const errors = [];
            const lines = yamlText.split('\n');
            
            // 检查基本的 YAML 语法错误
            for (let i = 0; i < lines.length; i++) {
                const line = lines[i];
                const lineNum = i + 1;
                
                // 跳过空行和注释
                if (!line.trim() || line.trim().startsWith('#')) {
                    continue;
                }
                
                // 检查缩进（必须是空格，不能是制表符）
                if (line.match(/^\t/)) {
                    errors.push({
                        line: lineNum,
                        column: 1,
                        message: 'YAML 不允许使用制表符缩进，请使用空格',
                        severity: 'error'
                    });
                }
                
                // 检查缩进是否为2的倍数
                const indent = line.match(/^( *)/)[1].length;
                if (indent % 2 !== 0) {
                    errors.push({
                        line: lineNum,
                        column: 1,
                        message: '缩进应该是2个空格的倍数',
                        severity: 'warning'
                    });
                }
                
                // 检查冒号后是否有空格
                if (line.match(/:\S/) && !line.match(/:\s*$/)) {
                    const colonIndex = line.indexOf(':');
                    errors.push({
                        line: lineNum,
                        column: colonIndex + 2,
                        message: '冒号后应该有一个空格',
                        severity: 'warning'
                    });
                }
                
                // 检查是否有未闭合的引号
                const singleQuotes = (line.match(/'/g) || []).length;
                const doubleQuotes = (line.match(/"/g) || []).length;
                if (singleQuotes % 2 !== 0 || doubleQuotes % 2 !== 0) {
                    errors.push({
                        line: lineNum,
                        column: 1,
                        message: '引号未正确闭合',
                        severity: 'error'
                    });
                }
            }
            
            // 尝试解析为对象
            try {
                // 这里使用简单的解析逻辑
                const obj = this.simpleParseYAML(yamlText);
                return { success: true, data: obj, errors };
            } catch (e) {
                errors.push({
                    line: 1,
                    column: 1,
                    message: `YAML 解析错误: ${e.message}`,
                    severity: 'error'
                });
                return { success: false, data: null, errors };
            }
        }
        
        simpleParseYAML(yamlText) {
            // 简化的 YAML 解析（仅用于基本验证）
            const lines = yamlText.split('\n');
            const result = {};
            let currentKey = null;
            let currentIndent = 0;
            
            for (const line of lines) {
                if (!line.trim() || line.trim().startsWith('#')) continue;
                
                const indent = line.match(/^( *)/)[1].length;
                const content = line.trim();
                
                if (content.includes(':')) {
                    const [key, value] = content.split(':', 2);
                    result[key.trim()] = value ? value.trim() : {};
                    currentKey = key.trim();
                    currentIndent = indent;
                }
            }
            
            return result;
        }
    }
    
    /**
     * Docker Compose Schema 验证器
     */
    class ComposeValidator {
        constructor() {
            this.parser = new SimpleYAMLParser();
            
            // Docker Compose 必需字段
            this.requiredFields = {
                root: ['services'],  // 根级别必需字段（version 在 v3+ 是可选的）
                service: []  // 服务级别必需字段（image 或 build 至少要有一个）
            };
            
            // Docker Compose 有效字段
            this.validFields = {
                root: ['version', 'services', 'networks', 'volumes', 'configs', 'secrets'],
                service: [
                    'image', 'build', 'container_name', 'command', 'entrypoint',
                    'environment', 'env_file', 'ports', 'expose', 'volumes',
                    'networks', 'depends_on', 'restart', 'labels', 'logging',
                    'healthcheck', 'deploy', 'configs', 'secrets', 'working_dir',
                    'user', 'hostname', 'domainname', 'mac_address', 'privileged',
                    'read_only', 'stdin_open', 'tty', 'cpu_shares', 'cpu_quota',
                    'cpuset', 'mem_limit', 'memswap_limit', 'mem_reservation',
                    'oom_score_adj', 'shm_size', 'devices', 'dns', 'dns_search',
                    'tmpfs', 'extra_hosts', 'security_opt', 'stop_signal',
                    'stop_grace_period', 'sysctls', 'ulimits', 'userns_mode',
                    'pid', 'ipc', 'cgroup_parent', 'extends', 'links', 'external_links',
                    'network_mode', 'aliases', 'ipv4_address', 'ipv6_address'
                ]
            };
        }
        
        validate(yamlText) {
            const errors = [];
            
            // 1. YAML 语法验证
            const parseResult = this.parser.parse(yamlText);
            errors.push(...parseResult.errors);
            
            if (!parseResult.success || !parseResult.data) {
                return errors;
            }
            
            const data = parseResult.data;
            
            // 2. Docker Compose 结构验证
            // 检查是否有 services 字段
            if (!data.services) {
                errors.push({
                    line: 1,
                    column: 1,
                    message: 'Docker Compose 文件必须包含 "services" 字段',
                    severity: 'error'
                });
                return errors;
            }
            
            // 3. 检查 version 字段（如果存在）
            if (data.version) {
                const version = String(data.version);
                if (!version.match(/^[23]\.\d+$/)) {
                    errors.push({
                        line: this.findLineNumber(yamlText, 'version'),
                        column: 1,
                        message: `不支持的 Compose 文件版本: ${version}（建议使用 2.x 或 3.x）`,
                        severity: 'warning'
                    });
                }
            }
            
            // 4. 验证服务配置
            const servicesLine = this.findLineNumber(yamlText, 'services');
            if (typeof data.services !== 'object') {
                errors.push({
                    line: servicesLine,
                    column: 1,
                    message: '"services" 必须是一个对象',
                    severity: 'error'
                });
                return errors;
            }
            
            // 检查是否至少有一个服务
            const serviceNames = Object.keys(data.services);
            if (serviceNames.length === 0) {
                errors.push({
                    line: servicesLine,
                    column: 1,
                    message: '至少需要定义一个服务',
                    severity: 'error'
                });
            }
            
            // 5. 验证每个服务的配置
            for (const serviceName of serviceNames) {
                const service = data.services[serviceName];
                const serviceLine = this.findLineNumber(yamlText, serviceName, servicesLine);
                
                // 检查服务名称是否有效
                if (!serviceName.match(/^[a-zA-Z0-9._-]+$/)) {
                    errors.push({
                        line: serviceLine,
                        column: 1,
                        message: `无效的服务名称 "${serviceName}"（只能包含字母、数字、点、下划线和连字符）`,
                        severity: 'error'
                    });
                }
                
                // 检查服务配置是否为对象
                if (typeof service !== 'object') {
                    errors.push({
                        line: serviceLine,
                        column: 1,
                        message: `服务 "${serviceName}" 的配置必须是一个对象`,
                        severity: 'error'
                    });
                    continue;
                }
                
                // 检查是否有 image 或 build 字段
                if (!service.image && !service.build) {
                    errors.push({
                        line: serviceLine,
                        column: 1,
                        message: `服务 "${serviceName}" 必须指定 "image" 或 "build" 字段`,
                        severity: 'error'
                    });
                }
                
                // 检查端口格式
                if (service.ports) {
                    this.validatePorts(service.ports, serviceName, serviceLine, yamlText, errors);
                }
                
                // 检查环境变量格式
                if (service.environment) {
                    this.validateEnvironment(service.environment, serviceName, serviceLine, yamlText, errors);
                }
                
                // 检查卷挂载格式
                if (service.volumes) {
                    this.validateVolumes(service.volumes, serviceName, serviceLine, yamlText, errors);
                }
                
                // 检查重启策略
                if (service.restart) {
                    const validRestartPolicies = ['no', 'always', 'on-failure', 'unless-stopped'];
                    if (!validRestartPolicies.includes(service.restart)) {
                        errors.push({
                            line: this.findLineNumber(yamlText, 'restart', serviceLine),
                            column: 1,
                            message: `无效的重启策略 "${service.restart}"（有效值: ${validRestartPolicies.join(', ')}）`,
                            severity: 'error'
                        });
                    }
                }
            }
            
            return errors;
        }
        
        validatePorts(ports, serviceName, serviceLine, yamlText, errors) {
            if (!Array.isArray(ports) && typeof ports !== 'string') {
                errors.push({
                    line: this.findLineNumber(yamlText, 'ports', serviceLine),
                    column: 1,
                    message: `服务 "${serviceName}" 的 ports 必须是数组或字符串`,
                    severity: 'error'
                });
                return;
            }
            
            const portArray = Array.isArray(ports) ? ports : [ports];
            for (const port of portArray) {
                const portStr = String(port);
                // 检查端口格式：8080:80 或 8080
                if (!portStr.match(/^\d+:\d+$/) && !portStr.match(/^\d+$/)) {
                    errors.push({
                        line: this.findLineNumber(yamlText, portStr, serviceLine),
                        column: 1,
                        message: `无效的端口映射格式 "${portStr}"（应该是 "宿主机端口:容器端口" 或 "端口"）`,
                        severity: 'error'
                    });
                }
            }
        }
        
        validateEnvironment(environment, serviceName, serviceLine, yamlText, errors) {
            // environment 可以是对象或数组
            if (typeof environment !== 'object') {
                errors.push({
                    line: this.findLineNumber(yamlText, 'environment', serviceLine),
                    column: 1,
                    message: `服务 "${serviceName}" 的 environment 必须是对象或数组`,
                    severity: 'error'
                });
            }
        }
        
        validateVolumes(volumes, serviceName, serviceLine, yamlText, errors) {
            if (!Array.isArray(volumes)) {
                errors.push({
                    line: this.findLineNumber(yamlText, 'volumes', serviceLine),
                    column: 1,
                    message: `服务 "${serviceName}" 的 volumes 必须是数组`,
                    severity: 'error'
                });
                return;
            }
            
            for (const volume of volumes) {
                const volumeStr = String(volume);
                // 检查卷格式：/host/path:/container/path 或 volume_name:/container/path
                if (!volumeStr.includes(':')) {
                    errors.push({
                        line: this.findLineNumber(yamlText, volumeStr, serviceLine),
                        column: 1,
                        message: `无效的卷挂载格式 "${volumeStr}"（应该是 "宿主机路径:容器路径" 或 "卷名:容器路径"）`,
                        severity: 'warning'
                    });
                }
            }
        }
        
        findLineNumber(text, searchStr, startLine = 1) {
            const lines = text.split('\n');
            for (let i = startLine - 1; i < lines.length; i++) {
                if (lines[i].includes(searchStr)) {
                    return i + 1;
                }
            }
            return startLine;
        }
    }
    
    // 导出到全局
    window.ComposeValidator = ComposeValidator;
})();
