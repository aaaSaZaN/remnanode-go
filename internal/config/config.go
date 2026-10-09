package config

import (
	"bufio"
	"errors"
	"os"
	"path/filepath"
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

// LoadEnv loads environment variables from .env files if not already set.
func LoadEnv() {
	candidates := []string{".env"}
	if exe, err := os.Executable(); err == nil {
		candidates = append(candidates, filepath.Join(filepath.Dir(exe), ".env"))
	}
	candidates = append(candidates, "/opt/remnanode/.env", "/etc/remnanode/.env")

	seen := make(map[string]bool)
	for _, p := range candidates {
		abs, err := filepath.Abs(p)
		if err != nil {
			abs = p
		}
		if seen[abs] {
			continue
		}
		seen[abs] = true

		file, err := os.Open(abs)
		if err != nil {
			continue
		}
		scanner := bufio.NewScanner(file)
		for scanner.Scan() {
			line := strings.TrimSpace(scanner.Text())
			if line == "" || strings.HasPrefix(line, "#") {
				continue
			}
			parts := strings.SplitN(line, "=", 2)
			if len(parts) == 2 {
				key := strings.TrimSpace(parts[0])
				val := strings.TrimSpace(parts[1])
				if (strings.HasPrefix(val, "\"") && strings.HasSuffix(val, "\"")) ||
					(strings.HasPrefix(val, "'") && strings.HasSuffix(val, "'")) {
					val = val[1 : len(val)-1]
				}
				if _, exists := os.LookupEnv(key); !exists {
					_ = os.Setenv(key, val)
				}
			}
		}
		_ = file.Close()
	}
}

func LoadConfig() (*Config, error) {
	LoadEnv()

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
