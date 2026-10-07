package tlsutil

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"

	"github.com/remnawave/node-go/internal/config"
)

// setupServerTLS creates a *tls.Config for mtls
func SetupServerTLS(cfg *config.Config) (*tls.Config, error) {
	cert, err := tls.X509KeyPair([]byte(cfg.Payload.NodeCertPem), []byte(cfg.Payload.NodeKeyPem))
	if err != nil {
		return nil, fmt.Errorf("failed to load node key pair: %w", err)
	}

	caPool := x509.NewCertPool()
	if ok := caPool.AppendCertsFromPEM([]byte(cfg.Payload.CACertPem)); !ok {
		return nil, errors.New("failed to append CA certificate to cert pool")
	}

	sniVerifier := config.MakeSniVerifier(cfg.Payload.CACertPem, cfg.Payload.JWTPublicKey)

	tlsConfig := &tls.Config{
		Certificates: []tls.Certificate{cert},
		ClientAuth:   tls.RequireAndVerifyClientCert,
		ClientCAs:    caPool,
		MinVersion:   tls.VersionTLS13,
	}

	if cfg.SniVerification {
		tlsConfig.GetCertificate = func(hello *tls.ClientHelloInfo) (*tls.Certificate, error) {
			if !sniVerifier(hello.ServerName) {
				return nil, errors.New("unknown sni")
			}
			return &cert, nil
		}
	}

	return tlsConfig, nil
}
