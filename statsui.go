package main

import (
	"io"
	"net/http"
	"strings"
)

// statsCollectJS is the drop-in replacement for the counter.dev snippet. It
// only reports same-origin-free page views to cf.lanhc.com and never stores
// cookies, IPs or user agents.
const statsCollectJS = `(function () {
  "use strict";
  try {
    var sid = sessionStorage.getItem("lh_sid");
    if (!sid) {
      sid = Math.random().toString(36).slice(2) + Date.now().toString(36);
      sessionStorage.setItem("lh_sid", sid);
    }
    var body = JSON.stringify({
      p: location.pathname,
      r: document.referrer || "",
      s: (screen.width || 0) + "x" + (screen.height || 0),
      sid: sid,
      l: (document.documentElement.lang || navigator.language || "").slice(0, 12)
    });
    var url = "__ENDPOINT__";
    if (navigator.sendBeacon) {
      navigator.sendBeacon(url, new Blob([body], { type: "text/plain;charset=UTF-8" }));
    } else {
      fetch(url, { method: "POST", body: body, keepalive: true, headers: { "Content-Type": "text/plain" } });
    }
  } catch (e) {
    /* 统计失败不影响页面 */
  }
})();
`

func (s *StatsService) serveCollectJS(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/javascript; charset=utf-8")
	w.Header().Set("Cache-Control", "public, max-age=600")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	endpoint := env("STATS_ENDPOINT", "https://cf.lanhc.com/stats/api/collect")
	_, _ = io.WriteString(w, strings.Replace(statsCollectJS, "__ENDPOINT__", endpoint, 1))
}

