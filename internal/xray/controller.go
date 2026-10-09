package xray

import (
	"os"
	"path/filepath"
	"strings"
	"context"
	"encoding/json"
	"log"
	"net/http"
	"sync"
	"time"

	"github.com/remnawave/node-go/internal/config"
)

type PluginInterface interface {
	RunPreStart()
	Reset()
}

type StatsInterface interface {
	GetCombined() interface{}
}

type Controller struct {
	mu                      sync.Mutex
	cfg                     *config.Config
	state                   *StateManager
	process                 *ProcessManager
	client                  *Client
	plugin                  PluginInterface
	statsPoller             interface{}
	systemStatsGetter       func() interface{}
	isXrayOnline            bool
	isXrayStartedProcessing bool
	xrayVersion             *string
	nodeVersion             string
}

func NewController(
	cfg *config.Config,
	state *StateManager,
	process *ProcessManager,
	client *Client,
	plugin PluginInterface,
	systemStatsGetter func() interface{},
) *Controller {
	c := &Controller{
		cfg:               cfg,
		state:             state,
		process:           process,
		client:            client,
		plugin:            plugin,
		systemStatsGetter: systemStatsGetter,
		nodeVersion:       "3.4.15",
	}

	if ver, err := process.GetCoreVersion(); err == nil && ver != "" {
		c.xrayVersion = &ver
	}

	return c
}

type StartXrayRequest struct {
	Internals struct {
		ForceRestart bool       `json:"forceRestart"`
		Hashes       HashesInfo `json:"hashes"`
	} `json:"internals"`
	XrayConfig map[string]interface{} `json:"xrayConfig"`
}

func (c *Controller) HandleStart(w http.ResponseWriter, r *http.Request) {
	c.mu.Lock()
	if c.isXrayStartedProcessing {
		c.mu.Unlock()
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"response": map[string]interface{}{
				"isStarted":       false,
				"version":         c.xrayVersion,
				"error":           "Request already in progress",
				"nodeInformation": map[string]interface{}{"version": c.nodeVersion},
				"system":          c.systemStatsGetter(),
			},
		})
		return
	}
	c.isXrayStartedProcessing = true
	c.mu.Unlock()

	defer func() {
		c.mu.Lock()
		c.isXrayStartedProcessing = false
		c.mu.Unlock()
	}()

	var req StartXrayRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"response": map[string]interface{}{
				"isStarted":       false,
				"version":         c.xrayVersion,
				"error":           "Invalid request body",
				"nodeInformation": map[string]interface{}{"version": c.nodeVersion},
				"system":          c.systemStatsGetter(),
			},
		})
		return
	}

	// check if restart needed
	if c.isXrayOnline && !c.cfg.DisableHashedSetCheck && !req.Internals.ForceRestart {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		_, err := c.client.GetSysStats(ctx)
		cancel()

		if err == nil {
			if !c.state.IsNeedRestartCore(req.Internals.Hashes) {
				writeJSON(w, http.StatusOK, map[string]interface{}{
					"response": map[string]interface{}{
						"isStarted":       true,
						"version":         c.xrayVersion,
						"error":           nil,
						"nodeInformation": map[string]interface{}{"version": c.nodeVersion},
						"system":          c.systemStatsGetter(),
					},
				})
				return
			}
		}
	}

	// prepare config
	tbState := TorrentBlockerState{
		Enabled: false,
	}
	// check plugin torrent blocker if enabled
	internalCfg := InternalConfig{
		SocketPath:        c.cfg.InternalSocketPath,
		Token:             c.cfg.InternalRestToken,
		XtlsApiSocketPath: c.cfg.XtlsApiSocketPath,
	}

	fullConfig := GenerateApiConfig(req.XrayConfig, tbState, internalCfg, true)
	c.state.ExtractUsersFromConfig(req.Internals.Hashes, fullConfig)

	// pre-start socket cleanup
	c.plugin.RunPreStart()
	cleanupInboundSockets(fullConfig)

	// stop & start xray
	_ = c.process.Stop()
	c.client.Close()
	if err := c.process.Start(fullConfig); err != nil {
		log.Printf("[XRAY] Failed to start xray: %v", err)
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"response": map[string]interface{}{
				"isStarted":       false,
				"version":         nil,
				"error":           err.Error(),
				"nodeInformation": map[string]interface{}{"version": c.nodeVersion},
				"system":          c.systemStatsGetter(),
			},
		})
		return
	}

	// wait for xray to become ready
	started := false
	for attempt := 0; attempt < 30; attempt++ {
		time.Sleep(300 * time.Millisecond)
		ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
		_, err := c.client.GetSysStats(ctx)
		cancel()
		if err == nil {
			started = true
			break
		}
		c.client.Close()
	}

	c.mu.Lock()
	c.isXrayOnline = started
	if ver, err := c.process.GetCoreVersion(); err == nil && ver != "" {
		c.xrayVersion = &ver
	}
	c.mu.Unlock()

	if !started {
		log.Println("[XRAY] Core did not become ready in time")
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"response": map[string]interface{}{
				"isStarted":       false,
				"version":         c.xrayVersion,
				"error":           "Xray Core did not become ready in time",
				"nodeInformation": map[string]interface{}{"version": c.nodeVersion},
				"system":          c.systemStatsGetter(),
			},
		})
		return
	}

	log.Printf("[XRAY] ✔ Xray Core is up and running.")
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"response": map[string]interface{}{
			"isStarted":       true,
			"version":         c.xrayVersion,
			"error":           nil,
			"nodeInformation": map[string]interface{}{"version": c.nodeVersion},
			"system":          c.systemStatsGetter(),
		},
	})
}

