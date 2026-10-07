package config

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/subtle"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"log"
	"regexp"
	"strings"
	"time"

	"golang.org/x/crypto/hkdf"
)

var (
	tlds           = []string{"com", "net", "org", "io", "dev", "app"}
	hkdfInfo       = []byte("rw-v1")
	headerFooterRe = regexp.MustCompile(`(-----BEGIN [A-Z ]+-----)|(-----END [A-Z ]+-----)`)
	canonStripRe   = regexp.MustCompile(`-----[^-]+-----|[^A-Za-z0-9+/=]`)
)

type NodePayload struct {
	CACertPem    string `json:"caCertPem"`
	JWTPublicKey string `json:"jwtPublicKey"`
	NodeCertPem  string `json:"nodeCertPem"`
	NodeKeyPem   string `json:"nodeKeyPem"`
}

func NormalizePem(p string) string {
	normalized := strings.ReplaceAll(p, `\n`, "\n")
	normalized = strings.ReplaceAll(normalized, "\r\n", "\n")
	// normalize pem lines
	lines := strings.Split(normalized, "\n")
	var result []string
	for _, l := range lines {
		l = strings.TrimSpace(l)
		if l != "" {
			result = append(result, l)
		}
	}
	return strings.Join(result, "\n")
}

func canon(pemStr string) string {
	return canonStripRe.ReplaceAllString(pemStr, "")
}

// derive sni hostname
func DeriveSni(caCertPem, jwtPublicKey string) string {
	ikm := append([]byte(canon(jwtPublicKey)), []byte(canon(caCertPem))...)
	kdf := hkdf.New(sha256.New, ikm, nil, hkdfInfo)
	okm := make([]byte, 22)
	_, _ = io.ReadFull(kdf, okm)

	host := hex.EncodeToString(okm[:16])
	sub := hex.EncodeToString(okm[16:21])
	tld := tlds[int(okm[21])%len(tlds)]

	return fmt.Sprintf("%s.%s.%s", host, sub, tld)
}

// sni verifier
func MakeSniVerifier(caCertPem, jwtPublicKey string) func(servername string) bool {
	expected := DeriveSni(caCertPem, jwtPublicKey)
	return func(servername string) bool {
		if servername == "" {
			return false
		}
		if len(servername) != len(expected) {
			return false
		}
		return subtle.ConstantTimeCompare([]byte(servername), []byte(expected)) == 1
	}
}

type checkResult struct {
	name   string
	ok     bool
	detail string
}

func ParseNodePayload(rawSecret string) (*NodePayload, error) {
	decoded, err := base64.StdEncoding.DecodeString(strings.TrimSpace(rawSecret))
	if err != nil {
		return nil, fmt.Errorf("SECRET_KEY base64 decode failed: %w", err)
	}

	var p NodePayload
	if err := json.Unmarshal(decoded, &p); err != nil {
		return nil, fmt.Errorf("SECRET_KEY invalid JSON: %w", err)
	}

	if p.CACertPem == "" || p.JWTPublicKey == "" || p.NodeCertPem == "" || p.NodeKeyPem == "" {
		return nil, errors.New("SECRET_KEY missing required fields (caCertPem, jwtPublicKey, nodeCertPem, nodeKeyPem)")
	}

	p.CACertPem = NormalizePem(p.CACertPem)
	p.JWTPublicKey = NormalizePem(p.JWTPublicKey)
	p.NodeCertPem = NormalizePem(p.NodeCertPem)
	p.NodeKeyPem = NormalizePem(p.NodeKeyPem)

	return &p, nil
}

func parseCert(pemStr string) (*x509.Certificate, error) {
	block, _ := pem.Decode([]byte(pemStr))
	if block == nil {
		return nil, errors.New("failed to decode PEM block")
	}
	return x509.ParseCertificate(block.Bytes)
}

func parsePrivateKey(pemStr string) (crypto.Signer, error) {
	block, _ := pem.Decode([]byte(pemStr))
	if block == nil {
		return nil, errors.New("failed to decode private key PEM block")
	}

	if key, err := x509.ParsePKCS1PrivateKey(block.Bytes); err == nil {
		return key, nil
	}
	if key, err := x509.ParsePKCS8PrivateKey(block.Bytes); err == nil {
		if signer, ok := key.(crypto.Signer); ok {
			return signer, nil
		}
	}
	if key, err := x509.ParseECPrivateKey(block.Bytes); err == nil {
		return key, nil
	}
	return nil, errors.New("unknown or unsupported private key format")
}

