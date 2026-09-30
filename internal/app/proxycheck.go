package app

import (
	"net"
	"net/http"
	"sync/atomic"
	"time"
)

// misrouted is when a request last came through Cloudflare but reached a
// listener that does not trust forwarding headers (see web/nginx), as when
// a Cloudflare Tunnel points at the published port instead of web:8080.
// Every visitor then shows up with the Docker bridge address: login rate
// limits are shared by everyone and audit logs lose the real addresses.
var misrouted atomic.Int64

func watchProxy(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("CF-Connecting-IP") != "" {
			if ip := net.ParseIP(remoteIP(r)); ip != nil && (ip.IsPrivate() || ip.IsLoopback()) {
				misrouted.Store(time.Now().Unix())
			}
		}
		next.ServeHTTP(w, r)
	})
}

// proxyWarning explains a misrouted tunnel seen within the last day.
func proxyWarning() string {
	if seen := misrouted.Load(); seen == 0 || time.Since(time.Unix(seen, 0)) > 24*time.Hour {
		return ""
	}
	return "检测到经 Cloudflare 转发的请求进入了直连端口，所有访客的 IP 都被识别成内网地址（登录限流会互相影响，审计日志看不到真实 IP）。请按部署文档 ACCESS.md 把隧道的服务地址改为 web:8080（前台）和 web:8081（后台），或在 .env 设置 ACCESS_MODE=cloudflare 后重新部署。"
}
