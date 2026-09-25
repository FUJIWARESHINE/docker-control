// Service Worker for DIANCUP
// 缓存版本号将在构建时动态替换
let CACHE_VERSION = 'dev';
let CACHE_NAME = 'diancup-' + CACHE_VERSION;

const urlsToCache = [
    '/',
    '/static/css/style.css',
    '/static/js/app.js',
    '/static/js/version.js',
    '/static/lib/remixicon.css',
    '/static/lib/particles.min.js',
    '/static/manifest.json'
];

// 从服务器获取最新版本号
async function fetchVersion() {
    try {
        const response = await fetch('/api/public/version', { cache: 'no-store' });
        if (response.ok) {
            const data = await response.json();
            return data.version || 'dev';
        }
    } catch (error) {
    }
    return CACHE_VERSION;
}

// 安装 Service Worker
self.addEventListener('install', event => {
    event.waitUntil(
        fetchVersion().then(version => {
            CACHE_VERSION = version;
            CACHE_NAME = 'diancup-' + version;
            
            return caches.open(CACHE_NAME)
                .then(cache => {
                    return cache.addAll(urlsToCache.map(url => new Request(url, {cache: 'reload'})));
                })
                .then(() => {
                    return self.skipWaiting();
                });
        })
    );
});

// 激活 Service Worker
self.addEventListener('activate', event => {
    event.waitUntil(
        fetchVersion().then(version => {
            CACHE_VERSION = version;
            CACHE_NAME = 'diancup-' + version;

            return caches.keys().then(cacheNames => {
                return Promise.all(
                    cacheNames.map(cacheName => {
                        // 删除所有不匹配当前版本的缓存（包括旧的 yjnas- 缓存）
                        if ((cacheName.startsWith('diancup-') || cacheName.startsWith('yjnas-')) && cacheName !== CACHE_NAME) {
                            return caches.delete(cacheName);
                        }
                    })
                );
            }).then(() => {
                return self.clients.claim();
            });
        })
    );
});

// 拦截网络请求
self.addEventListener('fetch', event => {
    event.respondWith(
        caches.match(event.request)
            .then(response => {
                // 缓存命中，返回缓存的资源
                if (response) {
                    return response;
                }

                // 克隆请求，因为请求是流式对象，只能使用一次
                const fetchRequest = event.request.clone();

                return fetch(fetchRequest).then(response => {
                    // 检查是否为有效响应
                    if (!response || response.status !== 200 || response.type !== 'basic') {
                        return response;
                    }

                    // 克隆响应
                    const responseToCache = response.clone();

                    // 将新资源添加到缓存
                    caches.open(CACHE_NAME).then(cache => {
                        cache.put(event.request, responseToCache);
                    });

                    return response;
                }).catch(error => {
                    console.error('[Service Worker] 网络请求失败:', error);
                    // 如果是API请求，不返回缓存
                    if (event.request.url.includes('/api/')) {
                        return new Response(JSON.stringify({ error: '网络离线' }), {
                            status: 503,
                            headers: { 'Content-Type': 'application/json' }
                        });
                    }
                });
            })
    );
});

// 后台同步
self.addEventListener('sync', event => {
    if (event.tag === 'sync-containers') {
        event.waitUntil(syncContainers());
    }
});

// 同步容器数据
async function syncContainers() {
    try {
        await fetch('/api/containers');
    } catch (error) {
    }
}

// 推送通知
self.addEventListener('push', event => {
    const options = {
        body: event.data ? event.data.text() : '昱君NAS容器更新',
        icon: '/static/icons/icon-192x192.png',
        badge: '/static/icons/icon-72x72.png',
        vibrate: [200, 100, 200],
        data: {
            dateOfArrival: Date.now(),
            primaryKey: 1
        },
        actions: [
            {
                action: 'explore',
                title: '查看详情',
                icon: '/static/icons/icon-96x96.png'
            },
            {
                action: 'close',
                title: '关闭',
                icon: '/static/icons/icon-96x96.png'
            }
        ]
    };

    event.waitUntil(
        self.registration.showNotification('昱君NAS', options)
    );
});

// 处理通知点击
self.addEventListener('notificationclick', event => {
    event.notification.close();

    if (event.action === 'explore') {
        event.waitUntil(
            clients.openWindow('/')
        );
    }
});
