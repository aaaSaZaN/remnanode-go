package handler

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"time"

	"github.com/remnawave/node-go/internal/plugin"
	"github.com/remnawave/node-go/internal/xray"
)

type Service struct {
	xrayClient *xray.Client
	state      *xray.StateManager
	plugin     *plugin.Service
}

func NewService(xrayClient *xray.Client, state *xray.StateManager, pluginSvc *plugin.Service) *Service {
	return &Service{
		xrayClient: xrayClient,
		state:      state,
		plugin:     pluginSvc,
	}
}

type InboundUserData struct {
	Type       string `json:"type"` // trojan, vless, shadowsocks, shadowsocks22, hysteria
	Tag        string `json:"tag"`
	Username   string `json:"username"`
	Password   string `json:"password"`
	UUID       string `json:"uuid"`
	Flow       string `json:"flow"`
	CipherType int32  `json:"cipherType"`
}

type AddUserRequest struct {
	Data     []InboundUserData `json:"data"`
	HashData struct {
		VlessUUID     string  `json:"vlessUuid"`
		PrevVlessUUID *string `json:"prevVlessUuid"`
	} `json:"hashData"`
}

type GenericSuccessResponse struct {
	Success bool    `json:"success"`
	Error   *string `json:"error"`
}

func (s *Service) HandleAddUser(w http.ResponseWriter, r *http.Request) {
	var req AddUserRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || len(req.Data) == 0 {
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"response": GenericSuccessResponse{Success: false, Error: ptr("Invalid request body")},
		})
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()

	userID := req.Data[0].Username
	for _, item := range req.Data {
		s.state.AddXtlsConfigInbound(item.Tag)
	}

	var userIPs []string
	if req.HashData.PrevVlessUUID != nil {
		if ips, err := s.xrayClient.GetStatsOnlineIPList(ctx, userID, true); err == nil {
			for _, ipSeen := range ips {
				if !s.plugin.IsWhitelisted(ipSeen.IP) {
					userIPs = append(userIPs, ipSeen.IP)
				}
			}
		}
	}

	// remove old user from inbounds
	for _, tag := range s.state.GetXtlsConfigInbounds() {
		_ = s.xrayClient.RemoveUser(ctx, tag, userID)
		if req.HashData.PrevVlessUUID != nil {
			s.state.RemoveUserFromInbound(tag, *req.HashData.PrevVlessUUID)
		} else {
			s.state.RemoveUserFromInbound(tag, req.HashData.VlessUUID)
		}
	}

	if len(userIPs) > 0 {
		plugin.KillSockets(userIPs)
	}

	// add user to each requested inbound
	var lastErr error
	successCount := 0
	for _, item := range req.Data {
		username := item.Username
		if username == "" {
			username = userID
		}
		uuidVal := item.UUID
		if uuidVal == "" {
			uuidVal = req.HashData.VlessUUID
		}

		err := s.xrayClient.AddUser(ctx, xray.UserConfig{
			Type:       item.Type,
			Tag:        item.Tag,
			Username:   username,
			UUID:       uuidVal,
			Password:   item.Password,
			Flow:       item.Flow,
			CipherType: item.CipherType,
		})

		if err == nil {
			s.state.AddUserToInbound(item.Tag, req.HashData.VlessUUID)
			successCount++
		} else {
			lastErr = err
			log.Printf("[HANDLER] Failed to add user %s to inbound %s: %v", username, item.Tag, err)
		}
	}

	if successCount == 0 && lastErr != nil {
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"response": GenericSuccessResponse{Success: false, Error: ptr(lastErr.Error())},
		})
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"response": GenericSuccessResponse{Success: true, Error: nil},
	})
}

type RemoveUserRequest struct {
	Username string `json:"username"`
	HashData struct {
		VlessUUID string `json:"vlessUuid"`
	} `json:"hashData"`
}

func (s *Service) HandleRemoveUser(w http.ResponseWriter, r *http.Request) {
	var req RemoveUserRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"response": GenericSuccessResponse{Success: false, Error: ptr("Invalid request body")},
		})
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()

	var userIPs []string
	if ips, err := s.xrayClient.GetStatsOnlineIPList(ctx, req.Username, true); err == nil {
		for _, ipSeen := range ips {
			if !s.plugin.IsWhitelisted(ipSeen.IP) {
				userIPs = append(userIPs, ipSeen.IP)
			}
		}
	}

	for _, tag := range s.state.GetXtlsConfigInbounds() {
		_ = s.xrayClient.RemoveUser(ctx, tag, req.Username)
		s.state.RemoveUserFromInbound(tag, req.HashData.VlessUUID)
	}

	if len(userIPs) > 0 {
		plugin.KillSockets(userIPs)
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"response": GenericSuccessResponse{Success: true, Error: nil},
	})
}