func (c *Controller) HandleStop(w http.ResponseWriter, r *http.Request) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.plugin.Reset()
	_ = c.process.Stop()
	c.client.Close()
	c.isXrayOnline = false
	c.state.Cleanup()

	log.Println("[XRAY] Stopped Xray process.")
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"response": map[string]interface{}{
			"isStopped": true,
		},
	})
}

func (c *Controller) isCustomCore() bool {
	if os.Getenv("CUSTOM_CORE") == "true" || os.Getenv("IS_CUSTOM_CORE") == "true" {
		return true
	}
	if _, err := os.Stat("/usr/local/bin/xray-custom"); err == nil {
		return true
	}
	if _, err := os.Stat("/usr/local/bin/.rw-core.json"); err == nil {
		return true
	}
	if exe, err := os.Executable(); err == nil {
		marker := filepath.Join(filepath.Dir(exe), ".rw-core.json")
		if _, err := os.Stat(marker); err == nil {
			return true
		}
	}
	if c.xrayVersion != nil {
		v := strings.ToLower(*c.xrayVersion)
		if strings.Contains(v, "custom") || strings.Contains(v, "mod") {
			return true
		}
	}
	return false
}

func (c *Controller) HandleHealthCheck(w http.ResponseWriter, r *http.Request) {
	c.mu.Lock()
	defer c.mu.Unlock()

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"response": map[string]interface{}{
			"isAlive":                  true,
			"xrayInternalStatusCached": c.isXrayOnline,
			"xrayVersion":              c.xrayVersion,
			"nodeVersion":              c.nodeVersion,
			"nodeType":                 "go",
			"isCustomCore":             c.isCustomCore(),
		},
	})
}

func (c *Controller) HandleVersion(w http.ResponseWriter, r *http.Request) {
	c.mu.Lock()
	defer c.mu.Unlock()

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"response": map[string]interface{}{
			"nodeVersion":  c.nodeVersion,
			"nodeType":     "go",
			"xrayVersion":  c.xrayVersion,
			"isCustomCore": c.isCustomCore(),
		},
	})
}

func writeJSON(w http.ResponseWriter, status int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(data)
}

func cleanupInboundSockets(cfg map[string]interface{}) {
	inbounds, ok := cfg["inbounds"].([]interface{})
	if !ok {
		return
	}
	for _, rawInb := range inbounds {
		inbMap, ok := rawInb.(map[string]interface{})
		if !ok {
			continue
		}
		streamSettings, ok := inbMap["streamSettings"].(map[string]interface{})
		if !ok {
			continue
		}
		if ds, ok := streamSettings["dsSettings"].(map[string]interface{}); ok {
			if p, ok := ds["path"].(string); ok && p != "" {
				_ = os.Remove(p)
				log.Printf("[XRAY] Pre-start cleaned domain socket: %s", p)
			}
		}
		if sh, ok := streamSettings["splithttpSettings"].(map[string]interface{}); ok {
			if p, ok := sh["path"].(string); ok && strings.HasPrefix(p, "/") {
				_ = os.Remove(p)
				log.Printf("[XRAY] Pre-start cleaned splithttp socket: %s", p)
			}
		}
		if xh, ok := streamSettings["xhttpSettings"].(map[string]interface{}); ok {
			if p, ok := xh["path"].(string); ok && strings.HasPrefix(p, "/") {
				_ = os.Remove(p)
				log.Printf("[XRAY] Pre-start cleaned xhttp socket: %s", p)
			}
		}
	}
}
