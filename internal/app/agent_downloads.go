package app

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

// agentDownloadFiles lists what the API image bundles for host nodes; nothing
// else in the directory is ever served.
var agentDownloadFiles = map[string]string{
	"hatch-agent-linux-amd64": "application/octet-stream",
	"hatch-agent-linux-arm64": "application/octet-stream",
	"SHA256SUMS":              "text/plain; charset=utf-8",
	"install.sh":              "text/x-shellscript; charset=utf-8",
	"hatch-agent.service":     "text/plain; charset=utf-8",
}

// agentDownloads serves the Hatch agent release bundled with this API build,
// so a node can install a matching agent straight from the billing site.
func agentDownloads(dir string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		name := r.PathValue("file")
		contentType, ok := agentDownloadFiles[name]
		if !ok || strings.TrimSpace(dir) == "" {
			http.NotFound(w, r)
			return
		}
		file, err := os.Open(filepath.Join(dir, name))
		if err != nil {
			http.NotFound(w, r)
			return
		}
		defer file.Close()
		info, err := file.Stat()
		if err != nil || info.IsDir() {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", contentType)
		w.Header().Set("Cache-Control", "no-cache")
		http.ServeContent(w, r, name, info.ModTime(), file)
	}
}
