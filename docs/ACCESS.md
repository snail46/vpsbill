# 访问方式：端口、域名与反向代理

客户前台和管理后台分开监听，方便分别绑定域名、分别设置访问限制：

| 入口 | 默认端口 | 内容 |
|---|---|---|
| 客户前台 | `PORTAL_PORT`（默认 8080） | 客户中心、下单、支付回调、Hatch Agent 连接 |
| 管理后台 | `ADMIN_PORT`（默认 8081） | 商家控制台 `/admin`、Web 安装向导 |

两个入口互不相通：前台地址打不开 `/admin`，也调不到管理接口；后台地址不提供客户接口。首次安装要在**后台地址**完成。

Web 容器内部有四个监听端口：80（前台）、81（后台）给直接访问的客户端，7080（前台）、7081（后台）只给可信的反向代理用——它们会从 `CF-Connecting-IP`、`X-Real-IP` 或 `X-Forwarded-For` 里取真实客户端地址，所以只能发布在 `127.0.0.1` 上，不能直接对外。早期版本的可信端口是 8080/8081，现在仍然保留、效果相同，已经指向 `web:8080` / `web:8081` 的隧道不用改；新配置请用 7080/7081。

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

两个域名都解析到服务器，放行 80、443。Caddy 自动申请和续期证书，前台域名转到 Web 容器的 7080，后台域名转到 7081。前台和后台端口只监听 `127.0.0.1`。

### cloudflare：Cloudflare Tunnel（无公网 IP）

1. Cloudflare Zero Trust → Networks → Tunnels → Create a tunnel，选 Cloudflared，复制令牌。
2. 在隧道的 Public Hostname 里添加两条：
   - 前台域名，服务 `HTTP`，URL `web:7080`
   - 后台域名，服务 `HTTP`，URL `web:7081`
3. `.env`：

```dotenv
ACCESS_MODE=cloudflare
CLOUDFLARE_TUNNEL_TOKEN=从 Cloudflare 复制的令牌
```

`deploy.sh` 会启动 `cloudflared` 容器，服务器不需要开放任何入站端口。

隧道的服务地址必须是 `web:7080` / `web:7081`（或自己装的 cloudflared 指向 `127.0.0.1` 上的可信端口，见下一节）。如果指向了直连端口（如 `http://localhost:8088`），所有访客都会被识别成 Docker 内网地址：登录限流互相影响，审计日志和登录记录看不到真实 IP。系统检测到这种情况时，会在后台「站点设置」顶部提示。建议在 Cloudflare Access 里给后台域名加一层登录保护。

### cloudflared 装在宿主机上（不在 Docker 里）

例如 Mac mini 上用 OrbStack 跑 Docker、cloudflared 用 Homebrew 或安装包直接装在 macOS 上。这时 cloudflared 不在 Docker 网络里，解析不到 `web` 这个名字，只能连宿主机端口。不要用 `ACCESS_MODE=cloudflare`（它会再起一个 cloudflared 容器，和本机的 cloudflared 抢同一条隧道），改用 `proxy`：

```dotenv
ACCESS_MODE=proxy
PORTAL_PORT=8080
ADMIN_PORT=8081
```

运行 `./deploy.sh` 后，`127.0.0.1:8080` 和 `127.0.0.1:8081` 分别指向 Web 容器的可信端口 7080 / 7081。在 Cloudflare 隧道的 Public Hostname 里：

- 前台域名，服务 `HTTP`，URL `localhost:8080`
- 后台域名，服务 `HTTP`，URL `localhost:8081`

用 `docker compose` 直接部署预构建镜像（不用 `deploy.sh`）时，在 `.env` 里加上 `PORTAL_BIND=127.0.0.1`、`ADMIN_BIND=127.0.0.1`、`PORTAL_TARGET=7080`、`ADMIN_TARGET=7081`，再 `docker compose up -d`。检查：`docker compose ps` 里 web 的端口应显示为 `127.0.0.1:8080->7080/tcp`、`127.0.0.1:8081->7081/tcp`；如果还是 `->80` / `->81`，就是没有生效。

