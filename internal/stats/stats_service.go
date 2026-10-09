package stats

import (
	"strconv"
	"strings"
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/remnawave/node-go/internal/plugin"
	"github.com/remnawave/node-go/internal/xray"
)

type StatsService struct {
	xrayClient *xray.Client
	netPoller  *NetworkPoller
	pluginSvc  *plugin.Service
	geocheck   *GeocheckService
}

func NewStatsService(xrayClient *xray.Client, netPoller *NetworkPoller, pluginSvc *plugin.Service) *StatsService {
	return &StatsService{
		xrayClient: xrayClient,
		netPoller:  netPoller,
		pluginSvc:  pluginSvc,
		geocheck:   NewGeocheckService(),
	}
}

// getUserOnlineStatus
type GetUserOnlineStatusRequest struct {
	Username string `json:"username"`
}

func (s *StatsService) HandleGetUserOnlineStatus(w http.ResponseWriter, r *http.Request) {
	var req GetUserOnlineStatusRequest
	_ = json.NewDecoder(r.Body).Decode(&req)

	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	online, err := s.xrayClient.GetUserOnlineStatus(ctx, req.Username)
	if err != nil {
		online = false
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"response": map[string]interface{}{
			"isOnline": online,
		},
	})
}

// getSystemStats
func (s *StatsService) HandleGetSystemStats(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	sysStats, err := s.xrayClient.GetSysStats(ctx)
	var xrayInfo interface{} = nil
	if err == nil && sysStats != nil {
		xrayInfo = map[string]interface{}{
			"numGoroutine": sysStats.NumGoroutine,
			"numGC":        sysStats.NumGC,
			"alloc":        sysStats.Alloc,
			"totalAlloc":   sysStats.TotalAlloc,
			"sys":          sysStats.Sys,
			"mallocs":      sysStats.Mallocs,
			"frees":        sysStats.Frees,
			"liveObjects":  sysStats.LiveObjects,
			"pauseTotalNs": sysStats.PauseTotalNs,
			"uptime":       sysStats.Uptime,
		}
	}

	reportsCount := s.pluginSvc.TorrentBlocker.ReportsCount()
	systemCombined := GetSystemCombined(s.netPoller)

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"response": map[string]interface{}{
			"xrayInfo": xrayInfo,
			"plugins": map[string]interface{}{
				"torrentBlocker": map[string]interface{}{
					"reportsCount": reportsCount,
				},
			},
			"system": systemCombined,
		},
	})
}

// getUsersStats
type ResetRequest struct {
	Reset bool `json:"reset"`
}

func (s *StatsService) HandleGetUsersStats(w http.ResponseWriter, r *http.Request) {
	var req ResetRequest
	_ = json.NewDecoder(r.Body).Decode(&req)

	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()

	users, err := s.xrayClient.GetAllUsersStats(ctx, req.Reset)
	if err != nil || users == nil {
		users = []xray.UserTraffic{}
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"response": map[string]interface{}{
			"users": users,
		},
	})
}

// getInboundStats
type InboundStatsRequest struct {
	Tag   string `json:"tag"`
	Reset bool   `json:"reset"`
}

func (s *StatsService) HandleGetInboundStats(w http.ResponseWriter, r *http.Request) {
	var req InboundStatsRequest
	_ = json.NewDecoder(r.Body).Decode(&req)

	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	ib, err := s.xrayClient.GetInboundStats(ctx, req.Tag, req.Reset)
	if err != nil || ib == nil {
		ib = &xray.InboundTraffic{Inbound: req.Tag}
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"response": map[string]interface{}{
			"inbound":  ib.Inbound,
			"downlink": ib.Downlink,
			"uplink":   ib.Uplink,
		},
	})
}

// getOutboundStats
type OutboundStatsRequest struct {
	Tag   string `json:"tag"`
	Reset bool   `json:"reset"`
}

func (s *StatsService) HandleGetOutboundStats(w http.ResponseWriter, r *http.Request) {
	var req OutboundStatsRequest
	_ = json.NewDecoder(r.Body).Decode(&req)

	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	ob, err := s.xrayClient.GetOutboundStats(ctx, req.Tag, req.Reset)
	if err != nil || ob == nil {
		ob = &xray.OutboundTraffic{Outbound: req.Tag}
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"response": map[string]interface{}{
			"outbound": ob.Outbound,
			"downlink": ob.Downlink,
			"uplink":   ob.Uplink,
		},
	})
}

