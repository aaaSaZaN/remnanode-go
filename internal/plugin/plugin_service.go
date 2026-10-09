package plugin

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

type SharedListItem struct {
	Type  string        `json:"type"` // ipList or asList
	Name  string        `json:"name"` // starts with ext:
	Items []interface{} `json:"items"`
}

type NodePluginConfig struct {
	SharedLists []SharedListItem `json:"sharedLists"`

	TorrentBlocker *struct {
		Enabled         bool     `json:"enabled"`
		BlockDuration   int      `json:"blockDuration"`
		RulePlacement   *int     `json:"rulePlacement"`
		WebhookURL      *string  `json:"webhookUrl"`
		IncludeRuleTags []string `json:"includeRuleTags"`
		IgnoreLists     *struct {
			IP     []string `json:"ip"`
			UserID []int    `json:"userId"`
		} `json:"ignoreLists"`
	} `json:"torrentBlocker"`

	IngressFilter *struct {
		Enabled    bool     `json:"enabled"`
		BlockedIps []string `json:"blockedIps"`
	} `json:"ingressFilter"`

	EgressFilter *struct {
		Enabled      bool     `json:"enabled"`
		BlockedIps   []string `json:"blockedIps"`
		BlockedPorts []int    `json:"blockedPorts"`
	} `json:"egressFilter"`

	ConnectionDrop *struct {
		Enabled      bool     `json:"enabled"`
		WhitelistIps []string `json:"whitelistIps"`
	} `json:"connectionDrop"`

	PreStart *struct {
		Enabled        bool `json:"enabled"`
		CleanupSockets *struct {
			Enabled bool     `json:"enabled"`
			Files   []string `json:"files"`
		} `json:"cleanupSockets"`
	} `json:"preStart"`
}

type Service struct {
	mu             sync.RWMutex
	NftManager     *NftManager
	TorrentBlocker *TorrentBlockerState
	configHash     string
	pluginUUID     string
	pluginName     string
	whitelistIPs   map[string]struct{}
	cleanupFiles   []string
}

func NewService(nft *NftManager) *Service {
	return &Service{
		NftManager:     nft,
		TorrentBlocker: NewTorrentBlockerState(),
		whitelistIPs:   make(map[string]struct{}),
	}
}

func (s *Service) IsWhitelisted(ip string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	_, exists := s.whitelistIPs[ip]
	return exists
}

func (s *Service) RunPreStart() {
	s.mu.RLock()
	files := s.cleanupFiles
	s.mu.RUnlock()

	for _, pattern := range files {
		matches, err := filepath.Glob(pattern)
		if err != nil || len(matches) == 0 {
			matches = []string{pattern}
		}
		for _, file := range matches {
			if fi, err := os.Lstat(file); err == nil {
				if fi.Mode()&os.ModeSocket != 0 || !fi.IsDir() {
					_ = os.Remove(file)
					log.Printf("[PLUGIN] Pre-Start removed socket/file: %s", file)
				}
			}
		}
	}
}

type SyncRequest struct {
	Plugin *struct {
		Config map[string]interface{} `json:"config"`
		UUID   string                 `json:"uuid"`
		Name   string                 `json:"name"`
	} `json:"plugin"`
}