后台「站点设置」顶部的直连端口提示，在下一个经 Cloudflare 正确转发的请求到达后会自动消失，排查步骤见下文「常见问题」。

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

## 给后台加 Cloudflare Access

Cloudflare Access（Zero Trust 的一部分，50 人以内免费）在后台域名前面多加一道登录：访问后台时先过 Cloudflare 的邮箱验证码或 Google / GitHub 登录，通过后才看到 VPSBill 的后台登录页。这样即使后台账号密码泄露，没有被放行的邮箱也打不开后台。

**前提：后台只能经过 Cloudflare 访问。** Access 拦在 Cloudflare 上，如果后台端口还能从公网直连，就能绕过它。

- 用 `ACCESS_MODE=cloudflare` 或 `ACCESS_MODE=proxy`（cloudflared 装在宿主机上时，见上文）。这两种模式下后台端口只监听 `127.0.0.1`，公网连不上。`docker compose ps` 里 web 的后台端口应为 `127.0.0.1:8081->7081/tcp`。
- `ACCESS_MODE=direct`（IP + 端口）时后台端口对公网开放，先改成上面两种之一，或在防火墙里关掉后台端口。
- 前台和后台要用两个不同的域名，例如前台 `vps.example.com`、后台 `admin.example.com`。前台域名打不开后台页面和后台接口（会返回 404），所以只保护后台域名就够了。

**设置步骤**（Cloudflare 后台的菜单名称以实际界面为准）：