// getAllInboundsStats
func (s *StatsService) HandleGetAllInboundsStats(w http.ResponseWriter, r *http.Request) {
	var req ResetRequest
	_ = json.NewDecoder(r.Body).Decode(&req)

	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	inbounds, err := s.xrayClient.GetAllInboundsStats(ctx, req.Reset)
	if err != nil || inbounds == nil {
		inbounds = []xray.InboundTraffic{}
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"response": map[string]interface{}{
			"inbounds": inbounds,
		},
	})
}

// getAllOutboundsStats
func (s *StatsService) HandleGetAllOutboundsStats(w http.ResponseWriter, r *http.Request) {
	var req ResetRequest
	_ = json.NewDecoder(r.Body).Decode(&req)

	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	outbounds, err := s.xrayClient.GetAllOutboundsStats(ctx, req.Reset)
	if err != nil || outbounds == nil {
		outbounds = []xray.OutboundTraffic{}
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"response": map[string]interface{}{
			"outbounds": outbounds,
		},
	})
}

// getCombinedStats
func (s *StatsService) HandleGetCombinedStats(w http.ResponseWriter, r *http.Request) {
	var req ResetRequest
	_ = json.NewDecoder(r.Body).Decode(&req)

	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	inbounds, _ := s.xrayClient.GetAllInboundsStats(ctx, req.Reset)
	outbounds, _ := s.xrayClient.GetAllOutboundsStats(ctx, req.Reset)
	if inbounds == nil {
		inbounds = []xray.InboundTraffic{}
	}
	if outbounds == nil {
		outbounds = []xray.OutboundTraffic{}
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"response": map[string]interface{}{
			"inbounds":  inbounds,
			"outbounds": outbounds,
		},
	})
}

// getUserIPList
type UserIPListRequest struct {
	UserID string `json:"userId"`
}

func (s *StatsService) HandleGetUserIPList(w http.ResponseWriter, r *http.Request) {
	var req UserIPListRequest
	_ = json.NewDecoder(r.Body).Decode(&req)

	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	ips, err := s.xrayClient.GetStatsOnlineIPList(ctx, req.UserID, true)
	if err != nil || ips == nil {
		ips = []xray.UserIPSeen{}
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"response": map[string]interface{}{
			"ips": ips,
		},
	})
}

type UserIPListResponseItem struct {
	UserID interface{}       `json:"userId"`
	IPs    []xray.UserIPSeen `json:"ips"`
}

// getUsersIPList
func (s *StatsService) HandleGetUsersIPList(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()

	users, err := s.xrayClient.GetAllOnlineUsers(ctx)
	if err != nil || len(users) == 0 {
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"response": map[string]interface{}{
				"users": []UserIPListResponseItem{},
			},
		})
		return
	}

	onlineUserIDs := make(map[string]bool)
	for _, raw := range users {
		parts := strings.Split(raw, ">>>")
		if len(parts) >= 2 {
			onlineUserIDs[parts[1]] = true
		} else {
			onlineUserIDs[raw] = true
		}
	}

	var result []UserIPListResponseItem
	for uidStr := range onlineUserIDs {
		ips, err := s.xrayClient.GetStatsOnlineIPList(ctx, uidStr, true)
		if err == nil && len(ips) > 0 {
			var uidVal interface{} = uidStr
			if idInt, err := strconv.Atoi(uidStr); err == nil {
				uidVal = idInt
			}
			result = append(result, UserIPListResponseItem{
				UserID: uidVal,
				IPs:    ips,
			})
		}
	}

	if result == nil {
		result = []UserIPListResponseItem{}
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"response": map[string]interface{}{
			"users": result,
		},
	})
}

// getGeocheck
func (s *StatsService) HandleGetGeocheck(w http.ResponseWriter, r *http.Request) {
	var req GeocheckRequest
	_ = json.NewDecoder(r.Body).Decode(&req)

	report, err := s.geocheck.Run(req)
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"error": err.Error(),
		})
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"response": report,
	})
}

func writeJSON(w http.ResponseWriter, status int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(data)
}
