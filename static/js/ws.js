// WebSocket：自动重连 + 消息分发
(function () {
  'use strict';

  let sock = null;
  let retry = 0;
  const handlers = {};
  let closedByUser = false;

  function connect() {
    closedByUser = false;
    const proto = location.protocol === 'https:' ? 'wss' : 'ws';
    const tk = localStorage.getItem('dc-token') || '';
    sock = new WebSocket(`${proto}://${location.host}/ws?token=${encodeURIComponent(tk)}`);

    sock.onopen = () => { retry = 0; };

    sock.onmessage = (ev) => {
      let msg;
      try { msg = JSON.parse(ev.data); } catch (e) { return; }
      (handlers[msg.type] || []).forEach(fn => {
        try { fn(msg.data || {}); } catch (e) { console.error(e); }
      });
    };

    sock.onclose = () => {
      if (closedByUser) return;
      retry = Math.min(retry + 1, 6);
      setTimeout(connect, 1000 * retry);
    };
  }

  window.WS = {
    on(type, fn) {
      (handlers[type] = handlers[type] || []).push(fn);
    },
    off(type, fn) {
      const arr = handlers[type] || [];
      const i = arr.indexOf(fn);
      if (i >= 0) arr.splice(i, 1);
    },
    close() {
      closedByUser = true;
      if (sock) sock.close();
    },
    connect,
  };
})();