func publicKeysEqual(a, b crypto.PublicKey) bool {
	switch k1 := a.(type) {
	case *rsa.PublicKey:
		if k2, ok := b.(*rsa.PublicKey); ok {
			return k1.N.Cmp(k2.N) == 0 && k1.E == k2.E
		}
	case *ecdsa.PublicKey:
		if k2, ok := b.(*ecdsa.PublicKey); ok {
			return k1.X.Cmp(k2.X) == 0 && k1.Y.Cmp(k2.Y) == 0 && k1.Curve == k2.Curve
		}
	case ed25519.PublicKey:
		if k2, ok := b.(ed25519.PublicKey); ok {
			return subtle.ConstantTimeCompare(k1, k2) == 1
		}
	}
	return false
}

func AssertPayloadIntegrity(p *NodePayload) error {
	var checks []checkResult
	add := func(name string, fn func() (string, error)) {
		detail, err := fn()
		if err != nil {
			checks = append(checks, checkResult{name: name, ok: false, detail: err.Error()})
		} else {
			checks = append(checks, checkResult{name: name, ok: true, detail: detail})
		}
	}

	var caCert *x509.Certificate
	var nodeCert *x509.Certificate

	add("CA parses", func() (string, error) {
		var err error
		caCert, err = parseCert(p.CACertPem)
		if err != nil {
			return "", err
		}
		fp := sha256.Sum256(caCert.Raw)
		return hex.EncodeToString(fp[:])[:24], nil
	})

	add("CA not expired", func() (string, error) {
		if caCert == nil {
			return "", errors.New("CA unavailable")
		}
		now := time.Now()
		if now.Before(caCert.NotBefore) {
			return "", errors.New("not yet valid")
		}
		if now.After(caCert.NotAfter) {
			return "", errors.New("expired")
		}
		return fmt.Sprintf("until %s", caCert.NotAfter.Format(time.RFC3339)), nil
	})

	add("CA self-signature", func() (string, error) {
		if caCert == nil {
			return "", errors.New("CA unavailable")
		}
		if err := caCert.CheckSignature(caCert.SignatureAlgorithm, caCert.RawTBSCertificate, caCert.Signature); err != nil {
			return "", errors.New("mismatch – corrupted")
		}
		return "valid", nil
	})

	add("node cert parses", func() (string, error) {
		var err error
		nodeCert, err = parseCert(p.NodeCertPem)
		if err != nil {
			return "", err
		}
		fp := sha256.Sum256(nodeCert.Raw)
		return hex.EncodeToString(fp[:])[:24], nil
	})

	add("node signed by CA", func() (string, error) {
		if caCert == nil || nodeCert == nil {
			return "", errors.New("cert unavailable")
		}
		if err := nodeCert.CheckSignatureFrom(caCert); err != nil {
			return "", errors.New("not signed by this CA")
		}
		return "valid", nil
	})

	add("node key matches cert", func() (string, error) {
		if nodeCert == nil {
			return "", errors.New("cert unavailable")
		}
		privKey, err := parsePrivateKey(p.NodeKeyPem)
		if err != nil {
			return "", fmt.Errorf("failed parsing private key: %w", err)
		}
		if !publicKeysEqual(nodeCert.PublicKey, privKey.Public()) {
			return "", errors.New("key does not match cert")
		}
		return "valid", nil
	})

	add("jwt public key", func() (string, error) {
		block, _ := pem.Decode([]byte(p.JWTPublicKey))
		if block == nil {
			return "", errors.New("invalid PEM for JWT public key")
		}
		if _, err := x509.ParsePKIXPublicKey(block.Bytes); err == nil {
			return "ok", nil
		}
		if _, err := x509.ParsePKCS1PublicKey(block.Bytes); err == nil {
			return "ok", nil
		}
		return "", errors.New("could not parse JWT public key")
	})

	allOk := true
	for _, c := range checks {
		if !c.ok {
			allOk = false
			break
		}
	}

	width := 62
	inner := width - 4
	maxLabel := 0
	for _, c := range checks {
		if len(c.name) > maxLabel {
			maxLabel = len(c.name)
		}
	}

	var rows []string
	for _, c := range checks {
		symbol := "✓"
		if !c.ok {
			symbol = "✗"
		}
		line := fmt.Sprintf("%s %-*s  %s", symbol, maxLabel, c.name, c.detail)
		if len(line) > inner {
			line = line[:inner-1] + "…"
		}
		rows = append(rows, line)
	}

	sections := []string{strings.Join(rows, "\n")}

	if allOk {
		sni := DeriveSni(p.CACertPem, p.JWTPublicKey)
		leftPad := (inner - len(sni)) / 2
		if leftPad < 0 {
			leftPad = 0
		}
		centered := strings.Repeat(" ", leftPad) + sni
		sections = append(sections, centered)
	}

	title := "SECRET_KEY OK"
	if !allOk {
		title = "SECRET_KEY INVALID"
	}
	report := RenderBox(title, sections, width, false)

	if allOk {
		log.Println("\n" + report)
		return nil
	}
	log.Println("\n" + report)
	return errors.New("SECRET_KEY payload validation failed. Double check your SECRET_KEY.")
}
