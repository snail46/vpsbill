package backup

import (
	"archive/tar"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"regexp"
	"strings"
	"time"
)

// A backup file is a tar holding manifest.json first, then database.dump
// (pg_dump's custom format, compressed).
const (
	manifestEntry = "manifest.json"
	dumpEntry     = "database.dump"
	formatVersion = 1
)

// Manifest describes a backup file.
type Manifest struct {
	Format    int       `json:"format"`
	CreatedAt time.Time `json:"created_at"`
	Trigger   string    `json:"trigger"`
	// AppVersion is the build that made it; SchemaVersion its newest
	// database migration, so a newer backup is not restored into older code.
	AppVersion      string `json:"app_version"`
	SchemaVersion   string `json:"schema_version"`
	PostgresVersion string `json:"postgres_version"`
	// KeyFingerprint identifies the ENCRYPTION_KEY the stored secrets
	// (payment keys, node tokens, SMTP password) are sealed with.
	KeyFingerprint string `json:"key_fingerprint"`
	DumpSize       int64  `json:"dump_size"`
	DumpSHA256     string `json:"dump_sha256"`
}

var namePattern = regexp.MustCompile(`^vpsbill-[0-9]{8}-[0-9]{6}(-[a-z][a-z-]{0,19})?\.tar$`)

// ValidName reports whether name is a backup file name this service made;
// only such names are read, served or deleted.
func ValidName(name string) bool { return namePattern.MatchString(name) }

// fileName names a backup by its time (UTC+8) and trigger.
func fileName(at time.Time, trigger string) string {
	return "vpsbill-" + at.Format("20060102-150405") + "-" + trigger + ".tar"
}

// rotating reports whether retention may delete the file: scheduled and
// manual backups rotate; pre-restore and uploaded ones stay until deleted.
func rotating(name string) bool {
	return strings.HasSuffix(name, "-scheduled.tar") || strings.HasSuffix(name, "-manual.tar")
}

// writeArchive packs the manifest and the dump into target.
func writeArchive(target string, manifest Manifest, dumpPath string) error {
	dump, err := os.Open(dumpPath)
	if err != nil {
		return err
	}
	defer dump.Close()
	info, err := dump.Stat()
	if err != nil {
		return err
	}
	body, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return err
	}
	file, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	writer := tar.NewWriter(file)
	fail := func(err error) error {
		file.Close()
		os.Remove(target)
		return err
	}
	if err := writer.WriteHeader(&tar.Header{Name: manifestEntry, Mode: 0o600, Size: int64(len(body)), ModTime: manifest.CreatedAt}); err != nil {
		return fail(err)
	}
	if _, err := writer.Write(body); err != nil {
		return fail(err)
	}
	if err := writer.WriteHeader(&tar.Header{Name: dumpEntry, Mode: 0o600, Size: info.Size(), ModTime: manifest.CreatedAt}); err != nil {
		return fail(err)
	}
	if _, err := io.Copy(writer, dump); err != nil {
		return fail(err)
	}
	if err := writer.Close(); err != nil {
		return fail(err)
	}
	if err := file.Sync(); err != nil {
		return fail(err)
	}
	return file.Close()
}

var errNotBackup = errors.New("这不是本系统生成的备份文件")

// readManifest reads a backup's manifest without touching the dump.
func readManifest(path string) (Manifest, error) {
	file, err := os.Open(path)
	if err != nil {
		return Manifest{}, err
	}
	defer file.Close()
	manifest, _, err := openArchive(file)
	return manifest, err
}

func openArchive(file io.Reader) (Manifest, *tar.Reader, error) {
	reader := tar.NewReader(file)
	header, err := reader.Next()
	if err != nil || header.Name != manifestEntry || header.Size > 64<<10 {
		return Manifest{}, nil, errNotBackup
	}
	var manifest Manifest
	if err := json.NewDecoder(io.LimitReader(reader, 64<<10)).Decode(&manifest); err != nil || manifest.Format == 0 {
		return Manifest{}, nil, errNotBackup
	}
	if manifest.Format > formatVersion {
		return Manifest{}, nil, fmt.Errorf("备份文件格式 %d 比本版本支持的更新，请先升级系统", manifest.Format)
	}
	return manifest, reader, nil
}

// extractDump writes the backup's dump to target (or only checks it when
// target is empty), verifying its size and checksum.
func extractDump(path, target string) (Manifest, error) {
	file, err := os.Open(path)
	if err != nil {
		return Manifest{}, err
	}
	defer file.Close()
	manifest, reader, err := openArchive(file)
	if err != nil {
		return Manifest{}, err
	}
	header, err := reader.Next()
	if err != nil || header.Name != dumpEntry {
		return Manifest{}, errNotBackup
	}
	var out io.Writer = io.Discard
	if target != "" {
		dump, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
		if err != nil {
			return Manifest{}, err
		}
		defer dump.Close()
		out = dump
	}
	hash := sha256.New()
	size, err := io.Copy(io.MultiWriter(out, hash), reader)
	if err != nil {
		return Manifest{}, fmt.Errorf("备份文件不完整：%w", err)
	}
	if size != manifest.DumpSize || hex.EncodeToString(hash.Sum(nil)) != manifest.DumpSHA256 {
		return Manifest{}, errors.New("备份文件校验失败：内容已损坏或不完整")
	}
	return manifest, nil
}

// hashFile returns a file's size and SHA-256.
func hashFile(path string) (int64, string, error) {
	file, err := os.Open(path)
	if err != nil {
		return 0, "", err
	}
	defer file.Close()
	hash := sha256.New()
	size, err := io.Copy(hash, file)
	return size, hex.EncodeToString(hash.Sum(nil)), err
}
