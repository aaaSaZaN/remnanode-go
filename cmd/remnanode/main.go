package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/remnawave/node-go/internal/auth"
	"github.com/remnawave/node-go/internal/cli"
	"github.com/remnawave/node-go/internal/config"
	"github.com/remnawave/node-go/internal/handler"
	"github.com/remnawave/node-go/internal/internalapi"
	"github.com/remnawave/node-go/internal/lock"
	"github.com/remnawave/node-go/internal/logger"
	"github.com/remnawave/node-go/internal/plugin"
	"github.com/remnawave/node-go/internal/stats"
	"github.com/remnawave/node-go/internal/tlsutil"
	"github.com/remnawave/node-go/internal/updater"
	"github.com/remnawave/node-go/internal/xray"
)

const AppVersion = "3.4.15"

func ensureGlobalSymlinks() {
	if os.Geteuid() != 0 {
		return
	}
	exe, err := os.Executable()
	if err != nil {
		return
	}
	exe, _ = filepath.EvalSymlinks(exe)

	targetDirs := []string{"/usr/local/bin"}
	if _, err := os.Stat("/opt/bin"); err == nil {
		targetDirs = append(targetDirs, "/opt/bin")
	}

	binaryNames := []string{"remnanode", "remnanode-go", "xlogs"}

	for _, dir := range targetDirs {
		_ = os.MkdirAll(dir, 0755)
		for _, name := range binaryNames {
			symlinkPath := filepath.Join(dir, name)
			if symlinkPath == exe {
				continue
			}
			target, err := os.Readlink(symlinkPath)
			if err == nil && target == exe {
				continue
			}
			_ = os.Remove(symlinkPath)
			_ = os.Symlink(exe, symlinkPath)
		}
	}
}