const statsDashboardHTML = `<!DOCTYPE html>
<html lang="zh-CN">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width,initial-scale=1">
<meta name="robots" content="noindex,nofollow">
<title>lanhc.com 访问统计</title>
<style>
  :root { color-scheme: dark; }
  * { box-sizing: border-box; }
  body { margin: 0; padding: 24px; font: 14px/1.6 ui-monospace, SFMono-Regular, Menlo, monospace;
         background: #070d24; color: #e2e8f0; }
  h1 { font-size: 18px; margin: 0 0 4px; letter-spacing: .04em; }
  .sub { color: #7f8ca8; margin-bottom: 20px; }
  .bar { display: flex; gap: 8px; flex-wrap: wrap; align-items: center; margin-bottom: 20px; }
  input, button, select { font: inherit; background: #101a3a; color: #e2e8f0;
         border: 1px solid #24325c; border-radius: 6px; padding: 7px 10px; }
  button { cursor: pointer; }
  button:hover { border-color: #3b82f6; }
  .cards { display: grid; grid-template-columns: repeat(auto-fit, minmax(150px, 1fr)); gap: 12px; margin-bottom: 20px; }
  .card { background: #0d1530; border: 1px solid #1c2748; border-radius: 10px; padding: 14px 16px; }
  .card .k { color: #7f8ca8; font-size: 12px; }
  .card .v { font-size: 24px; font-weight: 700; color: #93c5fd; }
  table { width: 100%; border-collapse: collapse; margin-bottom: 24px; }
  th, td { text-align: left; padding: 6px 8px; border-bottom: 1px solid #1c2748; }
  th { color: #7f8ca8; font-weight: 500; }
  td.n { text-align: right; }
  .cols { display: grid; grid-template-columns: repeat(auto-fit, minmax(280px, 1fr)); gap: 24px; }
  .spark { display: flex; align-items: flex-end; gap: 3px; height: 72px; margin-bottom: 8px; }
  .spark i { flex: 1; background: #3b82f6; border-radius: 2px 2px 0 0; min-height: 2px; opacity: .85; }
  .msg { color: #fca5a5; }
</style>
</head>
<body>
<h1>lanhc.com 访问统计</h1>
<div class="sub">自托管计数 · 不写 cookie、不存 IP 与 UA · 数据落盘于 cf.lanhc.com</div>

<div class="bar">
  <input id="token" type="password" placeholder="管理口令" autocomplete="current-password">
  <select id="range">
    <option value="7">近 7 天</option>
    <option value="14">近 14 天</option>
    <option value="30" selected>近 30 天</option>
    <option value="90">近 90 天</option>
  </select>
  <button id="load">查询</button>
  <span id="msg" class="msg"></span>
</div>

<div class="cards">
  <div class="card"><div class="k">区间浏览量</div><div class="v" id="views">-</div></div>
  <div class="card"><div class="k">区间会话数</div><div class="v" id="sessions">-</div></div>
  <div class="card"><div class="k">累计（保留 400 天）</div><div class="v" id="total">-</div></div>
  <div class="card"><div class="k">区间</div><div class="v" id="rangeLabel" style="font-size:14px">-</div></div>
</div>

<div class="cols">
  <div>
    <h2 style="font-size:14px;color:#7f8ca8">每日浏览量</h2>
    <div class="spark" id="spark"></div>
    <table id="days"><thead><tr><th>日期</th><th class="n">浏览</th><th class="n">会话</th></tr></thead><tbody></tbody></table>
  </div>
  <div>
    <h2 style="font-size:14px;color:#7f8ca8">热门页面</h2>
    <table id="paths"><thead><tr><th>路径</th><th class="n">浏览</th></tr></thead><tbody></tbody></table>
    <h2 style="font-size:14px;color:#7f8ca8">外部来源</h2>
    <table id="refs"><thead><tr><th>来源</th><th class="n">浏览</th></tr></thead><tbody></tbody></table>
  </div>
</div>

<script>
(function () {
  var $ = function (id) { return document.getElementById(id); };
  var token = $("token"), msg = $("msg");

  try {
    var saved = sessionStorage.getItem("lh_stats_token");
    if (saved) { token.value = saved; load(); }
  } catch (e) {}

  function esc(v) {
    return String(v).replace(/[&<>"']/g, function (c) {
      return { "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" }[c];
    });
  }

  function fill(id, rows, keyLabel) {
    var tb = $(id).querySelector("tbody");
    tb.innerHTML = rows.length ? rows.map(function (r) {
      return "<tr><td>" + esc(r.key) + "</td><td class='n'>" + r.views + "</td></tr>";
    }).join("") : "<tr><td colspan='2' style='color:#7f8ca8'>暂无数据</td></tr>";
  }

  function load() {
    msg.textContent = "";
    var days = $("range").value;
    fetch("/stats/api/summary?days=" + encodeURIComponent(days), {
      headers: { Authorization: "Bearer " + token.value }
    }).then(function (r) {
      if (r.status === 401) { throw new Error("口令不正确"); }
      if (!r.ok) { throw new Error("HTTP " + r.status); }
      return r.json();
    }).then(function (d) {
      try { sessionStorage.setItem("lh_stats_token", token.value); } catch (e) {}
      $("views").textContent = d.views;
      $("sessions").textContent = d.sessions;
      $("total").textContent = d.total;
      $("rangeLabel").textContent = d.from + " → " + d.to;

      var max = Math.max.apply(null, d.days.map(function (x) { return x.views; }).concat([1]));
      $("spark").innerHTML = d.days.map(function (x) {
        var h = Math.round((x.views / max) * 100);
        return "<i style='height:" + h + "%' title='" + x.day + ": " + x.views + "'></i>";
      }).join("");

      var tb = $("days").querySelector("tbody");
      tb.innerHTML = d.days.slice().reverse().map(function (x) {
        return "<tr><td>" + x.day + "</td><td class='n'>" + x.views + "</td><td class='n'>" + x.sessions + "</td></tr>";
      }).join("");

      fill("paths", d.top_paths || []);
      fill("refs", d.top_refs || []);
    }).catch(function (e) {
      msg.textContent = e.message;
    });
  }

  $("load").addEventListener("click", load);
  token.addEventListener("keydown", function (e) { if (e.key === "Enter") { load(); } });
})();
</script>
</body>
</html>
`

func (s *StatsService) serveDashboard(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Robots-Tag", "noindex, nofollow")
	_, _ = io.WriteString(w, statsDashboardHTML)
}
