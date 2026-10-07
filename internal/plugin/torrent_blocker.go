package plugin

import (
	"bytes"
	"context"
	"encoding/json"
	"log"
	"net/http"
	"sync"
	"time"
)

type XrayWebhookModel struct {
	Email          *string `json:"email"`
	Level          *int    `json:"level"`
	Protocol       *string `json:"protocol"`
	Network        string  `json:"network"`
	Source         *string `json:"source"`
	Destination    string  `json:"destination"`
	RouteTarget    *string `json:"routeTarget"`
	OriginalTarget *string `json:"originalTarget"`
	InboundTag     *string `json:"inboundTag"`
	InboundName    *string `json:"inboundName"`
	InboundLocal   *string `json:"inboundLocal"`
	OutboundTag    *string `json:"outboundTag"`
	TS             int64   `json:"ts"`
}

type ActionReport struct {
	Blocked       bool      `json:"blocked"`
	IP            string    `json:"ip"`
	BlockDuration int       `json:"blockDuration"`
	WillUnblockAt time.Time `json:"willUnblockAt"`
	UserID        string    `json:"userId"`
	ProcessedAt   time.Time `json:"processedAt"`
}

type TorrentBlockerReport struct {
	ActionReport ActionReport     `json:"actionReport"`
	XrayReport   XrayWebhookModel `json:"xrayReport"`
}

type TorrentBlockerState struct {
	mu              sync.RWMutex
	enabled         bool
	blockDuration   int
	rulePlacement   int
	webhookURL      string
	includeRuleTags []string
	ignoredIPs      map[string]struct{}
	ignoredUsers    map[string]struct{}
	reports         []TorrentBlockerReport
}

func NewTorrentBlockerState() *TorrentBlockerState {
	return &TorrentBlockerState{
		ignoredIPs:   make(map[string]struct{}),
		ignoredUsers: make(map[string]struct{}),
	}
}

func (tb *TorrentBlockerState) Configure(enabled bool, duration int, placement int, webhook string, tags []string, ignoredIPs, ignoredUsers []string) {
	tb.mu.Lock()
	defer tb.mu.Unlock()

	tb.enabled = enabled
	tb.blockDuration = duration
	tb.rulePlacement = placement
	tb.webhookURL = webhook
	tb.includeRuleTags = tags

	tb.ignoredIPs = make(map[string]struct{})
	for _, ip := range ignoredIPs {
		tb.ignoredIPs[ip] = struct{}{}
	}

	tb.ignoredUsers = make(map[string]struct{})
	for _, u := range ignoredUsers {
		tb.ignoredUsers[u] = struct{}{}
	}
}

func (tb *TorrentBlockerState) IsEnabled() bool {
	tb.mu.RLock()
	defer tb.mu.RUnlock()
	return tb.enabled
}

func (tb *TorrentBlockerState) BlockDuration() int {
	tb.mu.RLock()
	defer tb.mu.RUnlock()
	return tb.blockDuration
}

func (tb *TorrentBlockerState) RulePlacement() int {
	tb.mu.RLock()
	defer tb.mu.RUnlock()
	return tb.rulePlacement
}

func (tb *TorrentBlockerState) WebhookURL() string {
	tb.mu.RLock()
	defer tb.mu.RUnlock()
	return tb.webhookURL
}

func (tb *TorrentBlockerState) IncludeRuleTags() []string {
	tb.mu.RLock()
	defer tb.mu.RUnlock()
	return tb.includeRuleTags
}

func (tb *TorrentBlockerState) IsIPIgnored(ip string) bool {
	tb.mu.RLock()
	defer tb.mu.RUnlock()
	_, exists := tb.ignoredIPs[ip]
	return exists
}

func (tb *TorrentBlockerState) IsUserIgnored(user string) bool {
	tb.mu.RLock()
	defer tb.mu.RUnlock()
	_, exists := tb.ignoredUsers[user]
	return exists
}

func (tb *TorrentBlockerState) AddReport(r TorrentBlockerReport) {
	tb.mu.Lock()
	tb.reports = append(tb.reports, r)
	webhookURL := tb.webhookURL
	tb.mu.Unlock()

	if webhookURL != "" {
		go sendExternalWebhook(webhookURL, r)
	}
}

func (tb *TorrentBlockerState) FlushReports() []TorrentBlockerReport {
	tb.mu.Lock()
	defer tb.mu.Unlock()
	res := tb.reports
	tb.reports = nil
	if res == nil {
		res = []TorrentBlockerReport{}
	}
	return res
}

func (tb *TorrentBlockerState) ReportsCount() int {
	tb.mu.RLock()
	defer tb.mu.RUnlock()
	return len(tb.reports)
}

func (tb *TorrentBlockerState) Reset() {
	tb.mu.Lock()
	defer tb.mu.Unlock()
	tb.enabled = false
	tb.blockDuration = 0
	tb.rulePlacement = 0
	tb.webhookURL = ""
	tb.includeRuleTags = nil
	tb.ignoredIPs = make(map[string]struct{})
	tb.ignoredUsers = make(map[string]struct{})
	tb.reports = nil
}

func sendExternalWebhook(url string, report TorrentBlockerReport) {
	data, err := json.Marshal(report)
	if err != nil {
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(data))
	if err != nil {
		return
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		log.Printf("[TORRENT-BLOCKER] Failed to send external webhook: %v", err)
		return
	}
	_ = resp.Body.Close()
}
