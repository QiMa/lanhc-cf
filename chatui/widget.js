/* lanhc.com 在线客服挂件：由 cf.lanhc.com 提供，站内只需一行 <script src>。 */
(function () {
  "use strict";
  if (window.__lanhcChatLoaded) return;
  window.__lanhcChatLoaded = true;

  var script = document.currentScript;
  var base = script && script.src ? new URL(script.src).origin : "https://cf.lanhc.com";
  var tokenKey = "lanhc.chat.token";
  var sinceKey = "lanhc.chat.since";
  var title = (script && script.dataset.title) || "在线客服";
  var accent = (script && script.dataset.color) || "#2563eb";
  var pollMs = 4000;

  var host = document.createElement("div");
  host.setAttribute("data-lanhc-chat", "");
  document.body.appendChild(host);
  var root = host.attachShadow ? host.attachShadow({ mode: "open" }) : host;

  root.innerHTML = [
    "<style>",
    "*{box-sizing:border-box}",
    ".launcher{position:fixed;right:20px;bottom:20px;width:56px;height:56px;border-radius:50%;border:0;",
    "background:" + accent + ";color:#fff;box-shadow:0 10px 30px rgba(15,23,42,.28);cursor:pointer;",
    "display:flex;align-items:center;justify-content:center;z-index:2147483000;transition:transform .15s ease}",
    ".launcher:hover{transform:translateY(-2px)}",
    ".launcher svg{width:26px;height:26px;fill:none;stroke:currentColor;stroke-width:1.8;stroke-linecap:round;stroke-linejoin:round}",
    ".panel{position:fixed;right:20px;bottom:88px;width:360px;max-width:calc(100vw - 32px);height:520px;max-height:calc(100vh - 120px);",
    "background:#fff;border-radius:16px;box-shadow:0 24px 60px rgba(15,23,42,.26);display:none;flex-direction:column;",
    "overflow:hidden;z-index:2147483000;font-family:-apple-system,BlinkMacSystemFont,'Segoe UI','PingFang SC','Hiragino Sans GB','Microsoft YaHei',sans-serif;",
    "font-size:14px;line-height:1.55;color:#0f172a}",
    ".panel.open{display:flex}",
    ".head{background:" + accent + ";color:#fff;padding:14px 16px;display:flex;align-items:center;gap:10px}",
    ".head h3{margin:0;font-size:15px;font-weight:600;flex:1}",
    ".dot{width:8px;height:8px;border-radius:50%;background:#94a3b8;flex:none}",
    ".dot.on{background:#34d399}",
    ".status{font-size:12px;opacity:.9}",
    ".close{background:transparent;border:0;color:#fff;font-size:20px;line-height:1;cursor:pointer;padding:0 2px;opacity:.85}",
    ".close:hover{opacity:1}",
    ".log{flex:1;overflow-y:auto;padding:14px 14px 6px;background:#f8fafc;display:flex;flex-direction:column;gap:10px}",
    ".msg{max-width:82%;padding:9px 12px;border-radius:12px;white-space:pre-wrap;word-break:break-word}",
    ".msg.visitor{align-self:flex-end;background:" + accent + ";color:#fff;border-bottom-right-radius:4px}",
    ".msg.agent{align-self:flex-start;background:#fff;border:1px solid #e2e8f0;border-bottom-left-radius:4px}",
    ".msg.system{align-self:center;background:#eef2f7;color:#475569;font-size:12.5px;max-width:92%;text-align:center;border-radius:10px}",
    ".meta{align-self:flex-start;font-size:11px;color:#64748b;margin:-6px 0 0 4px}",
    ".foot{border-top:1px solid #e2e8f0;padding:10px;display:flex;gap:8px;align-items:flex-end;background:#fff}",
    ".foot textarea{flex:1;resize:none;height:42px;max-height:110px;padding:10px 12px;border:1px solid #cbd5e1;border-radius:10px;",
    "font:inherit;outline:none;color:#0f172a}",
    ".foot textarea:focus{border-color:" + accent + "}",
    ".send{border:0;background:" + accent + ";color:#fff;width:42px;height:42px;border-radius:10px;cursor:pointer;flex:none}",
    ".send:disabled{opacity:.5;cursor:default}",
    ".capture{padding:10px 14px;background:#fff;border-top:1px solid #e2e8f0;display:none;gap:6px;flex-wrap:wrap}",
    ".capture.show{display:flex}",
    ".capture p{margin:0 0 2px;font-size:12.5px;color:#475569;flex:1 0 100%}",
    ".capture input{flex:1 1 120px;padding:8px 10px;border:1px solid #cbd5e1;border-radius:8px;font:inherit;outline:none;min-width:0}",
    ".capture button{border:0;background:#0f172a;color:#fff;padding:8px 14px;border-radius:8px;cursor:pointer;font:inherit}",
    ".capture small{flex:1 0 100%;color:#16a34a;font-size:12px}",
    "@media(max-width:420px){.panel{right:8px;left:8px;bottom:80px;width:auto;height:calc(100vh - 110px)}.launcher{right:12px;bottom:12px}}",
    "</style>",
    "<button class='launcher' type='button' aria-label='" + title + "'>",
    "<svg viewBox='0 0 24 24' aria-hidden='true'><path d='M21 11.5a8.4 8.4 0 0 1-9 8.4 9.1 9.1 0 0 1-3.4-.7L3 21l1.8-5.4A8.3 8.3 0 0 1 4 11.5 8.4 8.4 0 0 1 12.5 3 8.4 8.4 0 0 1 21 11.5z'/></svg>",
    "</button>",
    "<section class='panel' role='dialog' aria-label='" + title + "' aria-modal='false'>",
    "<header class='head'><span class='dot'></span><h3>" + title + "</h3><span class='status'></span>",
    "<button class='close' type='button' aria-label='关闭'>×</button></header>",
    "<div class='log' role='log' aria-live='polite'></div>",
    "<form class='capture'>",
    "<p>留下邮箱，客服回复后我们可以邮件通知您。</p>",
    "<input name='name' type='text' placeholder='称呼（可选）' maxlength='40'>",
    "<input name='email' type='email' placeholder='you@company.com' required maxlength='120'>",
    "<button type='submit'>提交</button><small></small>",
    "</form>",
    "<form class='foot'>",
    "<textarea name='text' rows='1' placeholder='请输入您的问题…' maxlength='2000' required></textarea>",
    "<button class='send' type='submit' aria-label='发送'>",
    "<svg viewBox='0 0 24 24' width='20' height='20' fill='none' stroke='currentColor' stroke-width='1.8' stroke-linecap='round' stroke-linejoin='round'><path d='M4 12l16-8-6 16-2.5-6.5L4 12z'/></svg>",
    "</button></form>",
    "</section>",
  ].join("");

  var $ = function (sel) { return root.querySelector(sel); };
  var panel = $(".panel");
  var log = $(".log");
  var form = $(".foot");
  var textarea = $("textarea");
  var sendBtn = $(".send");
  var capture = $(".capture");
  var statusText = $(".status");
  var dot = $(".dot");
  var lastID = Number(localStorage.getItem(sinceKey) || 0) || 0;
  var token = localStorage.getItem(tokenKey) || "";
  var timer = null;
  var opened = false;
  var agentOnline = false;
  var captureAsked = false;

  function api(path, opts) {
    opts = opts || {};
    return fetch(base + path, {
      method: opts.method || "GET",
      headers: opts.body ? { "Content-Type": "application/json" } : undefined,
      body: opts.body ? JSON.stringify(opts.body) : undefined,
      credentials: "omit",
      mode: "cors",
    }).then(function (res) {
      return res.json().catch(function () { return {}; }).then(function (data) {
        if (!res.ok) throw new Error(data.error || res.status);
        return data;
      });
    });
  }

  function append(msg) {
    if (!msg || msg.id <= lastID) return;
    lastID = msg.id;
    localStorage.setItem(sinceKey, String(lastID));
    var div = document.createElement("div");
    div.className = "msg " + (msg.role === "visitor" ? "visitor" : msg.role === "agent" ? "agent" : "system");
    div.textContent = msg.text;
    log.appendChild(div);
    log.scrollTop = log.scrollHeight;
  }

  function render(list) {
    (list || []).forEach(append);
  }

  function setOnline(online) {
    agentOnline = !!online;
    dot.className = "dot" + (agentOnline ? " on" : "");
    statusText.textContent = agentOnline ? "客服在线" : "留言模式";
  }

  function ensureSession() {
    return api("/chat/api/session", {
      method: "POST",
      body: { token: token, page: location.href },
    }).then(function (data) {
      token = data.token;
      localStorage.setItem(tokenKey, token);
      setOnline(data.agent_online);
      render(data.messages);
      return data;
    });
  }

  function poll() {
    if (!token) return;
    api("/chat/api/poll?token=" + encodeURIComponent(token) + "&since=" + lastID)
      .then(function (data) {
        setOnline(data.agent_online);
        render(data.messages);
        if (!agentOnline && !captureAsked && !localStorage.getItem(tokenKey + ".email")) {
          captureAsked = true;
          capture.classList.add("show");
        }
      })
      .catch(function (err) {
        if (String(err.message) === "session not found") {
          localStorage.removeItem(tokenKey);
          token = "";
          lastID = 0;
          localStorage.removeItem(sinceKey);
          ensureSession();
        }
      });
  }

  function startPolling() {
    if (timer) return;
    timer = setInterval(poll, pollMs);
  }

  function stopPolling() {
    if (timer) clearInterval(timer);
    timer = null;
  }

  $(".launcher").addEventListener("click", function () {
    opened = !opened;
    panel.classList.toggle("open", opened);
    if (opened) {
      ensureSession().catch(function () {});
      startPolling();
      poll();
      setTimeout(function () { textarea.focus(); }, 60);
    } else {
      stopPolling();
    }
  });

  $(".close").addEventListener("click", function () {
    opened = false;
    panel.classList.remove("open");
    stopPolling();
  });

  form.addEventListener("submit", function (e) {
    e.preventDefault();
    var text = textarea.value.trim();
    if (!text) return;
    sendBtn.disabled = true;
    ensureSession()
      .then(function () {
        return api("/chat/api/message", { method: "POST", body: { token: token, text: text } });
      })
      .then(function (data) {
        textarea.value = "";
        append(data.message);
        setOnline(data.agent_online);
      })
      .catch(function () {
        textarea.value = text;
      })
      .then(function () { sendBtn.disabled = false; });
  });

  textarea.addEventListener("keydown", function (e) {
    if (e.key === "Enter" && !e.shiftKey) {
      e.preventDefault();
      form.requestSubmit ? form.requestSubmit() : form.dispatchEvent(new Event("submit", { cancelable: true }));
    }
  });

  capture.addEventListener("submit", function (e) {
    e.preventDefault();
    var note = capture.querySelector("small");
    var data = new FormData(capture);
    ensureSession()
      .then(function () {
        return api("/chat/api/email", {
          method: "POST",
          body: { token: token, name: data.get("name") || "", email: data.get("email") || "" },
        });
      })
      .then(function () {
        localStorage.setItem(tokenKey + ".email", "1");
        note.textContent = "已记录，感谢！";
        setTimeout(function () { capture.classList.remove("show"); }, 1200);
      })
      .catch(function (err) { note.textContent = "提交失败：" + err.message; });
  });

  ensureSession().catch(function () {});
})();
