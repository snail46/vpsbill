package backup

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"vpsbill/internal/clock"
)

// fakeDAV is a minimal in-memory WebDAV server: collections must exist
// before files go into them, as on real servers.
type fakeDAV struct {
	mu    sync.Mutex
	dirs  map[string]bool
	files map[string][]byte
}

func newFakeDAV() *fakeDAV {
	return &fakeDAV{dirs: map[string]bool{"/dav/": true}, files: map[string][]byte{}}
}

func (f *fakeDAV) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if user, pass, ok := r.BasicAuth(); !ok || user != "alice" || pass != "secret" {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	path := r.URL.Path
	parent := path[:strings.LastIndex(strings.TrimSuffix(path, "/"), "/")+1]
	switch r.Method {
	case "MKCOL":
		if f.dirs[path] {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		if !f.dirs[parent] {
			w.WriteHeader(http.StatusConflict)
			return
		}
		f.dirs[path] = true
		w.WriteHeader(http.StatusCreated)
	case http.MethodPut:
		if !f.dirs[parent] {
			w.WriteHeader(http.StatusConflict)
			return
		}
		body, _ := io.ReadAll(r.Body)
		f.files[path] = body
		w.WriteHeader(http.StatusCreated)
	case http.MethodGet:
		body, ok := f.files[path]
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		_, _ = w.Write(body)
	case http.MethodDelete:
		delete(f.files, path)
		w.WriteHeader(http.StatusNoContent)
	case "PROPFIND":
		if !f.dirs[path] {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.WriteHeader(http.StatusMultiStatus)
		fmt.Fprintf(w, `<?xml version="1.0"?><d:multistatus xmlns:d="DAV:"><d:response><d:href>%s</d:href><d:propstat><d:prop><d:resourcetype><d:collection/></d:resourcetype></d:prop><d:status>HTTP/1.1 200 OK</d:status></d:propstat></d:response>`, path)
		for name, body := range f.files {
			if strings.HasPrefix(name, path) && !strings.Contains(name[len(path):], "/") {
				fmt.Fprintf(w, `<d:response><d:href>%s</d:href><d:propstat><d:prop><d:getcontentlength>%d</d:getcontentlength><d:getlastmodified>Tue, 29 Sep 2026 10:00:00 GMT</d:getlastmodified><d:resourcetype/></d:prop><d:status>HTTP/1.1 200 OK</d:status></d:propstat></d:response>`, name, len(body))
			}
		}
		fmt.Fprint(w, `</d:multistatus>`)
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func TestWebDAVRoundTrip(t *testing.T) {
	server := httptest.NewServer(newFakeDAV())
	defer server.Close()
	ctx := context.Background()
	target := WebDAV{URL: server.URL + "/dav", Username: "alice", Password: "secret", Directory: "backups/vpsbill"}

	message, err := target.Test(ctx)
	if err != nil || !strings.Contains(message, "已有 0 份备份") {
		t.Fatalf("test: %q %v", message, err)
	}
	source := filepath.Join(t.TempDir(), "source.tar")
	if err := os.WriteFile(source, []byte("backup body"), 0o600); err != nil {
		t.Fatal(err)
	}
	names := []string{"vpsbill-20260928-033000-scheduled.tar", "vpsbill-20260929-033000-scheduled.tar"}
	for _, name := range names {
		if err := target.Upload(ctx, name, source); err != nil {
			t.Fatal(err)
		}
	}
	files, err := target.List(ctx)
	if err != nil || len(files) != 2 || files[0].Name != names[1] || files[0].SizeBytes != 11 || files[0].ModifiedAt.IsZero() {
		t.Fatalf("list: %+v %v", files, err)
	}
	downloaded := filepath.Join(t.TempDir(), "down.tar")
	if err := target.Download(ctx, names[0], downloaded); err != nil {
		t.Fatal(err)
	}
	if body, _ := os.ReadFile(downloaded); string(body) != "backup body" {
		t.Fatalf("downloaded %q", body)
	}
	if err := target.Delete(ctx, names[0]); err != nil {
		t.Fatal(err)
	}
	if files, _ := target.List(ctx); len(files) != 1 {
		t.Fatalf("after delete: %+v", files)
	}

	target.Password = "wrong"
	if _, err := target.Test(ctx); err == nil || !strings.Contains(err.Error(), "用户名或密码错误") {
		t.Fatalf("wrong password: %v", err)
	}
}

func TestArchiveRoundTripAndCorruption(t *testing.T) {
	dir := t.TempDir()
	dump := filepath.Join(dir, "db.dump")
	if err := os.WriteFile(dump, []byte(strings.Repeat("PGDMP data ", 1000)), 0o600); err != nil {
		t.Fatal(err)
	}
	size, sum, err := hashFile(dump)
	if err != nil {
		t.Fatal(err)
	}
	manifest := Manifest{Format: formatVersion, CreatedAt: time.Now(), Trigger: "manual", SchemaVersion: "000034_backups.sql", KeyFingerprint: "abc", DumpSize: size, DumpSHA256: sum}
	archive := filepath.Join(dir, "vpsbill-20260929-120000-manual.tar")
	if err := writeArchive(archive, manifest, dump); err != nil {
		t.Fatal(err)
	}
	if got, err := readManifest(archive); err != nil || got.DumpSHA256 != sum || got.Trigger != "manual" {
		t.Fatalf("manifest: %+v %v", got, err)
	}
	restored := filepath.Join(dir, "restored.dump")
	if _, err := extractDump(archive, restored); err != nil {
		t.Fatal(err)
	}
	if original, restoredBody := mustRead(t, dump), mustRead(t, restored); original != restoredBody {
		t.Fatal("dump changed on the way")
	}

	// Flip one byte of the dump: the checksum must catch it.
	body := []byte(mustRead(t, archive))
	body[len(body)-2000] ^= 0xff
	if err := os.WriteFile(archive, body, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := extractDump(archive, ""); err == nil || !strings.Contains(err.Error(), "校验失败") {
		t.Fatalf("corruption not detected: %v", err)
	}
	if err := os.WriteFile(archive, []byte("not a tar"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := readManifest(archive); err != errNotBackup {
		t.Fatalf("foreign file: %v", err)
	}
}

func mustRead(t *testing.T, path string) string {
	t.Helper()
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(body)
}

func TestNamesAndRetention(t *testing.T) {
	at := time.Date(2026, 9, 29, 3, 30, 0, 0, clock.Zone)
	if name := fileName(at, "scheduled"); name != "vpsbill-20260929-033000-scheduled.tar" || !ValidName(name) {
		t.Fatalf("name %q", name)
	}
	for _, bad := range []string{"../etc/passwd", "vpsbill-20260929-033000.tar/..", "vpsbill-2026-scheduled.tar", ".tmp-vpsbill-20260929-033000-manual.tar"} {
		if ValidName(bad) {
			t.Fatalf("%q accepted", bad)
		}
	}
	names := []string{
		"vpsbill-20260925-033000-scheduled.tar", "vpsbill-20260926-033000-scheduled.tar",
		"vpsbill-20260927-120000-manual.tar", "vpsbill-20260928-090000-pre-restore.tar",
		"vpsbill-20260928-100000-upload.tar", "vpsbill-20260929-033000-scheduled.tar",
	}
	drop := keep(names, 2)
	sort.Strings(drop)
	if strings.Join(drop, ",") != "vpsbill-20260925-033000-scheduled.tar,vpsbill-20260926-033000-scheduled.tar" {
		t.Fatalf("drop %v", drop)
	}
}

func TestSchedule(t *testing.T) {
	now := time.Date(2026, 9, 29, 10, 0, 0, 0, clock.Zone)
	if slot := dailySlot(now, "03:30"); !slot.Equal(time.Date(2026, 9, 29, 3, 30, 0, 0, clock.Zone)) {
		t.Fatalf("slot %v", slot)
	}
	if slot := dailySlot(now, "23:00"); !slot.Equal(time.Date(2026, 9, 28, 23, 0, 0, 0, clock.Zone)) {
		t.Fatalf("slot before today's time %v", slot)
	}
	// Saved at 10:00: today's 03:30 has passed, so the next run is tomorrow.
	saved := now
	next := nextRun(Settings{Schedule: "daily", DailyAt: "03:30", ScheduledAt: &saved}, now)
	if !next.Equal(time.Date(2026, 9, 30, 3, 30, 0, 0, clock.Zone)) {
		t.Fatalf("next daily %v", next)
	}
	last := now.Add(-2 * time.Hour)
	next = nextRun(Settings{Schedule: "interval", IntervalHours: 6, ScheduledAt: &last}, now)
	if !next.Equal(now.Add(4 * time.Hour)) {
		t.Fatalf("next interval %v", next)
	}
	if nextRun(Settings{Schedule: "off"}, now) != nil {
		t.Fatal("off has a next run")
	}
}

func TestPgToolPrefersMatchingMajor(t *testing.T) {
	dir := t.TempDir()
	for _, major := range []string{"16", "17", "18"} {
		if err := os.MkdirAll(filepath.Join(dir, "postgresql"+major), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "postgresql"+major, "pg_dump"), nil, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	old := libexecGlob
	libexecGlob = dir + "/postgresql*/"
	defer func() { libexecGlob = old }()
	cases := map[int]string{16: "postgresql16", 17: "postgresql17", 15: "postgresql16", 19: ""}
	for major, want := range cases {
		got := pgTool("pg_dump", major)
		if want == "" && got != "pg_dump" || want != "" && filepath.Base(filepath.Dir(got)) != want {
			t.Fatalf("server %d: %s", major, got)
		}
	}
}

func TestFreeNameSkipsTakenNames(t *testing.T) {
	s := &Service{dir: t.TempDir()}
	at := time.Date(2026, 9, 29, 23, 18, 56, 0, clock.Zone)
	first := s.freeName(at, "upload")
	if err := os.WriteFile(filepath.Join(s.dir, first), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if second := s.freeName(at, "upload"); second == first || second != "vpsbill-20260929-231857-upload.tar" {
		t.Fatalf("second name %q after %q", second, first)
	}
}
