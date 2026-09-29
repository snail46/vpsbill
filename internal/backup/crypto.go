package backup

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"

	"golang.org/x/crypto/scrypt"
)

// Encryption describes how a backup's dump was encrypted with a passphrase:
// scrypt derives a key from it and a per-file salt, and the dump is sealed
// with AES-256-GCM in chunks, each [4-byte length][ciphertext]. A chunk's
// nonce is its index plus a flag on the last one, so chunks cannot be
// reordered, dropped or cut off without the decryption failing.
type Encryption struct {
	Algorithm string `json:"algorithm"`
	KDF       string `json:"kdf"`
	Salt      []byte `json:"salt"`
	N         int    `json:"n"`
	R         int    `json:"r"`
	P         int    `json:"p"`
	ChunkSize int    `json:"chunk_size"`
}

const (
	encryptionAlgorithm = "aes-256-gcm-chunked"
	chunkSize           = 1 << 20
	// MinPassphrase is the shortest passphrase accepted.
	MinPassphrase = 12
)

var (
	ErrPassphraseRequired = errors.New("passphrase required")
	ErrWrongPassphrase    = errors.New("wrong passphrase")
)

func newEncryption() (Encryption, error) {
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return Encryption{}, err
	}
	return Encryption{Algorithm: encryptionAlgorithm, KDF: "scrypt", Salt: salt, N: 1 << 15, R: 8, P: 1, ChunkSize: chunkSize}, nil
}

func (e Encryption) aead(passphrase string) (cipher.AEAD, error) {
	if e.Algorithm != encryptionAlgorithm || e.KDF != "scrypt" || len(e.Salt) < 16 || e.N < 1<<14 || e.N > 1<<20 || e.ChunkSize <= 0 || e.ChunkSize > 64<<20 {
		return nil, errors.New("备份的加密参数不受支持")
	}
	key, err := scrypt.Key([]byte(passphrase), e.Salt, e.N, e.R, e.P, 32)
	if err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

func chunkNonce(index uint64, last bool) []byte {
	nonce := make([]byte, 12)
	binary.BigEndian.PutUint64(nonce[3:11], index)
	if last {
		nonce[11] = 1
	}
	return nonce
}

// encryptFile seals source into target.
func encryptFile(source, target, passphrase string, e Encryption) error {
	aead, err := e.aead(passphrase)
	if err != nil {
		return err
	}
	in, err := os.Open(source)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	fail := func(err error) error {
		out.Close()
		os.Remove(target)
		return err
	}
	// Read one chunk ahead so the last chunk is known when sealed.
	current := make([]byte, e.ChunkSize)
	next := make([]byte, e.ChunkSize)
	n, err := io.ReadFull(in, current)
	if err != nil && !errors.Is(err, io.ErrUnexpectedEOF) && !errors.Is(err, io.EOF) {
		return fail(err)
	}
	var header [4]byte
	for index := uint64(0); ; index++ {
		m, readErr := io.ReadFull(in, next)
		if readErr != nil && !errors.Is(readErr, io.ErrUnexpectedEOF) && !errors.Is(readErr, io.EOF) {
			return fail(readErr)
		}
		last := m == 0
		sealed := aead.Seal(nil, chunkNonce(index, last), current[:n], nil)
		binary.BigEndian.PutUint32(header[:], uint32(len(sealed)))
		if _, err := out.Write(header[:]); err != nil {
			return fail(err)
		}
		if _, err := out.Write(sealed); err != nil {
			return fail(err)
		}
		if last {
			break
		}
		current, next = next, current
		n = m
	}
	if err := out.Sync(); err != nil {
		return fail(err)
	}
	return out.Close()
}

// decryptFile opens source into target (or only checks the first chunk
// when target is empty, to test a passphrase quickly).
func decryptFile(source, target, passphrase string, e Encryption) error {
	aead, err := e.aead(passphrase)
	if err != nil {
		return err
	}
	in, err := os.Open(source)
	if err != nil {
		return err
	}
	defer in.Close()
	var out *os.File
	if target != "" {
		if out, err = os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600); err != nil {
			return err
		}
		defer out.Close()
	}
	limit := e.ChunkSize + aead.Overhead()
	var header [4]byte
	for index := uint64(0); ; index++ {
		if _, err := io.ReadFull(in, header[:]); err != nil {
			return errors.New("加密的备份不完整：缺少结尾")
		}
		size := int(binary.BigEndian.Uint32(header[:]))
		if size < aead.Overhead() || size > limit {
			return errors.New("加密的备份已损坏")
		}
		sealed := make([]byte, size)
		if _, err := io.ReadFull(in, sealed); err != nil {
			return errors.New("加密的备份不完整")
		}
		// The last chunk carries the flag; try it only when no data follows.
		plain, err := aead.Open(nil, chunkNonce(index, false), sealed, nil)
		last := false
		if err != nil {
			if plain, err = aead.Open(nil, chunkNonce(index, true), sealed, nil); err != nil {
				if index == 0 {
					return ErrWrongPassphrase
				}
				return errors.New("加密的备份已损坏或被改动")
			}
			last = true
		}
		if out == nil {
			return nil
		}
		if _, err := out.Write(plain); err != nil {
			return err
		}
		if last {
			if extra, _ := in.Read(header[:1]); extra > 0 {
				return errors.New("加密的备份结尾后还有多余内容")
			}
			return out.Sync()
		}
	}
}

// passphraseError explains a failed passphrase to the administrator.
func passphraseError(err error) error {
	if errors.Is(err, ErrWrongPassphrase) {
		return fmt.Errorf("%w: 备份口令不正确", ErrWrongPassphrase)
	}
	return err
}
