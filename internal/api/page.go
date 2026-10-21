package api

const indexHTML = `<!DOCTYPE html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>mj-cdn-download</title>
  <style>
    body { font: 16px/1.6 "Segoe UI", "PingFang SC", sans-serif; max-width: 760px; margin: 40px auto; padding: 0 20px; color: #1a1a1a; background: #fff; }
    code, pre { font-family: ui-monospace, Consolas, monospace; font-size: 14px; }
    pre { background: #f4f4f4; padding: 12px 16px; overflow: auto; }
    a { color: #0b57d0; }
    h1 { font-size: 28px; margin-bottom: 8px; }
    .muted { color: #555; }
    .langs { margin: 0 0 18px; }
    .langs button { background: none; border: 0; padding: 0; font: inherit; color: #777; cursor: pointer; }
    .langs button[aria-pressed="true"] { color: #1a1a1a; font-weight: 650; }
    .langs button + button::before { content: "|"; margin: 0 8px; color: #ccc; font-weight: 400; }
  </style>
</head>
<body>
  <p class="langs">
    <button type="button" data-set-lang="en" aria-pressed="true">English</button>
    <button type="button" data-set-lang="zh" aria-pressed="false">中文</button>
  </p>
  <h1>mj-cdn-download</h1>
  <div class="lang" data-lang="en">
    <p class="muted">Midjourney keeps images on <code>cdn.midjourney.com</code>. Send the URL, and this service saves the file on your own storage. <code>proxy</code> is optional.</p>
    <pre>POST /download
Content-Type: application/json

{
  "url": "https://cdn.midjourney.com/xxxx/0_0.png",
  "proxy": "http://user:pass@host:port"
}</pre>
  </div>
  <div class="lang" data-lang="zh" hidden>
    <p class="muted">Midjourney 的图在 <code>cdn.midjourney.com</code>。把地址传过来，文件会存到你自己的存储。<code>proxy</code> 可以不传。</p>
    <pre>POST /download
Content-Type: application/json

{
  "url": "https://cdn.midjourney.com/xxxx/0_0.png",
  "proxy": "http://user:pass@host:port"
}</pre>
  </div>
  <script>
    function applyLang(lang) {
      if (lang !== "zh") lang = "en";
      document.documentElement.lang = lang === "zh" ? "zh-CN" : "en";
      document.querySelectorAll(".lang").forEach(function (el) {
        el.hidden = el.getAttribute("data-lang") !== lang;
      });
      document.querySelectorAll("[data-set-lang]").forEach(function (btn) {
        btn.setAttribute("aria-pressed", btn.getAttribute("data-set-lang") === lang ? "true" : "false");
      });
      try { localStorage.setItem("mj-lang", lang); } catch (e) {}
    }
    document.querySelectorAll("[data-set-lang]").forEach(function (btn) {
      btn.addEventListener("click", function () { applyLang(btn.getAttribute("data-set-lang")); });
    });
    var saved = "en";
    try { saved = localStorage.getItem("mj-lang") || "en"; } catch (e) {}
    applyLang(saved);
  </script>
</body>
</html>
`
