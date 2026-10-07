package config

import (
	"errors"
	"os"
	"strconv"
	"strings"
)

type Config struct {
	NodePort                   int
	SecretKey                  string
	InternalRestToken          string
	InternalSocketPath         string
	XtlsApiSocketPath          string
	NftablesLogging            bool
	NftablesAcceptReplyTraffic bool
	SniVerification            bool
	DisableHashedSetCheck      bool
	XrayS6ServiceDir           string

	Payload *NodePayload
}

func parseBool(val string, def bool) bool {
	val = strings.TrimSpace(strings.ToLower(val))
	if val == "true" {
		return true
	}
	if val == "false" {
		return false
	}
	return def
}

func LoadConfig() (*Config, error) {
	secretKey := os.Getenv("SECRET_KEY")
	if secretKey == "" {
		return nil, errors.New("SECRET_KEY missing in environment variables")
	}

	payload, err := ParseNodePayload(secretKey)
	if err != nil {
		return nil, err
	}

	if err := AssertPayloadIntegrity(payload); err != nil {
		return nil, err
	}

	nodePort := 3000
	if portStr := os.Getenv("NODE_PORT"); portStr != "" {
		if p, err := strconv.Atoi(portStr); err == nil && p > 0 {
			nodePort = p
		}
	}

	internalToken := os.Getenv("INTERNAL_REST_TOKEN")
	if internalToken == "" {
		internalToken = "remnanode-internal-secret"
	}

	internalSocket := os.Getenv("INTERNAL_SOCKET_PATH")
	if internalSocket == "" {
		internalSocket = "rw-node.sock"
	}

	xtlsSocket := os.Getenv("XTLS_API_SOCKET_PATH")
	if xtlsSocket == "" {
		xtlsSocket = "rw-api.sock"
	}

	s6ServiceDir := os.Getenv("XRAY_S6_SERVICE_DIR")
	if s6ServiceDir == "" {
		s6ServiceDir = "/run/service/xray"
	}

	return &Config{
		NodePort:                   nodePort,
		SecretKey:                  secretKey,
		InternalRestToken:          internalToken,
		InternalSocketPath:         internalSocket,
		XtlsApiSocketPath:          xtlsSocket,
		NftablesLogging:            parseBool(os.Getenv("NFTABLES_LOGGING"), true),
		NftablesAcceptReplyTraffic: parseBool(os.Getenv("NFTABLES_ACCEPT_REPLY_TRAFFIC"), false),
		SniVerification:            parseBool(os.Getenv("SNI_VERIFICATION"), false),
		DisableHashedSetCheck:      parseBool(os.Getenv("DISABLE_HASHED_SET_CHECK"), false),
		XrayS6ServiceDir:           s6ServiceDir,
		Payload:                    payload,
	}, nil
}
