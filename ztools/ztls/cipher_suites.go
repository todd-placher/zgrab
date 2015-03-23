// Copyright 2010 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package ztls

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/des"
	"crypto/hmac"
	"crypto/rc4"
	"crypto/sha1"
	"hash"

	"github.com/zmap/zgrab/ztools/x509"
)

// a keyAgreement implements the client and server side of a TLS key agreement
// protocol by generating and processing key exchange messages.
type keyAgreement interface {
	// On the server side, the first two methods are called in order.

	// In the case that the key agreement protocol doesn't use a
	// ServerKeyExchange message, generateServerKeyExchange can return nil,
	// nil.
	generateServerKeyExchange(*Config, *Certificate, *clientHelloMsg, *serverHelloMsg) (*serverKeyExchangeMsg, error)
	processClientKeyExchange(*Config, *Certificate, *clientKeyExchangeMsg, uint16) ([]byte, error)

	// On the client side, the next two methods are called in order.

	// This method may not be called if the server doesn't send a
	// ServerKeyExchange message.
	processServerKeyExchange(*Config, *clientHelloMsg, *serverHelloMsg, *x509.Certificate, *serverKeyExchangeMsg) error
	generateClientKeyExchange(*Config, *clientHelloMsg, *x509.Certificate, uint16) ([]byte, *clientKeyExchangeMsg, error)
}

const (
	// suiteECDH indicates that the cipher suite involves elliptic curve
	// Diffie-Hellman. This means that it should only be selected when the
	// client indicates that it supports ECC with a curve and point format
	// that we're happy with.
	suiteECDHE = 1 << iota
	// suiteECDSA indicates that the cipher suite involves an ECDSA
	// signature and therefore may only be selected when the server's
	// certificate is ECDSA. If this is not set then the cipher suite is
	// RSA based.
	suiteECDSA
	// suiteTLS12 indicates that the cipher suite should only be advertised
	// and accepted when using TLS 1.2.
	suiteTLS12
)

// A cipherSuite is a specific combination of key agreement, cipher and MAC
// function. All cipher suites currently assume RSA key agreement.
type cipherSuite struct {
	id uint16
	// the lengths, in bytes, of the key material needed for each component.
	keyLen int
	macLen int
	ivLen  int
	ka     func(version uint16) keyAgreement
	// flags is a bitmask of the suite* values, above.
	flags  int
	cipher func(key, iv []byte, isRead bool) interface{}
	mac    func(version uint16, macKey []byte) macFunction
	aead   func(key, fixedNonce []byte) cipher.AEAD
}

var cipherSuites = []*cipherSuite{
	// Ciphersuite order is chosen so that ECDHE comes before plain RSA
	// and RC4 comes before AES (because of the Lucky13 attack).
	{TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256, 16, 0, 4, ecdheRSAKA, suiteECDHE | suiteTLS12, nil, nil, aeadAESGCM},
	{TLS_ECDHE_ECDSA_WITH_AES_128_GCM_SHA256, 16, 0, 4, ecdheECDSAKA, suiteECDHE | suiteECDSA | suiteTLS12, nil, nil, aeadAESGCM},
	{TLS_ECDHE_RSA_WITH_RC4_128_SHA, 16, 20, 0, ecdheRSAKA, suiteECDHE, cipherRC4, macSHA1, nil},
	{TLS_ECDHE_ECDSA_WITH_RC4_128_SHA, 16, 20, 0, ecdheECDSAKA, suiteECDHE | suiteECDSA, cipherRC4, macSHA1, nil},
	{TLS_ECDHE_RSA_WITH_AES_128_CBC_SHA, 16, 20, 16, ecdheRSAKA, suiteECDHE, cipherAES, macSHA1, nil},
	{TLS_ECDHE_ECDSA_WITH_AES_128_CBC_SHA, 16, 20, 16, ecdheECDSAKA, suiteECDHE | suiteECDSA, cipherAES, macSHA1, nil},
	{TLS_ECDHE_RSA_WITH_AES_256_CBC_SHA, 32, 20, 16, ecdheRSAKA, suiteECDHE, cipherAES, macSHA1, nil},
	{TLS_ECDHE_ECDSA_WITH_AES_256_CBC_SHA, 32, 20, 16, ecdheECDSAKA, suiteECDHE | suiteECDSA, cipherAES, macSHA1, nil},
	{TLS_RSA_WITH_RC4_128_SHA, 16, 20, 0, rsaKA, 0, cipherRC4, macSHA1, nil},
	{TLS_RSA_WITH_AES_128_GCM_SHA256, 16, 0, 4, rsaKA, 0, nil, nil, aeadAESGCM},
	{TLS_RSA_WITH_AES_128_CBC_SHA, 16, 20, 16, rsaKA, 0, cipherAES, macSHA1, nil},
	{TLS_RSA_WITH_AES_256_CBC_SHA, 32, 20, 16, rsaKA, 0, cipherAES, macSHA1, nil},
	{TLS_ECDHE_RSA_WITH_3DES_EDE_CBC_SHA, 24, 20, 8, ecdheRSAKA, suiteECDHE, cipher3DES, macSHA1, nil},
	{TLS_RSA_WITH_3DES_EDE_CBC_SHA, 24, 20, 8, rsaKA, 0, cipher3DES, macSHA1, nil},
}

