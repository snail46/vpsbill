package backup

import (
	"context"
	"crypto/rand"
	"crypto/tls"
	"encoding/hex"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"sort"
	"strconv"
	"strings"
	"time"
)

// WebDAV is where backups are copied off the server: Nextcloud, a NAS,
// Jianguoyun (dav.jianguoyun.com/dav/), AList and the like.
type WebDAV struct {
	URL       string
	Username  string
	Password  string
	Directory string
	// Insecure skips certificate checks, for a NAS with a self-signed one.
	Insecure bool
}

// RemoteFile is a backup stored on the WebDAV server.
type RemoteFile struct {
	Name       string    `json:"name"`
	SizeBytes  int64     `json:"size_bytes"`
	ModifiedAt time.Time `json:"modified_at"`
}

func (w WebDAV) Configured() bool { return strings.TrimSpace(w.URL) != "" }

func (w WebDAV) client() *http.Client {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	if w.Insecure {
		transport.TLSClientConfig = &tls.Config{InsecureSkipVerify: true} // #nosec G402 -- chosen by the administrator for a self-signed NAS
	}
	return &http.Client{Transport: transport}
}

// segments is the backup directory split into path segments.
func (w WebDAV) segments() []string {
	var parts []string
	for _, part := range strings.Split(w.Directory, "/") {
		if part = strings.TrimSpace(part); part != "" && part != "." && part != ".." {
			parts = append(parts, part)
		}
	}
	return parts
}

// collectionURL is the address of the first n directory segments.
func (w WebDAV) collectionURL(n int) string {
	address := strings.TrimRight(strings.TrimSpace(w.URL), "/") + "/"
	for _, part := range w.segments()[:n] {
		address += url.PathEscape(part) + "/"
	}
	return address
}

func (w WebDAV) fileURL(name string) string {
	return w.collectionURL(len(w.segments())) + url.PathEscape(name)
}

func (w WebDAV) request(ctx context.Context, method, address string, body io.Reader, size int64, header http.Header) (*http.Response, error) {
	request, err := http.NewRequestWithContext(ctx, method, address, body)
	if err != nil {
		return nil, fmt.Errorf("WebDAV 地址无效：%w", err)
	}
	for key, values := range header {
		request.Header[key] = values
	}
	if body != nil {
		request.ContentLength = size
	}
	if w.Username != "" || w.Password != "" {
		request.SetBasicAuth(w.Username, w.Password)
	}
	response, err := w.client().Do(request)
	if err != nil {
		return nil, fmt.Errorf("无法连接 WebDAV 服务器：%w", err)
	}
	return response, nil
}

// statusError turns a failed response into a message for the administrator.
func statusError(action string, response *http.Response) error {
	detail, _ := io.ReadAll(io.LimitReader(response.Body, 300))
	switch response.StatusCode {
	case http.StatusUnauthorized:
		return fmt.Errorf("%s失败：用户名或密码错误（401）", action)
	case http.StatusForbidden:
		return fmt.Errorf("%s失败：没有权限（403），请检查账号权限或应用密码", action)
	case http.StatusNotFound:
		return fmt.Errorf("%s失败：地址不存在（404），请检查 WebDAV 地址", action)
	case http.StatusInsufficientStorage:
		return fmt.Errorf("%s失败：WebDAV 空间不足（507）", action)
	}
	text := strings.TrimSpace(string(detail))
	if len(text) > 120 {
		text = text[:120]
	}
	return fmt.Errorf("%s失败：HTTP %d %s", action, response.StatusCode, text)
}

// ensureDirectory creates the backup directory one level at a time.
func (w WebDAV) ensureDirectory(ctx context.Context) error {
	for n := 1; n <= len(w.segments()); n++ {
		response, err := w.request(ctx, "MKCOL", w.collectionURL(n), nil, 0, nil)
		if err != nil {
			return err
		}
		response.Body.Close()
		switch {
		case response.StatusCode < 300, response.StatusCode == http.StatusMethodNotAllowed,
			response.StatusCode == http.StatusMovedPermanently, response.StatusCode == http.StatusFound:
			// created, or already there
		default:
			return statusError("创建备份目录", response)
		}
	}
	return nil
}

// Upload copies a local file into the backup directory.
func (w WebDAV) Upload(ctx context.Context, name, source string) error {
	if err := w.ensureDirectory(ctx); err != nil {
		return err
	}
	file, err := os.Open(source)
	if err != nil {
		return err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return err
	}
	response, err := w.request(ctx, http.MethodPut, w.fileURL(name), file, info.Size(), http.Header{"Content-Type": {"application/x-tar"}})
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode >= 300 {
		return statusError("上传", response)
	}
	return nil
}

