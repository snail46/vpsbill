# 访问方式：端口、域名与反向代理

客户前台和管理后台分开监听，方便分别绑定域名、分别设置访问限制：

| 入口 | 默认端口 | 内容 |
|---|---|---|
| 客户前台 | `PORTAL_PORT`（默认 8080） | 客户中心、下单、支付回调、Hatch Agent 连接 |
| 管理后台 | `ADMIN_PORT`（默认 8081） | 商家控制台 `/admin`、Web 安装向导 |

两个入口互不相通：前台地址打不开 `/admin`，也调不到管理接口；后台地址不提供客户接口。首次安装要在**后台地址**完成。

Web 容器内部有四个监听端口：80（前台）、81（后台）给直接访问的客户端，8080（前台）、8081（后台）只给可信的反向代理用——它们会从 `CF-Connecting-IP`、`X-Real-IP` 或 `X-Forwarded-For` 里取真实客户端地址，所以只能发布在 `127.0.0.1` 上，不能直接对外。

## 选择访问方式

在 `.env` 里设置 `ACCESS_MODE`，然后执行 `./deploy.sh`。脚本按模式发布端口、启用对应组件：

| ACCESS_MODE | 适用场景 | 需要填写 |
|---|---|---|
| `direct` | 没有域名，直接 `IP:端口` 访问 | `PORTAL_PORT`、`ADMIN_PORT` |
| `caddy` | 有公网 IP，自动申请 HTTPS 证书 | `DOMAIN`（前台）、`ADMIN_DOMAIN`（后台） |
| `cloudflare` | 没有公网 IP，走 Cloudflare Tunnel | `CLOUDFLARE_TUNNEL_TOKEN` |
| `proxy` | 已经有宝塔 / 1Panel / Nginx 等反向代理 | `PORTAL_PORT`、`ADMIN_PORT` |

部署完成后，到后台「站点设置」填写**公开访问地址**（前台地址，用于邮件链接、支付回调和 Agent 连接）和**后台访问地址**（发给管理员的邮件链接指向这里）。

### direct：IP + 端口

```dotenv
ACCESS_MODE=direct
PORTAL_PORT=8080
ADMIN_PORT=8081
```

前台 `http://服务器IP:8080`，后台 `http://服务器IP:8081`。云厂商安全组放行这两个端口；建议后台端口只对自己的 IP 放行。没有 HTTPS 时 Hatch Agent 只能经本机连接，托管中心只能接入与计费站同机的母鸡，正式对外请用下面三种方式之一。

### caddy：自动 HTTPS 证书

```dotenv
ACCESS_MODE=caddy
DOMAIN=billing.example.com
ADMIN_DOMAIN=admin.example.com
```

两个域名都解析到服务器，放行 80、443。Caddy 自动申请和续期证书，前台域名转到 Web 容器的 8080，后台域名转到 8081。前台和后台端口只监听 `127.0.0.1`。

### cloudflare：Cloudflare Tunnel（无公网 IP）

1. Cloudflare Zero Trust → Networks → Tunnels → Create a tunnel，选 Cloudflared，复制令牌。
2. 在隧道的 Public Hostname 里添加两条：
   - 前台域名，服务 `HTTP`，URL `web:8080`
   - 后台域名，服务 `HTTP`，URL `web:8081`
3. `.env`：

```dotenv
ACCESS_MODE=cloudflare
CLOUDFLARE_TUNNEL_TOKEN=从 Cloudflare 复制的令牌
```

`deploy.sh` 会启动 `cloudflared` 容器，服务器不需要开放任何入站端口。

隧道的服务地址必须是 `web:8080` / `web:8081`（或自己装的 cloudflared 指向 `127.0.0.1` 上的可信端口）。如果指向了直连端口（如 `http://localhost:8088`），所有访客都会被识别成 Docker 内网地址：登录限流互相影响，审计日志和登录记录看不到真实 IP。系统检测到这种情况时，会在后台「站点设置」顶部提示。建议在 Cloudflare Access 里给后台域名加一层登录保护。

### proxy：自己的反向代理

```dotenv
ACCESS_MODE=proxy
PORTAL_PORT=8080
ADMIN_PORT=8081
```

两个端口只监听 `127.0.0.1`。在反向代理里把前台域名转到 `http://127.0.0.1:8080`、后台域名转到 `http://127.0.0.1:8081`，并传递真实地址和协议。Nginx 示例：

```nginx
location / {
    proxy_pass http://127.0.0.1:8080;          # 后台域名改为 8081
    proxy_http_version 1.1;
    proxy_set_header Host $host;
    proxy_set_header X-Real-IP $remote_addr;
    proxy_set_header X-Forwarded-For $remote_addr;
    proxy_set_header X-Forwarded-Proto $scheme;
    proxy_set_header Upgrade $http_upgrade;      # WebSSH、聊天室和 Agent 需要 WebSocket
    proxy_set_header Connection "upgrade";
    proxy_read_timeout 1d;
}
```

页面 HTML 里嵌着当前的登录状态（打开页面时不用再等 API，见下文「页面加载」），代理和 CDN 不要缓存 HTML；`/assets/` 下的脚本和样式带内容哈希，可以长期缓存。

## 页面加载

Web 容器的 nginx 在返回页面时，通过 SSI 把 `/api/v1/boot`（站点名称、Logo、是否需要安装、当前登录的管理员或客户）直接嵌进 HTML，并让浏览器同时下载前台或后台的代码。所以打开页面只等两次往返（HTML 和脚本），再次打开时脚本走缓存，只等 HTML 一次。使用 HTTPS 时，浏览器还会启用 Service Worker（`/sw.js`）：再次打开时直接用浏览器保存的页面，不用等网络；同时在后台取新页面，并核对登录状态（在别处退出或会话过期会立即切到登录页）。发布新版本后，下一次打开即用上新版本。HTTP 访问时浏览器不允许 Service Worker，行为不变。进入后台或客户中心约 1.5 秒后，浏览器在空闲时预取各菜单页的数据，之后点任一菜单都先显示已加载的数据，再在后台刷新。已加载的数据还会存在当前标签页的 sessionStorage 里（只属于当前登录的账号，退出登录或关闭标签页即清除），所以刷新页面时数据也立即显示。

## 不用 deploy.sh 时

直接用 `docker compose` 部署预构建镜像时，`deploy.sh` 做的事情要自己在 `.env` 里写上：

| 模式 | 追加到 `.env` | 启动命令 |
|---|---|---|
| direct | （默认值即可） | `docker compose up -d` |
| caddy | `PORTAL_BIND=127.0.0.1`、`ADMIN_BIND=127.0.0.1`、`PORTAL_TARGET=8080`、`ADMIN_TARGET=8081` | `docker compose --profile tls up -d` |
| cloudflare | 同上 | `docker compose --profile tunnel up -d` |
| proxy | 同上 | `docker compose up -d` |

## 从旧版本升级

旧版只有一个 `APP_PORT`，前台和后台在同一个端口。升级后 `deploy.sh` 会把 `APP_PORT` 当作 `PORTAL_PORT`，后台默认在 8081；填了 `DOMAIN` 的旧配置按 `caddy` 处理，但还需要补上 `ADMIN_DOMAIN`。管理员的登录 Cookie 改了名字，升级后需要重新登录一次。