func (s *Service) Sync(req SyncRequest) (accepted bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if req.Plugin == nil {
		if s.pluginUUID != "" {
			log.Println("[PLUGIN] Received empty plugins, resetting active plugins...")
			s.resetLocked()
		}
		return true
	}

	configBytes, err := json.Marshal(req.Plugin.Config)
	if err != nil {
		log.Printf("[PLUGIN] Failed to serialize plugin config: %v", err)
		return false
	}

	h := sha256.Sum256(configBytes)
	hashStr := hex.EncodeToString(h[:])
	if hashStr == s.configHash {
		log.Println("[PLUGIN] Config unchanged. Skipping sync.")
		return true
	}

	var parsed NodePluginConfig
	if err := json.Unmarshal(configBytes, &parsed); err != nil {
		log.Printf("[PLUGIN] Invalid plugin config: %v", err)
		s.resetLocked()
		return false
	}

	// resolve shared lists
	sharedMap := make(map[string][]string)
	for _, sl := range parsed.SharedLists {
		if sl.Type == "ipList" {
			var ips []string
			for _, item := range sl.Items {
				if str, ok := item.(string); ok {
					ips = append(ips, str)
				}
			}
			sharedMap[sl.Name] = ips
		}
	}

	resolveIPs := func(in []string) []string {
		var out []string
		for _, ip := range in {
			if strings.HasPrefix(ip, "ext:") {
				if resolved, ok := sharedMap[ip]; ok {
					out = append(out, resolved...)
				} else {
					log.Printf("[PLUGIN] Warning: shared IP list %s not found", ip)
				}
			} else {
				out = append(out, ip)
			}
		}
		return out
	}

	// recreate nftables tables
	_ = s.NftManager.RecreateTables()

	// connection drop
	s.whitelistIPs = make(map[string]struct{})
	if parsed.ConnectionDrop != nil && parsed.ConnectionDrop.Enabled {
		resolved := resolveIPs(parsed.ConnectionDrop.WhitelistIps)
		for _, ip := range resolved {
			s.whitelistIPs[ip] = struct{}{}
		}
		log.Printf("[PLUGIN] Connection-Drop: %d whitelisted IPs synced", len(resolved))
	}

	// prestart
	s.cleanupFiles = nil
	if parsed.PreStart != nil && parsed.PreStart.Enabled && parsed.PreStart.CleanupSockets != nil && parsed.PreStart.CleanupSockets.Enabled {
		s.cleanupFiles = parsed.PreStart.CleanupSockets.Files
		log.Printf("[PLUGIN] Pre-Start: %d cleanup file pattern(s) configured", len(s.cleanupFiles))
	}

	// ingress filter
	if parsed.IngressFilter != nil && parsed.IngressFilter.Enabled {
		resolved := resolveIPs(parsed.IngressFilter.BlockedIps)
		_ = s.NftManager.AddAddresses(resolved, IngressFilterIPSetName)
		log.Printf("[PLUGIN] Ingress Filter: %d IPs synced", len(resolved))
	}

	// egress filter
	if parsed.EgressFilter != nil && parsed.EgressFilter.Enabled {
		resolved := resolveIPs(parsed.EgressFilter.BlockedIps)
		_ = s.NftManager.AddAddresses(resolved, EgressFilterIPSetName)
		if len(parsed.EgressFilter.BlockedPorts) > 0 {
			_ = s.NftManager.AddPorts(parsed.EgressFilter.BlockedPorts, EgressFilterPortSetName)
		}
		log.Printf("[PLUGIN] Egress Filter: %d IPs, %d ports synced", len(resolved), len(parsed.EgressFilter.BlockedPorts))
	}

	// torrent blocker
	if parsed.TorrentBlocker != nil && parsed.TorrentBlocker.Enabled {
		var ignoredIPs []string
		var ignoredUsers []string
		if parsed.TorrentBlocker.IgnoreLists != nil {
			ignoredIPs = resolveIPs(parsed.TorrentBlocker.IgnoreLists.IP)
			for _, uid := range parsed.TorrentBlocker.IgnoreLists.UserID {
				ignoredUsers = append(ignoredUsers, strings.TrimSpace(string(rune(uid))))
			}
		}
		rulePos := 0
		if parsed.TorrentBlocker.RulePlacement != nil {
			rulePos = *parsed.TorrentBlocker.RulePlacement
		}
		webhook := ""
		if parsed.TorrentBlocker.WebhookURL != nil {
			webhook = *parsed.TorrentBlocker.WebhookURL
		}
		s.TorrentBlocker.Configure(
			true,
			parsed.TorrentBlocker.BlockDuration,
			rulePos,
			webhook,
			parsed.TorrentBlocker.IncludeRuleTags,
			ignoredIPs,
			ignoredUsers,
		)
		log.Printf("[PLUGIN] Torrent-Blocker: duration=%ds, %d ignored IPs", parsed.TorrentBlocker.BlockDuration, len(ignoredIPs))
	} else {
		s.TorrentBlocker.Reset()
	}

	s.configHash = hashStr
	s.pluginUUID = req.Plugin.UUID
	s.pluginName = req.Plugin.Name

	return true
}

func (s *Service) Reset() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.resetLocked()
}

func (s *Service) resetLocked() {
	s.TorrentBlocker.Reset()
	s.whitelistIPs = make(map[string]struct{})
	s.cleanupFiles = nil
	s.configHash = ""
	s.pluginUUID = ""
	s.pluginName = ""
	_ = s.NftManager.RecreateTables()
}

// http controllers

func (s *Service) HandleSync(w http.ResponseWriter, r *http.Request) {
	var req SyncRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"response": map[string]interface{}{"accepted": false},
		})
		return
	}

	accepted := s.Sync(req)
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"response": map[string]interface{}{"accepted": accepted},
	})
}

func (s *Service) HandleCollectReports(w http.ResponseWriter, r *http.Request) {
	reports := s.TorrentBlocker.FlushReports()
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"response": map[string]interface{}{
			"reports": reports,
		},
	})
}

type BlockIPsRequest struct {
	IPs []struct {
		IP      string `json:"ip"`
		Timeout int    `json:"timeout"`
	} `json:"ips"`
}

func (s *Service) HandleBlockIPs(w http.ResponseWriter, r *http.Request) {
	var req BlockIPsRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"response": map[string]interface{}{"accepted": false},
		})
		return
	}

	for _, item := range req.IPs {
		_ = s.NftManager.BlockIP(item.IP, item.Timeout)
		KillSockets([]string{item.IP})
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"response": map[string]interface{}{"accepted": true},
	})
}

type UnblockIPsRequest struct {
	IPs []string `json:"ips"`
}

func (s *Service) HandleUnblockIPs(w http.ResponseWriter, r *http.Request) {
	var req UnblockIPsRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"response": map[string]interface{}{"accepted": false},
		})
		return
	}

	_ = s.NftManager.UnblockIPs(req.IPs, TorrentBlockerSetName)
	_ = s.NftManager.UnblockIPs(req.IPs, IngressFilterIPSetName)

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"response": map[string]interface{}{"accepted": true},
	})
}

func (s *Service) HandleRecreateTables(w http.ResponseWriter, r *http.Request) {
	err := s.NftManager.RecreateTables()
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"response": map[string]interface{}{"accepted": err == nil},
	})
}

func writeJSON(w http.ResponseWriter, status int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(data)
}