1. 打开 [Cloudflare Zero Trust](https://one.dash.cloudflare.com/)。第一次用要先起一个团队名并选免费方案（Free）。
2. 进入 **Access → Applications → Add an application**，选 **Self-hosted**。
3. 应用名称随意（如「VPSBill 后台」）；**Application domain** 填后台域名 `admin.example.com`，路径留空，表示整个域名都受保护。会话时长（Session Duration）按习惯选，例如 24 小时。
4. 登录方式：默认的 **One-time PIN**（往邮箱发验证码）不用额外配置；也可以在 **Settings → Authentication** 里接入 Google、GitHub 等再在这里勾选。
5. 添加策略（Policy）：动作选 **Allow**，规则 **Include → Emails** 填允许进入后台的邮箱（多个管理员就填多个），或 **Emails ending in** 填公司邮箱后缀。不要用 Everyone。
6. 保存。用浏览器的无痕窗口打开 `https://admin.example.com`，应先看到 Cloudflare 的验证页，输入邮箱收到验证码后，才进入 VPSBill 的后台登录页。

**注意事项：**

- **不要保护前台域名。** 客户、支付平台的回调（`/api/v1/webhooks/payments/…`）和母机上的 Agent 都走前台域名，加了 Access 它们都会被挡住。
- 后台「站点设置」里的**后台地址**填 `https://admin.example.com`，邮件里的后台链接才指向正确的地址。
- Access 是额外的一层，VPSBill 自己的后台密码和二步验证（「登录安全」里的 TOTP）照常保留，建议都开着。
- 新增管理员时，除了在后台建账号，还要把他的邮箱加进 Access 策略。
- 检查是否生效：在服务器以外的机器上执行 `curl -sI https://admin.example.com`，应返回 302 跳转到 `*.cloudflareaccess.com`；再试 `http://服务器IP:8081`，应连不上。

## 页面加载

Web 容器的 nginx 在返回页面时，通过 SSI 把 `/api/v1/boot`（站点名称、Logo、是否需要安装、当前登录的管理员或客户）直接嵌进 HTML，并让浏览器同时下载前台或后台的代码。所以打开页面只等两次往返（HTML 和脚本），再次打开时脚本走缓存，只等 HTML 一次。使用 HTTPS 时，浏览器还会启用 Service Worker（`/sw.js`）：再次打开时直接用浏览器保存的页面，不用等网络；同时在后台取新页面，并核对登录状态（在别处退出或会话过期会立即切到登录页）。发布新版本后，下一次打开即用上新版本。HTTP 访问时浏览器不允许 Service Worker，行为不变。进入后台或客户中心约 1.5 秒后，浏览器在空闲时预取各菜单页的数据，之后点任一菜单都先显示已加载的数据，再在后台刷新。已加载的数据还会存在当前标签页的 sessionStorage 里（只属于当前登录的账号，退出登录或关闭标签页即清除），所以刷新页面时数据也立即显示。

## 不用 deploy.sh 时

直接用 `docker compose` 部署预构建镜像时，`deploy.sh` 做的事情要自己在 `.env` 里写上：

| 模式 | 追加到 `.env` | 启动命令 |
|---|---|---|
| direct | （默认值即可） | `docker compose up -d` |
| caddy | `PORTAL_BIND=127.0.0.1`、`ADMIN_BIND=127.0.0.1`、`PORTAL_TARGET=7080`、`ADMIN_TARGET=7081` | `docker compose --profile tls up -d` |
| cloudflare | 同上 | `docker compose --profile tunnel up -d` |
| proxy | 同上 | `docker compose up -d` |

## 常见问题

### 后台提示「检测到经 Cloudflare 转发的请求进入了直连端口」

意思是：有请求带着 Cloudflare 的 `CF-Connecting-IP` 头，却进了直连端口（Web 容器的 80 / 81）。直连端口不信任转发头，所以所有访客都被记成内网地址：登录限流按同一个 IP 计算、互相影响，审计日志和登录记录看不到真实 IP。按 cloudflared 装在哪里处理：

| cloudflared 在哪 | `.env` | 隧道 Public Hostname 的服务地址 |
|---|---|---|
| Docker 里，由 `deploy.sh` 启动 | `ACCESS_MODE=cloudflare` + `CLOUDFLARE_TUNNEL_TOKEN` | `http://web:7080`（前台）、`http://web:7081`（后台） |
| 宿主机上（Linux 的 systemd 服务、Mac 的 Homebrew / 安装包，包括 OrbStack、Docker Desktop 跑计费站的情况） | `ACCESS_MODE=proxy`（不要用 `cloudflare`，否则会多起一个 cloudflared 容器抢同一条隧道） | `http://localhost:8080`、`http://localhost:8081`（即 `PORTAL_PORT`、`ADMIN_PORT`） |
| 同一 Docker 网络里自己起的 cloudflared 容器 | 任意 | `http://web:7080`、`http://web:7081` |

改完 `.env` 后重新运行 `./deploy.sh`。不用 `deploy.sh`、直接 `docker compose` 部署时，在 `.env` 里写上 `PORTAL_BIND=127.0.0.1`、`ADMIN_BIND=127.0.0.1`、`PORTAL_TARGET=7080`、`ADMIN_TARGET=7081` 再 `docker compose up -d`。

确认是否生效：

1. `docker compose ps` 里 web 的端口应为 `127.0.0.1:8080->7080/tcp`、`127.0.0.1:8081->7081/tcp`；还是 `->80`、`->81` 就说明 `.env` 没生效。
2. 在 Cloudflare 后台核对隧道的服务地址，注意前台、后台两条都要改；多个连接器（connector）连着同一条隧道时，每个连接器所在的机器都要能访问这个地址。
3. 打开一次前台和后台页面。下一个经 Cloudflare 正确转发的请求到达时，提示自动消失（2026-10-01 之前的版本要等 24 小时或重启 API 才消失）。后台「审计日志」里新的记录应显示访客的公网 IP。

## 从旧版本升级

旧版只有一个 `APP_PORT`，前台和后台在同一个端口。升级后 `deploy.sh` 会把 `APP_PORT` 当作 `PORTAL_PORT`，后台默认在 8081；填了 `DOMAIN` 的旧配置按 `caddy` 处理，但还需要补上 `ADMIN_DOMAIN`。管理员的登录 Cookie 改了名字，升级后需要重新登录一次。