func cipherRC4(key, iv []byte, isRead bool) interface{} {
	cipher, _ := rc4.NewCipher(key)
	return cipher
}

func cipher3DES(key, iv []byte, isRead bool) interface{} {
	block, _ := des.NewTripleDESCipher(key)
	if isRead {
		return cipher.NewCBCDecrypter(block, iv)
	}
	return cipher.NewCBCEncrypter(block, iv)
}

func cipherAES(key, iv []byte, isRead bool) interface{} {
	block, _ := aes.NewCipher(key)
	if isRead {
		return cipher.NewCBCDecrypter(block, iv)
	}
	return cipher.NewCBCEncrypter(block, iv)
}

// macSHA1 returns a macFunction for the given protocol version.
func macSHA1(version uint16, key []byte) macFunction {
	if version == VersionSSL30 {
		mac := ssl30MAC{
			h:   sha1.New(),
			key: make([]byte, len(key)),
		}
		copy(mac.key, key)
		return mac
	}
	return tls10MAC{hmac.New(sha1.New, key)}
}

type macFunction interface {
	Size() int
	MAC(digestBuf, seq, header, data []byte) []byte
}

// fixedNonceAEAD wraps an AEAD and prefixes a fixed portion of the nonce to
// each call.
type fixedNonceAEAD struct {
	// sealNonce and openNonce are buffers where the larger nonce will be
	// constructed. Since a seal and open operation may be running
	// concurrently, there is a separate buffer for each.
	sealNonce, openNonce []byte
	aead                 cipher.AEAD
}

func (f *fixedNonceAEAD) NonceSize() int { return 8 }
func (f *fixedNonceAEAD) Overhead() int  { return f.aead.Overhead() }

func (f *fixedNonceAEAD) Seal(out, nonce, plaintext, additionalData []byte) []byte {
	copy(f.sealNonce[len(f.sealNonce)-8:], nonce)
	return f.aead.Seal(out, f.sealNonce, plaintext, additionalData)
}

func (f *fixedNonceAEAD) Open(out, nonce, plaintext, additionalData []byte) ([]byte, error) {
	copy(f.openNonce[len(f.openNonce)-8:], nonce)
	return f.aead.Open(out, f.openNonce, plaintext, additionalData)
}

func aeadAESGCM(key, fixedNonce []byte) cipher.AEAD {
	aes, err := aes.NewCipher(key)
	if err != nil {
		panic(err)
	}
	aead, err := cipher.NewGCM(aes)
	if err != nil {
		panic(err)
	}

	nonce1, nonce2 := make([]byte, 12), make([]byte, 12)
	copy(nonce1, fixedNonce)
	copy(nonce2, fixedNonce)

	return &fixedNonceAEAD{nonce1, nonce2, aead}
}

