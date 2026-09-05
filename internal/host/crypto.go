package host

import (
	"crypto/md5"
	"crypto/rand"
	"crypto/sha1"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"hash"
	"strings"

	"github.com/nooga/paserati/pkg/driver"
	"github.com/nooga/paserati/pkg/vm"
)

type hashHasher struct {
	h hash.Hash
}

func (h *hashHasher) Update(data string) *hashHasher {
	_, _ = h.h.Write([]byte(data))
	return h
}

func (h *hashHasher) Digest(encoding string) string {
	sum := h.h.Sum(nil)
	switch strings.ToLower(encoding) {
	case "hex":
		return hex.EncodeToString(sum)
	case "base64":
		return base64.StdEncoding.EncodeToString(sum)
	default:
		return string(sum)
	}
}

func declareCrypto(p *driver.Paserati) {
	vmInst := p.GetVM()
	p.DeclareModule("crypto", func(m *driver.ModuleBuilder) {
		m.Function("randomUUID", func() string {
			b := make([]byte, 16)
			_, _ = rand.Read(b)
			b[6] = (b[6] & 0x0f) | 0x40
			b[8] = (b[8] & 0x3f) | 0x80
			return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x",
				b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
		})
		m.Function("randomBytes", func(n float64) (vm.Value, error) {
			size := int(n)
			if size < 0 {
				size = 0
			}
			b := make([]byte, size)
			if _, err := rand.Read(b); err != nil {
				return vm.Undefined, err
			}
			return wrapBuffer(vmInst, string(b)), nil
		})
		m.Function("createHash", func(algo string) (*hashHasher, error) {
			switch strings.ToLower(algo) {
			case "md5":
				// Found via jiti's own filesystem cache-key hashing
				// (getCache -> utils_hash) - a completely ordinary
				// non-cryptographic use; md5 is still real Node's
				// default fast hash for this kind of thing.
				return &hashHasher{h: md5.New()}, nil
			case "sha1":
				return &hashHasher{h: sha1.New()}, nil
			case "sha256":
				return &hashHasher{h: sha256.New()}, nil
			case "sha384":
				return &hashHasher{h: sha512.New384()}, nil
			case "sha512":
				return &hashHasher{h: sha512.New()}, nil
			default:
				return nil, fmt.Errorf("Digest algorithm %q is not supported", algo)
			}
		})
		m.Default(nil)
	})
	_ = p.DeclareModuleAlias("node:crypto", "crypto")
}
