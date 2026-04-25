package adminapi

import (
	"bytes"
	"embed"
	"html/template"
	"io/fs"
	"net/http"
)

//go:embed templates/*.html
var templateFS embed.FS

//go:embed static
var staticFS embed.FS

type pageData struct {
	Page  string
	Title string
}

func RegisterFrontend(mux *http.ServeMux) {
	staticContent, _ := fs.Sub(staticFS, "static")
	mux.Handle("/static/", http.StripPrefix("/static/", http.FileServer(http.FS(staticContent))))

	pages := map[string]struct {
		title string
		js    string
		html  string
	}{
		"/":          {title: "总览", js: "loadOverview();startAutoRefresh();", html: overviewHTML},
		"/requests":  {title: "请求记录", js: "loadRequests(false,true);startRequestsAutoRefresh();", html: requestsHTML},
		"/endpoints": {title: "节点管理", js: "loadEndpoints();startEndpointsAutoRefresh();", html: endpointsHTML},
	}

	layout := template.Must(template.New("layout").Parse(layoutStr))

	for path, pg := range pages {
		p := path
		pageHTML := pg.html
		pageTitle := pg.title
		pageJS := pg.js
		mux.HandleFunc(p, func(w http.ResponseWriter, r *http.Request) {
			if p == "/" && r.URL.Path != "/" {
				http.NotFound(w, r)
				return
			}
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			var content bytes.Buffer
			content.WriteString(pageHTML)
			content.WriteString("\n<script>" + pageJS + "</script>")
			layout.Execute(w, map[string]any{
				"Page":    p,
				"Title":   pageTitle,
				"Content": template.HTML(content.String()),
			})
		})
	}
}

const layoutStr = `<!DOCTYPE html>
<html>
<head>
<title>MiniRoute{{if .Title}} - {{.Title}}{{end}}</title>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<link rel="stylesheet" href="/static/style.css">
<script src="/static/app.js"></script>
</head>
<body>
<nav>
  <span class="brand">MiniRoute</span>
  <a href="/" {{if eq .Page "/"}}class="active"{{end}}>总览</a>
  <a href="/requests" {{if eq .Page "/requests"}}class="active"{{end}}>请求记录</a>
  <a href="/endpoints" {{if eq .Page "/endpoints"}}class="active"{{end}}>节点管理</a>
  <div class="nav-right">
    <button class="theme-toggle" id="theme-toggle" title="切换主题" onclick="toggleTheme()">🌙</button>
  </div>
</nav>
<main>
{{.Content}}
</main>
<script>initTheme();</script>
</body>
</html>`

const overviewHTML = `
<div class="section">
  <h2>运行总览 <span class="auto-refresh"><span class="spinner">&#9696;</span> 每秒自动刷新</span></h2>
  <div class="cards">
    <div class="card"><div class="label">运行时长</div><div class="value" id="uptime">-</div></div>
    <div class="card"><div class="label">近1小时请求数</div><div class="value" id="requests-1h">-</div></div>
    <div class="card"><div class="label">近1小时成功数</div><div class="value green" id="success-1h">-</div></div>
    <div class="card"><div class="label">成功率</div><div class="value" id="success-rate">-</div></div>
    <div class="card"><div class="label">进行中请求</div><div class="value" id="inflight">0</div></div>
  </div>
</div>
<div class="section">
  <h2>Token 用量(输入 / 输出 / 总计)</h2>
  <div class="cards">
    <div class="card"><div class="label">近1小时</div><div class="value" id="token-1h">0 / 0 / 0</div></div>
    <div class="card"><div class="label">近5小时</div><div class="value" id="token-5h">0 / 0 / 0</div></div>
    <div class="card"><div class="label">当天</div><div class="value" id="token-today">0 / 0 / 0</div></div>
    <div class="card"><div class="label">当月</div><div class="value" id="token-month">0 / 0 / 0</div></div>
    <div class="card"><div class="label">累计</div><div class="value" id="token-total">0 / 0 / 0</div></div>
  </div>
</div>
<div class="section">
  <h2>节点状态 <span id="peak-badge" style="display:none" class="badge yellow">高峰期</span></h2>
  <table>
    <thead><tr><th>名称</th><th>提供商</th><th>优先级</th><th>状态</th><th>近1小时请求</th><th>近1小时成功率</th></tr></thead>
    <tbody id="endpoints-tbody"></tbody>
  </table>
</div>`

const requestsHTML = `
<div class="section">
  <h2>请求记录</h2>
  <div class="filters">
    <input type="text" id="filter-model" placeholder="模型筛选" onkeyup="loadRequests(false,true)">
    <select id="filter-status" onchange="loadRequests(false,true)">
      <option value="">全部状态</option>
      <option value="success">成功</option>
      <option value="fail">失败</option>
    </select>
  </div>
  <div class="request-stream" id="requests-container"></div>
</div>`

const endpointsHTML = `
<div class="section">
  <h2>节点管理 <span class="auto-refresh"><span class="spinner">&#9696;</span> 每秒自动刷新</span></h2>
  <div class="endpoint-cards" id="endpoints-container"></div>
</div>`