type AddUsersRequest struct {
	AffectedInboundTags []string `json:"affectedInboundTags"`
	Users               []struct {
		InboundData []struct {
			Type string `json:"type"`
			Tag  string `json:"tag"`
			Flow string `json:"flow"`
		} `json:"inboundData"`
		UserData struct {
			UserID         string `json:"userId"`
			HashUUID       string `json:"hashUuid"`
			VlessUUID      string `json:"vlessUuid"`
			TrojanPassword string `json:"trojanPassword"`
			SSPassword     string `json:"ssPassword"`
		} `json:"userData"`
	} `json:"users"`
}

func (s *Service) HandleAddUsers(w http.ResponseWriter, r *http.Request) {
	var req AddUsersRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"response": GenericSuccessResponse{Success: false, Error: ptr("Invalid request body")},
		})
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
	defer cancel()

	for _, tag := range req.AffectedInboundTags {
		s.state.AddXtlsConfigInbound(tag)
	}

	for _, u := range req.Users {
		// remove from old inbounds
		for _, tag := range s.state.GetXtlsConfigInbounds() {
			_ = s.xrayClient.RemoveUser(ctx, tag, u.UserData.UserID)
			s.state.RemoveUserFromInbound(tag, u.UserData.HashUUID)
		}

		for _, item := range u.InboundData {
			userCfg := xray.UserConfig{
				Type:     item.Type,
				Tag:      item.Tag,
				Username: u.UserData.UserID,
				UUID:     u.UserData.VlessUUID,
				Flow:     item.Flow,
			}
			switch item.Type {
			case "trojan":
				userCfg.Password = u.UserData.TrojanPassword
			case "shadowsocks", "shadowsocks22":
				userCfg.Password = u.UserData.SSPassword
			case "hysteria":
				userCfg.Password = u.UserData.VlessUUID
			}

			if err := s.xrayClient.AddUser(ctx, userCfg); err == nil {
				s.state.AddUserToInbound(item.Tag, u.UserData.VlessUUID)
			}
		}
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"response": GenericSuccessResponse{Success: true, Error: nil},
	})
}

type RemoveUsersRequest struct {
	Users []struct {
		UserID   string `json:"userId"`
		HashUUID string `json:"hashUuid"`
	} `json:"users"`
}

func (s *Service) HandleRemoveUsers(w http.ResponseWriter, r *http.Request) {
	var req RemoveUsersRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"response": GenericSuccessResponse{Success: false, Error: ptr("Invalid request body")},
		})
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
	defer cancel()

	var allIPs []string
	for _, u := range req.Users {
		if ips, err := s.xrayClient.GetStatsOnlineIPList(ctx, u.UserID, true); err == nil {
			for _, ipSeen := range ips {
				if !s.plugin.IsWhitelisted(ipSeen.IP) {
					allIPs = append(allIPs, ipSeen.IP)
				}
			}
		}

		for _, tag := range s.state.GetXtlsConfigInbounds() {
			_ = s.xrayClient.RemoveUser(ctx, tag, u.UserID)
			s.state.RemoveUserFromInbound(tag, u.HashUUID)
		}
	}

	if len(allIPs) > 0 {
		plugin.KillSockets(allIPs)
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"response": GenericSuccessResponse{Success: true, Error: nil},
	})
}

type DropUsersConnectionsRequest struct {
	UserIDs []string `json:"userIds"`
}

func (s *Service) HandleDropUsersConnections(w http.ResponseWriter, r *http.Request) {
	var req DropUsersConnectionsRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"response": map[string]interface{}{"success": false},
		})
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()

	var allIPs []string
	for _, uid := range req.UserIDs {
		if ips, err := s.xrayClient.GetStatsOnlineIPList(ctx, uid, true); err == nil {
			for _, ipSeen := range ips {
				if !s.plugin.IsWhitelisted(ipSeen.IP) {
					allIPs = append(allIPs, ipSeen.IP)
				}
			}
		}
	}

	if len(allIPs) > 0 {
		plugin.KillSockets(allIPs)
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"response": map[string]interface{}{"success": true},
	})
}

type DropIPsRequest struct {
	IPs []string `json:"ips"`
}

func (s *Service) HandleDropIPs(w http.ResponseWriter, r *http.Request) {
	var req DropIPsRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"response": map[string]interface{}{"success": false},
		})
		return
	}

	plugin.KillSockets(req.IPs)

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"response": map[string]interface{}{"success": true},
	})
}

func ptr(s string) *string {
	return &s
}

func writeJSON(w http.ResponseWriter, status int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(data)
}