// ssl30MAC implements the SSLv3 MAC function, as defined in
// www.mozilla.org/projects/security/pki/nss/ssl/draft302.txt section 5.2.3.1
type ssl30MAC struct {
	h   hash.Hash
	key []byte
}

func (s ssl30MAC) Size() int {
	return s.h.Size()
}

var ssl30Pad1 = [48]byte{0x36, 0x36, 0x36, 0x36, 0x36, 0x36, 0x36, 0x36, 0x36, 0x36, 0x36, 0x36, 0x36, 0x36, 0x36, 0x36, 0x36, 0x36, 0x36, 0x36, 0x36, 0x36, 0x36, 0x36, 0x36, 0x36, 0x36, 0x36, 0x36, 0x36, 0x36, 0x36, 0x36, 0x36, 0x36, 0x36, 0x36, 0x36, 0x36, 0x36, 0x36, 0x36, 0x36, 0x36, 0x36, 0x36, 0x36, 0x36}

var ssl30Pad2 = [48]byte{0x5c, 0x5c, 0x5c, 0x5c, 0x5c, 0x5c, 0x5c, 0x5c, 0x5c, 0x5c, 0x5c, 0x5c, 0x5c, 0x5c, 0x5c, 0x5c, 0x5c, 0x5c, 0x5c, 0x5c, 0x5c, 0x5c, 0x5c, 0x5c, 0x5c, 0x5c, 0x5c, 0x5c, 0x5c, 0x5c, 0x5c, 0x5c, 0x5c, 0x5c, 0x5c, 0x5c, 0x5c, 0x5c, 0x5c, 0x5c, 0x5c, 0x5c, 0x5c, 0x5c, 0x5c, 0x5c, 0x5c, 0x5c}

func (s ssl30MAC) MAC(digestBuf, seq, header, data []byte) []byte {
	padLength := 48
	if s.h.Size() == 20 {
		padLength = 40
	}

	s.h.Reset()
	s.h.Write(s.key)
	s.h.Write(ssl30Pad1[:padLength])
	s.h.Write(seq)
	s.h.Write(header[:1])
	s.h.Write(header[3:5])
	s.h.Write(data)
	digestBuf = s.h.Sum(digestBuf[:0])

	s.h.Reset()
	s.h.Write(s.key)
	s.h.Write(ssl30Pad2[:padLength])
	s.h.Write(digestBuf)
	return s.h.Sum(digestBuf[:0])
}

// tls10MAC implements the TLS 1.0 MAC function. RFC 2246, section 6.2.3.
type tls10MAC struct {
	h hash.Hash
}

func (s tls10MAC) Size() int {
	return s.h.Size()
}

func (s tls10MAC) MAC(digestBuf, seq, header, data []byte) []byte {
	s.h.Reset()
	s.h.Write(seq)
	s.h.Write(header)
	s.h.Write(data)
	return s.h.Sum(digestBuf[:0])
}

func rsaExportKA(version uint16) keyAgreement {
	return &rsaExportKeyAgreement{
		sigType: signatureRSA,
		version: version,
	}
}

func rsaKA(version uint16) keyAgreement {
	return rsaKeyAgreement{}
}

func ecdheECDSAKA(version uint16) keyAgreement {
	return &ecdheKeyAgreement{
		sigType: signatureECDSA,
		version: version,
	}
}

func ecdheRSAKA(version uint16) keyAgreement {
	return &ecdheKeyAgreement{
		sigType: signatureRSA,
		version: version,
	}
}

// mutualCipherSuite returns a cipherSuite given a list of supported
// ciphersuites and the id requested by the peer.
func mutualCipherSuite(have []uint16, want uint16) *cipherSuite {
	for _, id := range have {
		if id == want {
			for _, suite := range cipherSuites {
				if suite.id == want {
					return suite
				}
			}
			return nil
		}
	}
	return nil
}

