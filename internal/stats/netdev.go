package stats

import (
	"bufio"
	"net"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

type NetworkInterfaceRate struct {
	Interface     string  `json:"interface"`
	RxBytesPerSec float64 `json:"rxBytesPerSec"`
	TxBytesPerSec float64 `json:"txBytesPerSec"`
	RxTotal       int64   `json:"rxTotal"`
	TxTotal       int64   `json:"txTotal"`
}

type interfaceRawStats struct {
	rxBytes   int64
	txBytes   int64
	timestamp int64
}

type NetworkPoller struct {
	mu           sync.RWMutex
	prevStats    map[string]interfaceRawStats
	currentRates map[string]NetworkInterfaceRate
	defaultIface string
	available    bool
}

func NewNetworkPoller() *NetworkPoller {
	np := &NetworkPoller{
		prevStats:    make(map[string]interfaceRawStats),
		currentRates: make(map[string]NetworkInterfaceRate),
	}

	if _, err := os.Stat("/proc/net/dev"); err == nil {
		np.available = true
		np.defaultIface = resolveDefaultInterface()
		np.prevStats = readProcNetDev()
		go np.pollLoop()
	} else {
		// non-linux fallback
		ifaces, _ := net.Interfaces()
		for _, iface := range ifaces {
			np.currentRates[iface.Name] = NetworkInterfaceRate{
				Interface: iface.Name,
			}
		}
	}

	return np
}

func (np *NetworkPoller) pollLoop() {
	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

	for range ticker.C {
		now := readProcNetDev()
		np.mu.Lock()
		for iface, current := range now {
			prev, ok := np.prevStats[iface]
			if !ok {
				continue
			}
			elapsed := float64(current.timestamp-prev.timestamp) / 1000.0
			if elapsed <= 0 {
				continue
			}

			rxRate := float64(current.rxBytes-prev.rxBytes) / elapsed
			txRate := float64(current.txBytes-prev.txBytes) / elapsed
			if rxRate < 0 {
				rxRate = 0
			}
			if txRate < 0 {
				txRate = 0
			}

			np.currentRates[iface] = NetworkInterfaceRate{
				Interface:     iface,
				RxBytesPerSec: rxRate,
				TxBytesPerSec: txRate,
				RxTotal:       current.rxBytes,
				TxTotal:       current.txBytes,
			}
		}
		np.prevStats = now
		np.mu.Unlock()
	}
}

func (np *NetworkPoller) GetDefault() *NetworkInterfaceRate {
	np.mu.RLock()
	defer np.mu.RUnlock()

	if np.defaultIface != "" {
		if rate, ok := np.currentRates[np.defaultIface]; ok {
			return &rate
		}
	}

	// fallback to first non-loopback interface
	for iface, rate := range np.currentRates {
		if iface != "lo" {
			return &rate
		}
	}
	return nil
}

func readProcNetDev() map[string]interfaceRawStats {
	result := make(map[string]interfaceRawStats)
	nowMs := time.Now().UnixMilli()

	f, err := os.Open("/proc/net/dev")
	if err != nil {
		return result
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	lineIdx := 0
	for scanner.Scan() {
		lineIdx++
		if lineIdx <= 2 {
			continue // skip header
		}
		line := scanner.Text()
		colonIdx := strings.Index(line, ":")
		if colonIdx == -1 {
			continue
		}

		iface := strings.TrimSpace(line[:colonIdx])
		if iface == "" {
			continue
		}

		fields := strings.Fields(line[colonIdx+1:])
		if len(fields) < 16 {
			continue
		}

		rxBytes, err1 := strconv.ParseInt(fields[0], 10, 64)
		txBytes, err2 := strconv.ParseInt(fields[8], 10, 64)
		if err1 != nil || err2 != nil {
			continue
		}

		result[iface] = interfaceRawStats{
			rxBytes:   rxBytes,
			txBytes:   txBytes,
			timestamp: nowMs,
		}
	}
	if err := scanner.Err(); err != nil {
		return result
	}

	return result
}

func resolveDefaultInterface() string {
	f, err := os.Open("/proc/net/route")
	if err != nil {
		return ""
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	lineIdx := 0
	type candidate struct {
		iface  string
		metric int
	}
	var best *candidate

	for scanner.Scan() {
		lineIdx++
		if lineIdx == 1 {
			continue
		}
		fields := strings.Fields(scanner.Text())
		if len(fields) < 11 {
			continue
		}
		iface := fields[0]
		destination := fields[1]
		metricStr := fields[6]

		if destination != "00000000" {
			continue
		}

		metric, _ := strconv.Atoi(metricStr)
		if best == nil || metric < best.metric {
			best = &candidate{iface: iface, metric: metric}
		}
	}
	if err := scanner.Err(); err != nil {
		return ""
	}

	if best != nil {
		return best.iface
	}
	return ""
}
