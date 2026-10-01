package app

import (
	"net"
	"net/http"
	"sync/atomic"
	"time"
)

// misrouted is when a request last came through Cloudflare but reached a
// listener that does not trust forwarding headers (see web/nginx), as when
// a Cloudflare Tunnel points at the published direct port instead of a
// trusted one. Every visitor then shows up with a private address: login
// rate limits are shared by everyone and audit logs lose the real
// addresses. routed is when one last arrived the right way, with the
// visitor's own address.
var misrouted, routed atomic.Int64

func watchProxy(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("CF-Connecting-IP") != "" {
			if ip := net.ParseIP(remoteIP(r)); ip != nil && (ip.IsPrivate() || ip.IsLoopback()) {
				misrouted.Store(time.Now().Unix())
			} else if ip != nil {
				routed.Store(time.Now().Unix())
			}
		}
		next.ServeHTTP(w, r)
	})
}

// proxyWarning explains a misrouted tunnel seen within the last day. Once a
// Cloudflare request arrives the right way after it, the tunnel has been
// fixed and the warning goes away.
func proxyWarning() string {
	seen := misrouted.Load()
	if seen == 0 || time.Since(time.Unix(seen, 0)) > 24*time.Hour || routed.Load() > seen {
		return ""
	}
	return "检测到经 Cloudflare 转发的请求进入了直连端口，所有访客的 IP 都被识别成内网地址（登录限流会互相影响，审计日志看不到真实 IP）。" +
		"cloudflared 跑在 Docker 里（ACCESS_MODE=cloudflare）时，隧道的服务地址填 http://web:7080（前台）和 http://web:7081（后台）；" +
		"cloudflared 装在宿主机上（不在 Docker 里）时，在 .env 设置 ACCESS_MODE=proxy 并重新运行 ./deploy.sh，隧道填 http://127.0.0.1:前台端口 和 http://127.0.0.1:后台端口（PORTAL_PORT、ADMIN_PORT，默认 8080、8081）。" +
		"改好后，下一个经 Cloudflare 的正确请求到达时此提示自动消失。详见部署文档 ACCESS.md。"
}