// A list of the possible cipher suite ids. Taken from
// http://www.iana.org/assignments/tls-parameters/tls-parameters.xml
const (
	TLS_RSA_WITH_RC4_128_SHA                uint16 = 0x0005
	TLS_RSA_WITH_3DES_EDE_CBC_SHA           uint16 = 0x000a
	TLS_RSA_WITH_AES_128_CBC_SHA            uint16 = 0x002f
	TLS_RSA_WITH_AES_256_CBC_SHA            uint16 = 0x0035
	TLS_RSA_WITH_AES_128_GCM_SHA256         uint16 = 0x009C
	TLS_ECDHE_ECDSA_WITH_RC4_128_SHA        uint16 = 0xc007
	TLS_ECDHE_ECDSA_WITH_AES_128_CBC_SHA    uint16 = 0xc009
	TLS_ECDHE_ECDSA_WITH_AES_256_CBC_SHA    uint16 = 0xc00a
	TLS_ECDHE_RSA_WITH_RC4_128_SHA          uint16 = 0xc011
	TLS_ECDHE_RSA_WITH_3DES_EDE_CBC_SHA     uint16 = 0xc012
	TLS_ECDHE_RSA_WITH_AES_128_CBC_SHA      uint16 = 0xc013
	TLS_ECDHE_RSA_WITH_AES_256_CBC_SHA      uint16 = 0xc014
	TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256   uint16 = 0xc02f
	TLS_ECDHE_ECDSA_WITH_AES_128_GCM_SHA256 uint16 = 0xc02b
	TLS_RSA_WITH_RC4_128_MD5                uint16 = 0x0004
)

// RSA Ciphers
var RSACiphers = []uint16{
	TLS_RSA_WITH_RC4_128_SHA,
	TLS_RSA_WITH_3DES_EDE_CBC_SHA,
	TLS_RSA_WITH_AES_128_CBC_SHA,
	TLS_RSA_WITH_AES_256_CBC_SHA,
	TLS_RSA_WITH_AES_128_GCM_SHA256,
}

//  Export ciphers
const (
	TLS_RSA_EXPORT_WITH_RC4_40_MD5          = 0x0003
	TLS_RSA_EXPORT_WITH_RC2_CBC_40_MD5      = 0x0006
	TLS_RSA_EXPORT_WITH_DES40_CBC_SHA       = 0x0008
	TLS_DH_DSS_EXPORT_WITH_DES40_CBC_SHA    = 0x000B
	TLS_DH_RSA_EXPORT_WITH_DES40_CBC_SHA    = 0x000E
	TLS_DHE_DSS_EXPORT_WITH_DES40_CBC_SHA   = 0x0011
	TLS_DHE_RSA_EXPORT_WITH_DES40_CBC_SHA   = 0x0014
	TLS_DH_anon_EXPORT_WITH_RC4_40_MD5      = 0x0017
	TLS_DH_anon_EXPORT_WITH_DES40_CBC_SHA   = 0x0019
	TLS_KRB5_EXPORT_WITH_DES_CBC_40_SHA     = 0x0026
	TLS_KRB5_EXPORT_WITH_RC2_CBC_40_SHA     = 0x0027
	TLS_KRB5_EXPORT_WITH_RC4_40_SHA         = 0x0028
	TLS_KRB5_EXPORT_WITH_DES_CBC_40_MD5     = 0x0029
	TLS_KRB5_EXPORT_WITH_RC2_CBC_40_MD5     = 0x002A
	TLS_KRB5_EXPORT_WITH_RC4_40_MD5         = 0x002B
	TLS_RSA_EXPORT1024_WITH_RC4_56_MD5      = 0x0060
	TLS_RSA_EXPORT1024_WITH_RC2_CBC_56_MD5  = 0x0061
	TLS_RSA_EXPORT1024_WITH_DES_CBC_SHA     = 0x0062
	TLS_DHE_DSS_EXPORT1024_WITH_DES_CBC_SHA = 0x0063
	TLS_RSA_EXPORT1024_WITH_RC4_56_SHA      = 0x0064
	TLS_DHE_DSS_EXPORT1024_WITH_RC4_56_SHA  = 0x0065
)

