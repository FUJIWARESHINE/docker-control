// API 封装：自动带 token / 统一错误处理
(function () {
  'use strict';

  function getToken() { return localStorage.getItem('dc-token') || ''; }

  function handle401() {
    localStorage.removeItem('dc-token');
    location.href = '/static/login.html';
  }

  async function request(method, url, body, raw) {
    const headers = {};
    const tk = getToken();
    if (tk) headers['Authorization'] = 'Bearer ' + tk;
    if (body !== undefined && !(body instanceof FormData)) headers['Content-Type'] = 'application/json';
    const opt = { method, headers };
    if (body !== undefined) opt.body = body instanceof FormData ? body : JSON.stringify(body);
    let resp;
    try {
      resp = await fetch(url, opt);
    } catch (e) {
      throw new Error('网络错误');
    }
    if (resp.status === 401) { handle401(); throw new Error('未授权'); }
    if (raw) return resp;
    let data = null;
    try { data = await resp.json(); } catch (e) { /* 非 JSON */ }
    if (!resp.ok) throw new Error((data && data.message) || ('HTTP ' + resp.status));
    return data;
  }

  window.API = {
    token: getToken,
    get: (u) => request('GET', u),
    post: (u, b) => request('POST', u, b || {}),
    put: (u, b) => request('PUT', u, b || {}),
    del: (u) => request('DELETE', u),
    raw: request,
    // 上传文件
    upload: (u, file) => {
      const fd = new FormData();
      fd.append('file', file);
      return request('POST', u, fd);
    },
    // 文本响应（日志）
    text: async (u) => {
      const resp = await request('GET', u, undefined, true);
      return resp.text();
    },
    // 下载（带 token 的 blob）
    blob: async (u) => {
      const resp = await request('GET', u, undefined, true);
      return resp.blob();
    },
  };
})();