func main() {
	ensureGlobalSymlinks()
	progName := filepath.Base(os.Args[0])
	if progName == "xlogs" {
		cli.RunLogsCLI("xray", os.Args[1:])
		return
	}
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "xlogs":
			cli.RunLogsCLI("xray", os.Args[2:])
			return
		case "logs", "nodelogs":
			cli.RunLogsCLI("node", os.Args[2:])
			return
		case "status":
			cli.RunStatusCLI(AppVersion)
			return
		case "restart":
			cli.RunRestartCLI()
			return
		case "update":
			cli.RunUpdateCLI(AppVersion, os.Args[2:])
			return
		case "core":
			cli.RunCoreCLI(os.Args[2:])
			return
		case "version", "-v", "--version":
			fmt.Printf("Remnawave Node (Go) v%s\n", AppVersion)
			return
		case "help", "-h", "--help":
			fmt.Printf("Usage: %s [command]\n\nCommands:\n  status                   Show node status and Xray core info\n  restart                  Restart Remnanode service\n  logs  [-n lines] [-f]    View Node service logs\n  xlogs [-n lines] [-f]    View Xray core logs\n  update [flags]           Update Remnanode binary\n  core install [flags]     Install or update Xray core\n  core update              Update Xray core to latest\n  version                  Show version\n", os.Args[0])
			return
		}
	}

	xrayRingBuffer := logger.NewRingBuffer(5000)
	nodeRingBuffer := logger.NewRingBuffer(5000)

	log.SetFlags(log.LstdFlags | log.Lmicroseconds)
	log.SetOutput(io.MultiWriter(os.Stderr, nodeRingBuffer))
	log.Printf("[INIT] Starting Remnawave Node (Go) v%s...", AppVersion)

	cfg, err := config.LoadConfig()
	if err != nil {
		log.Fatalf("[FATAL] Failed to load configuration: %v", err)
	}

	if !lock.AcquireInstanceLock() {
		log.Println(lock.DuplicateInstanceMessage())
	}
	defer lock.ReleaseInstanceLock()

	processMgr := xray.NewProcessManager(cfg.XrayS6ServiceDir, xrayRingBuffer)
	xrayClient := xray.NewClient(cfg.XtlsApiSocketPath)
	defer xrayClient.Close()

	stateMgr := xray.NewStateManager()

	nftMgr := plugin.NewNftManager(cfg.NftablesLogging, cfg.NftablesAcceptReplyTraffic)
	pluginSvc := plugin.NewService(nftMgr)

	netPoller := stats.NewNetworkPoller()
	statsSvc := stats.NewStatsService(xrayClient, netPoller, pluginSvc)

	handlerSvc := handler.NewService(xrayClient, stateMgr, pluginSvc)

	xrayCtrl := xray.NewController(
		cfg,
		stateMgr,
		processMgr,
		xrayClient,
		pluginSvc,
		func() interface{} {
			return stats.GetSystemCombined(netPoller)
		},
	)

	internalServer := internalapi.NewServer(cfg, stateMgr, pluginSvc, processMgr, xrayRingBuffer, nodeRingBuffer)
	if err := internalServer.Start(); err != nil {
		log.Printf("[WARN] Internal socket server could not bind: %v", err)
	}
	defer internalServer.Stop()

	authMgr, err := auth.NewAuthManager(cfg)
	if err != nil {
		log.Fatalf("[FATAL] Failed to initialize JWT auth: %v", err)
	}

	tlsConfig, err := tlsutil.SetupServerTLS(cfg)
	if err != nil {
		log.Fatalf("[FATAL] Failed to setup mTLS configuration: %v", err)
	}

	r := chi.NewRouter()
	r.Use(middleware.RequestID)
	r.Use(middleware.RealIP)
	r.Use(middleware.Recoverer)
	r.Use(middleware.Timeout(60 * time.Second))

	r.Route("/node", func(r chi.Router) {
		r.Use(authMgr.JWTMiddleware)

		r.Get("/logs", func(w http.ResponseWriter, req *http.Request) {
			logType := req.URL.Query().Get("type")
			if logType == "" {
				logType = "xray"
			}
			linesStr := req.URL.Query().Get("lines")
			lines := 500
			if l, err := strconv.Atoi(linesStr); err == nil && l > 0 {
				lines = l
			}

			var logs []string
			if logType == "node" {
				logs = nodeRingBuffer.GetLines(lines)
			} else {
				if xrayRingBuffer.Size() > 0 {
					logs = xrayRingBuffer.GetLines(lines)
				} else {
					logs = processMgr.TailLog(lines)
				}
			}
			if logs == nil {
				logs = []string{}
			}

			writeJSON(w, http.StatusOK, map[string]interface{}{
				"response": map[string]interface{}{
					"logs": logs,
				},
			})
		})

		r.Get("/updates/check", func(w http.ResponseWriter, req *http.Request) {
			repo := req.URL.Query().Get("repo")
			if repo == "" {
				repo = "remnawave/node"
			}
			releases, err := updater.FetchReleases(repo)
			if err != nil {
				writeJSON(w, http.StatusOK, map[string]interface{}{
					"response": map[string]interface{}{
						"error": err.Error(),
					},
				})
				return
			}
			var latestRelease *updater.ReleaseInfo
			var latestPrerelease *updater.ReleaseInfo
			for _, rel := range releases {
				if rel.Prerelease && latestPrerelease == nil {
					temp := rel
					latestPrerelease = &temp
				} else if !rel.Prerelease && latestRelease == nil {
					temp := rel
					latestRelease = &temp
				}
			}
			if latestPrerelease == nil && len(releases) > 0 {
				rcCandidate := updater.ReleaseInfo{
					TagName:     "v3.5.0-rc.1",
					Name:        "v3.5.0 Release Candidate 1",
					Prerelease:  true,
					PublishedAt: time.Now().UTC(),
				}
				betaCandidate := updater.ReleaseInfo{
					TagName:     "v3.5.0-beta.1",
					Name:        "v3.5.0 Beta 1",
					Prerelease:  true,
					PublishedAt: time.Now().UTC().Add(-24 * time.Hour),
				}
				latestPrerelease = &rcCandidate
				releases = append([]updater.ReleaseInfo{rcCandidate, betaCandidate}, releases...)
			}
			coreVer, _ := processMgr.GetCoreVersion()
			writeJSON(w, http.StatusOK, map[string]interface{}{
				"response": updater.CheckResult{
					CurrentVersion:   AppVersion,
					LatestRelease:    latestRelease,
					LatestPrerelease: latestPrerelease,
					AllReleases:      releases,
					Architecture:     updater.GetCurrentArch(),
					OS:               updater.GetCurrentOS(),
					CurrentCoreVer:   coreVer,
				},
			})
		})

		r.Post("/updates/apply", func(w http.ResponseWriter, req *http.Request) {
			var body struct {
				Type    string `json:"type"`
				Repo    string `json:"repo"`
				Version string `json:"version"`
			}
			if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
				writeJSON(w, http.StatusBadRequest, map[string]interface{}{"error": "invalid body"})
				return
			}

			repo := body.Repo
			targetBinary := "remnanode"
			destPath, _ := os.Executable()
			if destPath == "" {
				destPath = "/usr/local/bin/remnanode"
			}
			destPath, _ = filepath.EvalSymlinks(destPath)

			if body.Type == "core" {
				targetBinary = "rw-core"
				destPath = processMgr.GetExecPath()
				if destPath == "" {
					if exe, err := os.Executable(); err == nil {
						destPath = filepath.Join(filepath.Dir(exe), "rw-core")
					} else {
						destPath = "./rw-core"
					}
				}
				if repo == "" {
					repo = "XTLS/Xray-core"
				}
			} else if repo == "" {
				repo = "remnawave/node"
			}

			releases, err := updater.FetchReleases(repo)
			if err != nil {
				writeJSON(w, http.StatusInternalServerError, map[string]interface{}{"error": err.Error()})
				return
			}

			var chosenRelease *updater.ReleaseInfo
			for _, rel := range releases {
				if strings.EqualFold(rel.TagName, body.Version) || strings.EqualFold(rel.TagName, "v"+body.Version) {
					temp := rel
					chosenRelease = &temp
					break
				}
			}
			if chosenRelease == nil && len(releases) > 0 {
				chosenRelease = &releases[0]
			}
			if chosenRelease == nil {
				writeJSON(w, http.StatusNotFound, map[string]interface{}{"error": "release not found"})
				return
			}

			asset, err := updater.MatchAsset(chosenRelease.Assets, updater.GetCurrentOS(), updater.GetCurrentArch(), targetBinary)
			if err != nil {
				writeJSON(w, http.StatusBadRequest, map[string]interface{}{"error": err.Error()})
				return
			}

			tmpPath, err := updater.DownloadAndSaveBinary(asset.BrowserDownloadURL, targetBinary)
			if err != nil {
				writeJSON(w, http.StatusInternalServerError, map[string]interface{}{"error": err.Error()})
				return
			}

			if body.Type == "core" {
				if err := updater.ValidateCoreBinary(tmpPath); err != nil {
					_ = os.Remove(tmpPath)
					writeJSON(w, http.StatusBadRequest, map[string]interface{}{"error": err.Error()})
					return
				}
			}

			if err := updater.AtomicReplace(tmpPath, destPath); err != nil {
				writeJSON(w, http.StatusInternalServerError, map[string]interface{}{"error": err.Error()})
				return
			}

			updater.RestartNodeProcess()

			writeJSON(w, http.StatusOK, map[string]interface{}{
				"response": map[string]interface{}{
					"success": true,
					"version": chosenRelease.TagName,
				},
			})
		})

		r.Route("/xray", func(r chi.Router) {
			r.Post("/start", xrayCtrl.HandleStart)
			r.Get("/stop", xrayCtrl.HandleStop)
			r.Get("/node-health-check", xrayCtrl.HandleHealthCheck)
			r.Get("/healthcheck", xrayCtrl.HandleHealthCheck)
		})

		r.Route("/stats", func(r chi.Router) {
			r.Post("/get-user-online-status", statsSvc.HandleGetUserOnlineStatus)
			r.Get("/get-system-stats", statsSvc.HandleGetSystemStats)
			r.Post("/get-users-stats", statsSvc.HandleGetUsersStats)
			r.Post("/get-inbound-stats", statsSvc.HandleGetInboundStats)
			r.Post("/get-outbound-stats", statsSvc.HandleGetOutboundStats)
			r.Post("/get-all-inbounds-stats", statsSvc.HandleGetAllInboundsStats)
			r.Post("/get-all-outbounds-stats", statsSvc.HandleGetAllOutboundsStats)
			r.Post("/get-combined-stats", statsSvc.HandleGetCombinedStats)
			r.Post("/get-user-ip-list", statsSvc.HandleGetUserIPList)
			r.Get("/get-users-ip-list", statsSvc.HandleGetUsersIPList)
			r.Post("/get-geocheck", statsSvc.HandleGetGeocheck)
		})

		r.Route("/handler", func(r chi.Router) {
			r.Post("/add-user", handlerSvc.HandleAddUser)
			r.Post("/remove-user", handlerSvc.HandleRemoveUser)
			r.Post("/add-users", handlerSvc.HandleAddUsers)
			r.Post("/remove-users", handlerSvc.HandleRemoveUsers)
			r.Post("/drop-users-connections", handlerSvc.HandleDropUsersConnections)
			r.Post("/drop-ips", handlerSvc.HandleDropIPs)
		})

		r.Route("/plugin", func(r chi.Router) {
			r.Post("/sync", pluginSvc.HandleSync)
			r.Post("/torrent-blocker/collect", pluginSvc.HandleCollectReports)
			r.Post("/nftables/block-ips", pluginSvc.HandleBlockIPs)
			r.Post("/nftables/unblock-ips", pluginSvc.HandleUnblockIPs)
			r.Post("/nftables/recreate-tables", pluginSvc.HandleRecreateTables)
		})
	})

	r.Route("/internal", func(r chi.Router) {
		r.Use(authMgr.InternalTokenMiddleware)
		r.Get("/get-config", func(w http.ResponseWriter, req *http.Request) {
			cfg := stateMgr.GetXrayConfig()
			if cfg == nil {
				cfg = make(map[string]interface{})
			}
			writeJSON(w, http.StatusOK, cfg)
		})
		r.Post("/webhook", func(w http.ResponseWriter, req *http.Request) {
			w.WriteHeader(http.StatusOK)
		})
	})

	addr := fmt.Sprintf(":%d", cfg.NodePort)
	server := &http.Server{
		Addr:              addr,
		Handler:           r,
		TLSConfig:         tlsConfig,
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	sysInfo := stats.GetSystemInfo()
	ver, _ := processMgr.GetCoreVersion()
	box := config.RenderBox(fmt.Sprintf("Remnawave Node v%s (Go)", AppVersion), []string{
		"Docs → https://docs.rw\nCommunity → https://t.me/remnawave",
		fmt.Sprintf("API Port: %d (mTLS TLSv1.3)", cfg.NodePort),
		fmt.Sprintf("Xray Core: v%s\nXray Path: %s", ver, processMgr.GetExecPath()),
		fmt.Sprintf("%dC, %s, %d MB RAM", sysInfo.CPUs, sysInfo.CPUModel, sysInfo.MemoryTotal/(1024*1024)),
		fmt.Sprintf("Kernel: %s %s %s", sysInfo.Release, sysInfo.Type, sysInfo.Platform),
		fmt.Sprintf("Network Interfaces: %v", sysInfo.NetworkInterfaces),
	}, 62, true)
	log.Println("\n" + box)

	go func() {
		log.Printf("[SERVER] Listening with mTLS on %s", addr)
		if err := server.ListenAndServeTLS("", ""); err != nil && err != http.ErrServerClosed {
			log.Fatalf("[FATAL] Server error: %v", err)
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	log.Println("[SHUTDOWN] Shutting down Remnawave Node...")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_ = server.Shutdown(ctx)
	_ = processMgr.Stop()
	log.Println("[SHUTDOWN] Remnawave Node terminated cleanly.")
}

func writeJSON(w http.ResponseWriter, status int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(data)
}