// DHE ciphers
const (
	TLS_DHE_DSS_WITH_DES_CBC_SHA              = 0x0012
	TLS_DHE_DSS_WITH_3DES_EDE_CBC_SHA         = 0x0013
	TLS_DHE_RSA_WITH_DES_CBC_SHA              = 0x0015
	TLS_DHE_RSA_WITH_3DES_EDE_CBC_SHA         = 0x0016
	TLS_DHE_DSS_WITH_AES_128_CBC_SHA          = 0x0032
	TLS_DHE_RSA_WITH_AES_128_CBC_SHA          = 0x0033
	TLS_DHE_DSS_WITH_AES_256_CBC_SHA          = 0x0038
	TLS_DHE_RSA_WITH_AES_256_CBC_SHA          = 0x0039
	TLS_DHE_DSS_WITH_AES_128_CBC_SHA256       = 0x0040
	TLS_DHE_DSS_WITH_CAMELLIA_128_CBC_SHA     = 0x0044
	TLS_DHE_RSA_WITH_CAMELLIA_128_CBC_SHA     = 0x0045
	TLS_DHE_DSS_WITH_RC4_128_SHA              = 0x0066
	TLS_DHE_RSA_WITH_AES_128_CBC_SHA256       = 0x0067
	TLS_DHE_DSS_WITH_AES_256_CBC_SHA256       = 0x006A
	TLS_DHE_RSA_WITH_AES_256_CBC_SHA256       = 0x006B
	TLS_DHE_DSS_WITH_CAMELLIA_256_CBC_SHA     = 0x0087
	TLS_DHE_RSA_WITH_CAMELLIA_256_CBC_SHA     = 0x0088
	TLS_DHE_DSS_WITH_SEED_CBC_SHA             = 0x0099
	TLS_DHE_RSA_WITH_SEED_CBC_SHA             = 0x009A
	TLS_DHE_RSA_WITH_AES_128_GCM_SHA256       = 0x009E
	TLS_DHE_RSA_WITH_AES_256_GCM_SHA384       = 0x009F
	TLS_DHE_DSS_WITH_AES_128_GCM_SHA256       = 0x00A2
	TLS_DHE_DSS_WITH_AES_256_GCM_SHA384       = 0x00A3
	TLS_DHE_DSS_WITH_CAMELLIA_128_CBC_SHA256  = 0x00BD
	TLS_DHE_RSA_WITH_CAMELLIA_128_CBC_SHA256  = 0x00BE
	TLS_DHE_DSS_WITH_CAMELLIA_256_CBC_SHA256  = 0x00C3
	TLS_DHE_RSA_WITH_CAMELLIA_256_CBC_SHA256  = 0x00C4
	TLS_DHE_DSS_WITH_ARIA_128_CBC_SHA256      = 0xC042
	TLS_DHE_DSS_WITH_ARIA_256_CBC_SHA384      = 0xC043
	TLS_DHE_RSA_WITH_ARIA_128_CBC_SHA256      = 0xC044
	TLS_DHE_RSA_WITH_ARIA_256_CBC_SHA384      = 0xC045
	TLS_DHE_RSA_WITH_ARIA_128_GCM_SHA256      = 0xC052
	TLS_DHE_RSA_WITH_ARIA_256_GCM_SHA384      = 0xC053
	TLS_DHE_DSS_WITH_ARIA_128_GCM_SHA256      = 0xC056
	TLS_DHE_DSS_WITH_ARIA_256_GCM_SHA384      = 0xC057
	TLS_DHE_RSA_WITH_CAMELLIA_128_GCM_SHA256  = 0xC07C
	TLS_DHE_RSA_WITH_CAMELLIA_256_GCM_SHA384  = 0xC07D
	TLS_DHE_DSS_WITH_CAMELLIA_128_GCM_SHA256  = 0xC080
	TLS_DHE_DSS_WITH_CAMELLIA_256_GCM_SHA384  = 0xC081
	TLS_DHE_RSA_WITH_AES_128_CCM              = 0xC09E
	TLS_DHE_RSA_WITH_AES_256_CCM              = 0xC09F
	TLS_DHE_RSA_WITH_AES_128_CCM_8            = 0xC0A2
	TLS_DHE_RSA_WITH_AES_256_CCM_8            = 0xC0A3
	TLS_DHE_RSA_WITH_CHACHA20_POLY1305_SHA256 = 0xCC15
)