type multistatus struct {
	Responses []struct {
		Href      string `xml:"href"`
		Propstats []struct {
			Status string `xml:"status"`
			Prop   struct {
				Length       string `xml:"getcontentlength"`
				Modified     string `xml:"getlastmodified"`
				ResourceType struct {
					Collection *struct{} `xml:"collection"`
				} `xml:"resourcetype"`
			} `xml:"prop"`
		} `xml:"propstat"`
	} `xml:"response"`
}

const propfindBody = `<?xml version="1.0" encoding="utf-8"?><d:propfind xmlns:d="DAV:"><d:prop><d:getcontentlength/><d:getlastmodified/><d:resourcetype/></d:prop></d:propfind>`

// List returns the backups in the directory, newest first. A missing
// directory has none.
func (w WebDAV) List(ctx context.Context) ([]RemoteFile, error) {
	response, err := w.request(ctx, "PROPFIND", w.collectionURL(len(w.segments())), strings.NewReader(propfindBody), int64(len(propfindBody)),
		http.Header{"Depth": {"1"}, "Content-Type": {"application/xml; charset=utf-8"}})
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode == http.StatusNotFound {
		return []RemoteFile{}, nil
	}
	if response.StatusCode != http.StatusMultiStatus && response.StatusCode != http.StatusOK {
		return nil, statusError("读取备份列表", response)
	}
	var status multistatus
	if err := xml.NewDecoder(io.LimitReader(response.Body, 16<<20)).Decode(&status); err != nil {
		return nil, fmt.Errorf("读取备份列表失败：服务器返回的不是 WebDAV 目录列表（%v）", err)
	}
	files := []RemoteFile{}
	for _, item := range status.Responses {
		href, err := url.PathUnescape(item.Href)
		if err != nil {
			href = item.Href
		}
		name := path.Base(strings.TrimRight(href, "/"))
		if !ValidName(name) {
			continue
		}
		file := RemoteFile{Name: name}
		for _, propstat := range item.Propstats {
			if propstat.Prop.ResourceType.Collection != nil {
				file.Name = ""
			}
			if propstat.Prop.Length != "" {
				file.SizeBytes, _ = strconv.ParseInt(strings.TrimSpace(propstat.Prop.Length), 10, 64)
			}
			if propstat.Prop.Modified != "" {
				file.ModifiedAt, _ = http.ParseTime(strings.TrimSpace(propstat.Prop.Modified))
			}
		}
		if file.Name != "" {
			files = append(files, file)
		}
	}
	sort.Slice(files, func(i, j int) bool { return files[i].Name > files[j].Name })
	return files, nil
}

// Download saves a backup from the directory to target.
func (w WebDAV) Download(ctx context.Context, name, target string) error {
	response, err := w.request(ctx, http.MethodGet, w.fileURL(name), nil, 0, nil)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return statusError("下载", response)
	}
	file, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	if _, err = io.Copy(file, response.Body); err != nil {
		file.Close()
		return fmt.Errorf("下载中断：%w", err)
	}
	return file.Close()
}

// Delete removes a backup; one already gone is not an error.
func (w WebDAV) Delete(ctx context.Context, name string) error {
	response, err := w.request(ctx, http.MethodDelete, w.fileURL(name), nil, 0, nil)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode >= 300 && response.StatusCode != http.StatusNotFound {
		return statusError("删除", response)
	}
	return nil
}

// Test checks the whole round trip a backup needs: create the directory,
// write, list and delete a small file.
func (w WebDAV) Test(ctx context.Context) (string, error) {
	if !w.Configured() {
		return "", errors.New("请先填写 WebDAV 地址")
	}
	started := time.Now()
	if err := w.ensureDirectory(ctx); err != nil {
		return "", err
	}
	suffix := make([]byte, 4)
	_, _ = rand.Read(suffix)
	probe := ".vpsbill-test-" + hex.EncodeToString(suffix) + ".txt"
	body := "VPSBill WebDAV connectivity test; safe to delete.\n"
	response, err := w.request(ctx, http.MethodPut, w.fileURL(probe), strings.NewReader(body), int64(len(body)), http.Header{"Content-Type": {"text/plain"}})
	if err != nil {
		return "", err
	}
	response.Body.Close()
	if response.StatusCode >= 300 {
		return "", statusError("写入测试文件", response)
	}
	files, listErr := w.List(ctx)
	deleteErr := w.Delete(ctx, probe)
	if listErr != nil {
		return "", listErr
	}
	if deleteErr != nil {
		return "", fmt.Errorf("测试文件已写入，但%w", deleteErr)
	}
	return fmt.Sprintf("连接正常：目录 /%s 可读写，已有 %d 份备份，往返用时 %d 毫秒。", strings.Join(w.segments(), "/"), len(files), time.Since(started).Milliseconds()), nil
}
