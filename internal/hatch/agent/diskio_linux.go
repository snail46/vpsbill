//go:build linux

package agent

import (
	"crypto/rand"
	"errors"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
	"unsafe"

	"vpsbill/internal/hatch/protocol"
)

// blockDevices lists the whole disks an instance's I/O may reach, as
// "major:minor". Limits go on every disk: the cgroup io controller only
// takes whole disks, and instance storage may sit on any of them (a loop
// file, LVM, a second disk). Stacked devices each carry the limit, which
// still caps the instance at the one rate.
func blockDevices() []string {
	entries, err := os.ReadDir("/sys/block")
	if err != nil {
		return nil
	}
	var devices []string
	for _, entry := range entries {
		name := entry.Name()
		if strings.HasPrefix(name, "ram") || strings.HasPrefix(name, "zram") || strings.HasPrefix(name, "sr") || strings.HasPrefix(name, "fd") {
			continue
		}
		size, err := os.ReadFile(filepath.Join("/sys/block", name, "size"))
		if err != nil || strings.TrimSpace(string(size)) == "0" {
			continue
		}
		if dev, err := os.ReadFile(filepath.Join("/sys/block", name, "dev")); err == nil {
			devices = append(devices, strings.TrimSpace(string(dev)))
		}
	}
	return devices
}

const (
	benchFileSize = 128 << 20
	benchSeqTime  = 3 * time.Second
	benchRandTime = 2 * time.Second
	benchDepth    = 8
	benchAlign    = 4096
)

// measureDisk benchmarks the file system holding dir with O_DIRECT and
// random data (so compressing or deduplicating storage cannot flatter it):
// about ten seconds and a 128 MiB scratch file.
func measureDisk(dir string) (*protocol.DiskPerf, error) {
	// Agents sharing a machine take turns, or each would measure a third.
	// (/dev stays writable under the unit's ProtectSystem=strict.)
	if lock, err := os.OpenFile("/dev/shm/hatch-disk-bench.lock", os.O_CREATE|os.O_RDWR, 0o600); err == nil {
		defer lock.Close()
		_ = syscall.Flock(int(lock.Fd()), syscall.LOCK_EX)
	}
	path := filepath.Join(dir, ".disk-bench")
	file, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE|os.O_TRUNC|syscall.O_DIRECT, 0o600)
	if err != nil {
		return nil, err
	}
	defer os.Remove(path)
	defer file.Close()

	block := alignedBuffer(1 << 20)
	if _, err := rand.Read(block); err != nil {
		return nil, err
	}
	perf := &protocol.DiskPerf{MeasuredAt: time.Now().UTC()}
	var written int64
	start := time.Now()
	for written < benchFileSize && time.Since(start) < benchSeqTime {
		n, err := file.WriteAt(block, written)
		if err != nil {
			return nil, err
		}
		written += int64(n)
	}
	if err := file.Sync(); err != nil {
		return nil, err
	}
	perf.WriteMBps = mbps(written, time.Since(start))
	if written < 1<<20 {
		return nil, errors.New("disk too slow to measure")
	}

	var read int64
	start = time.Now()
	for read < written && time.Since(start) < benchSeqTime {
		n, err := file.ReadAt(block, read)
		if err != nil {
			return nil, err
		}
		read += int64(n)
	}
	perf.ReadMBps = mbps(read, time.Since(start))

	blocks := written / benchAlign
	perf.ReadIOPS, err = randomIOPS(file, blocks, false)
	if err != nil {
		return nil, err
	}
	perf.WriteIOPS, err = randomIOPS(file, blocks, true)
	if err != nil {
		return nil, err
	}
	return perf, file.Sync()
}

// randomIOPS runs benchDepth workers doing 4 KiB reads or writes at random
// aligned offsets.
func randomIOPS(file *os.File, blocks int64, write bool) (int, error) {
	var ops atomic.Int64
	var failure atomic.Value
	deadline := time.Now().Add(benchRandTime)
	var wg sync.WaitGroup
	for range benchDepth {
		wg.Add(1)
		go func() {
			defer wg.Done()
			buffer := alignedBuffer(benchAlign)
			_, _ = rand.Read(buffer)
			limit := big.NewInt(blocks)
			for time.Now().Before(deadline) {
				index, err := rand.Int(rand.Reader, limit)
				if err != nil {
					failure.Store(err)
					return
				}
				offset := index.Int64() * benchAlign
				if write {
					_, err = file.WriteAt(buffer, offset)
				} else {
					_, err = file.ReadAt(buffer, offset)
				}
				if err != nil {
					failure.Store(err)
					return
				}
				ops.Add(1)
			}
		}()
	}
	wg.Wait()
	if err, ok := failure.Load().(error); ok {
		return 0, err
	}
	return int(ops.Load() * int64(time.Second) / int64(benchRandTime)), nil
}

// alignedBuffer returns size bytes starting on a benchAlign boundary, as
// O_DIRECT requires.
func alignedBuffer(size int) []byte {
	raw := make([]byte, size+benchAlign)
	offset := 0
	if rem := int(uintptr(unsafe.Pointer(&raw[0])) % benchAlign); rem != 0 {
		offset = benchAlign - rem
	}
	return raw[offset : offset+size]
}

func mbps(bytes int64, elapsed time.Duration) int {
	if elapsed <= 0 {
		return 0
	}
	return int(float64(bytes) / float64(1<<20) / elapsed.Seconds())
}