// CHACHA ciphers
const (
	TLS_ECDHE_ECDSA_WITH_CHACHA20_POLY1305_SHA256 uint16 = 0xCC14
	TLS_ECDHE_RSA_WITH_CHACHA20_POLY1305_SHA256   uint16 = 0xCC13
)

var DHECiphers []uint16 = []uint16{
	TLS_DHE_DSS_WITH_DES_CBC_SHA,
	TLS_DHE_DSS_WITH_3DES_EDE_CBC_SHA,
	TLS_DHE_RSA_WITH_DES_CBC_SHA,
	TLS_DHE_RSA_WITH_3DES_EDE_CBC_SHA,
	TLS_DHE_DSS_WITH_AES_128_CBC_SHA,
	TLS_DHE_RSA_WITH_AES_128_CBC_SHA,
	TLS_DHE_DSS_WITH_AES_256_CBC_SHA,
	TLS_DHE_RSA_WITH_AES_256_CBC_SHA,
	TLS_DHE_DSS_WITH_AES_128_CBC_SHA256,
	TLS_DHE_DSS_WITH_CAMELLIA_128_CBC_SHA,
	TLS_DHE_RSA_WITH_CAMELLIA_128_CBC_SHA,
	TLS_DHE_DSS_WITH_RC4_128_SHA,
	TLS_DHE_RSA_WITH_AES_128_CBC_SHA256,
	TLS_DHE_DSS_WITH_AES_256_CBC_SHA256,
	TLS_DHE_RSA_WITH_AES_256_CBC_SHA256,
	TLS_DHE_DSS_WITH_CAMELLIA_256_CBC_SHA,
	TLS_DHE_RSA_WITH_CAMELLIA_256_CBC_SHA,
	TLS_DHE_DSS_WITH_SEED_CBC_SHA,
	TLS_DHE_RSA_WITH_SEED_CBC_SHA,
	TLS_DHE_RSA_WITH_AES_128_GCM_SHA256,
	TLS_DHE_RSA_WITH_AES_256_GCM_SHA384,
	TLS_DHE_DSS_WITH_AES_128_GCM_SHA256,
	TLS_DHE_DSS_WITH_AES_256_GCM_SHA384,
	TLS_DHE_DSS_WITH_CAMELLIA_128_CBC_SHA256,
	TLS_DHE_RSA_WITH_CAMELLIA_128_CBC_SHA256,
	TLS_DHE_DSS_WITH_CAMELLIA_256_CBC_SHA256,
	TLS_DHE_RSA_WITH_CAMELLIA_256_CBC_SHA256,
	TLS_DHE_DSS_WITH_ARIA_128_CBC_SHA256,
	TLS_DHE_DSS_WITH_ARIA_256_CBC_SHA384,
	TLS_DHE_RSA_WITH_ARIA_128_CBC_SHA256,
	TLS_DHE_RSA_WITH_ARIA_256_CBC_SHA384,
	TLS_DHE_RSA_WITH_ARIA_128_GCM_SHA256,
	TLS_DHE_RSA_WITH_ARIA_256_GCM_SHA384,
	TLS_DHE_DSS_WITH_ARIA_128_GCM_SHA256,
	TLS_DHE_DSS_WITH_ARIA_256_GCM_SHA384,
	TLS_DHE_RSA_WITH_CAMELLIA_128_GCM_SHA256,
	TLS_DHE_RSA_WITH_CAMELLIA_256_GCM_SHA384,
	TLS_DHE_DSS_WITH_CAMELLIA_128_GCM_SHA256,
	TLS_DHE_DSS_WITH_CAMELLIA_256_GCM_SHA384,
	TLS_DHE_RSA_WITH_AES_128_CCM,
	TLS_DHE_RSA_WITH_AES_256_CCM,
	TLS_DHE_RSA_WITH_AES_128_CCM_8,
	TLS_DHE_RSA_WITH_AES_256_CCM_8,
	TLS_DHE_RSA_WITH_CHACHA20_POLY1305_SHA256,
}

var ExportCiphers []uint16 = []uint16{
	TLS_RSA_EXPORT_WITH_RC4_40_MD5,
	TLS_RSA_EXPORT_WITH_RC2_CBC_40_MD5,
	TLS_RSA_EXPORT_WITH_DES40_CBC_SHA,
	TLS_DH_DSS_EXPORT_WITH_DES40_CBC_SHA,
	TLS_DH_RSA_EXPORT_WITH_DES40_CBC_SHA,
	TLS_DHE_DSS_EXPORT_WITH_DES40_CBC_SHA,
	TLS_DHE_RSA_EXPORT_WITH_DES40_CBC_SHA,
	TLS_DH_anon_EXPORT_WITH_RC4_40_MD5,
	TLS_DH_anon_EXPORT_WITH_DES40_CBC_SHA,
	TLS_KRB5_EXPORT_WITH_DES_CBC_40_SHA,
	TLS_KRB5_EXPORT_WITH_RC2_CBC_40_SHA,
	TLS_KRB5_EXPORT_WITH_RC4_40_SHA,
	TLS_KRB5_EXPORT_WITH_DES_CBC_40_MD5,
	TLS_KRB5_EXPORT_WITH_RC2_CBC_40_MD5,
	TLS_KRB5_EXPORT_WITH_RC4_40_MD5,
	TLS_RSA_EXPORT1024_WITH_RC4_56_MD5,
	TLS_RSA_EXPORT1024_WITH_RC2_CBC_56_MD5,
	TLS_RSA_EXPORT1024_WITH_DES_CBC_SHA,
	TLS_DHE_DSS_EXPORT1024_WITH_DES_CBC_SHA,
	TLS_RSA_EXPORT1024_WITH_RC4_56_SHA,
	TLS_DHE_DSS_EXPORT1024_WITH_RC4_56_SHA,
}

var RSAExportCiphers []uint16 = []uint16{
	TLS_RSA_EXPORT_WITH_RC4_40_MD5,
	TLS_RSA_EXPORT_WITH_RC2_CBC_40_MD5,
	TLS_RSA_EXPORT_WITH_DES40_CBC_SHA,
	TLS_RSA_EXPORT1024_WITH_RC4_56_MD5,
	TLS_RSA_EXPORT1024_WITH_RC2_CBC_56_MD5,
	TLS_RSA_EXPORT1024_WITH_DES_CBC_SHA,
	TLS_RSA_EXPORT1024_WITH_RC4_56_SHA,
}

var RSA512ExportCiphers []uint16 = []uint16{
	TLS_RSA_EXPORT_WITH_RC4_40_MD5,
	TLS_RSA_EXPORT_WITH_RC2_CBC_40_MD5,
	TLS_RSA_EXPORT_WITH_DES40_CBC_SHA,
}

var DHEExportCiphers []uint16 = []uint16{
	TLS_DHE_RSA_EXPORT_WITH_DES40_CBC_SHA,
	TLS_DH_anon_EXPORT_WITH_RC4_40_MD5,
	TLS_DH_anon_EXPORT_WITH_DES40_CBC_SHA,
	TLS_DHE_DSS_EXPORT_WITH_DES40_CBC_SHA,
}

var DHE512ExportCiphers []uint16 = []uint16{}

var CBCSuiteIDList []uint16 = []uint16{
	TLS_RSA_WITH_3DES_EDE_CBC_SHA,
	TLS_RSA_WITH_AES_128_CBC_SHA,
	TLS_RSA_WITH_AES_256_CBC_SHA,
	TLS_ECDHE_ECDSA_WITH_AES_128_CBC_SHA,
	TLS_ECDHE_ECDSA_WITH_AES_256_CBC_SHA,
	TLS_ECDHE_RSA_WITH_3DES_EDE_CBC_SHA,
	TLS_ECDHE_RSA_WITH_AES_128_CBC_SHA,
	TLS_ECDHE_RSA_WITH_AES_256_CBC_SHA,
}

var ChromeCiphers []uint16 = []uint16{
	TLS_ECDHE_ECDSA_WITH_AES_128_GCM_SHA256,
	TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256,
	TLS_DHE_RSA_WITH_AES_128_GCM_SHA256,
	TLS_ECDHE_ECDSA_WITH_CHACHA20_POLY1305_SHA256,
	TLS_ECDHE_RSA_WITH_CHACHA20_POLY1305_SHA256,
	TLS_DHE_RSA_WITH_CHACHA20_POLY1305_SHA256,
	TLS_ECDHE_ECDSA_WITH_AES_256_CBC_SHA,
	TLS_ECDHE_RSA_WITH_AES_256_CBC_SHA,
	TLS_DHE_RSA_WITH_AES_256_CBC_SHA,
	TLS_ECDHE_ECDSA_WITH_AES_128_CBC_SHA,
	TLS_ECDHE_RSA_WITH_AES_128_CBC_SHA,
	TLS_DHE_RSA_WITH_AES_128_CBC_SHA,
	TLS_ECDHE_ECDSA_WITH_RC4_128_SHA,
	TLS_ECDHE_RSA_WITH_RC4_128_SHA,
	TLS_RSA_WITH_AES_128_GCM_SHA256,
	TLS_RSA_WITH_AES_256_CBC_SHA,
	TLS_RSA_WITH_AES_128_CBC_SHA,
	TLS_RSA_WITH_RC4_128_SHA,
	TLS_RSA_WITH_RC4_128_MD5,
	TLS_RSA_WITH_3DES_EDE_CBC_SHA,
}

var ChromeNoDHECiphers []uint16 = []uint16{
	TLS_ECDHE_ECDSA_WITH_AES_128_GCM_SHA256,
	TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256,
	TLS_ECDHE_ECDSA_WITH_CHACHA20_POLY1305_SHA256,
	TLS_ECDHE_RSA_WITH_CHACHA20_POLY1305_SHA256,
	TLS_ECDHE_ECDSA_WITH_AES_256_CBC_SHA,
	TLS_ECDHE_RSA_WITH_AES_256_CBC_SHA,
	TLS_ECDHE_ECDSA_WITH_AES_128_CBC_SHA,
	TLS_ECDHE_RSA_WITH_AES_128_CBC_SHA,
	TLS_ECDHE_ECDSA_WITH_RC4_128_SHA,
	TLS_ECDHE_RSA_WITH_RC4_128_SHA,
	TLS_RSA_WITH_AES_128_GCM_SHA256,
	TLS_RSA_WITH_AES_256_CBC_SHA,
	TLS_RSA_WITH_AES_128_CBC_SHA,
	TLS_RSA_WITH_RC4_128_SHA,
	TLS_RSA_WITH_RC4_128_MD5,
	TLS_RSA_WITH_3DES_EDE_CBC_SHA,
}

var FirefoxCiphers []uint16 = []uint16{
	TLS_ECDHE_ECDSA_WITH_AES_128_GCM_SHA256,
	TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256,
	TLS_ECDHE_ECDSA_WITH_AES_256_CBC_SHA,
	TLS_ECDHE_ECDSA_WITH_AES_128_CBC_SHA,
	TLS_ECDHE_RSA_WITH_AES_128_CBC_SHA,
	TLS_ECDHE_RSA_WITH_AES_256_CBC_SHA,
	TLS_DHE_RSA_WITH_AES_128_CBC_SHA,
	TLS_DHE_DSS_WITH_AES_128_CBC_SHA,
	TLS_DHE_RSA_WITH_AES_256_CBC_SHA,
	TLS_RSA_WITH_AES_128_CBC_SHA,
	TLS_RSA_WITH_AES_256_CBC_SHA,
	TLS_RSA_WITH_3DES_EDE_CBC_SHA,
}

func cipherInList(cipher uint16, cipherList []uint16) bool {
	for _, val := range cipherList {
		if cipher == val {
			return true
		}
	}
	return false
}

var SChannelSuites []uint16 = []uint16{
	TLS_RSA_WITH_AES_128_GCM_SHA256,
	TLS_RSA_WITH_RC4_128_SHA,
}
