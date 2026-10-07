package plugin

import (
	"context"
	"fmt"
	"log"
	"net"
	"os/exec"
	"strings"
	"sync"
	"time"
)

const (
	TableName               = "remnanode"
	TorrentBlockerSetName   = "torrent-blocker"
	IngressFilterIPSetName  = "ingress-filter-ip"
	EgressFilterIPSetName   = "egress-filter-ip"
	EgressFilterPortSetName = "egress-filter-port"
)

type NftManager struct {
	mu                 sync.Mutex
	available          bool
	logging            bool
	acceptReplyTraffic bool
}

func NewNftManager(logging, acceptReplyTraffic bool) *NftManager {
	_, err := exec.LookPath("nft")
	available := err == nil

	mgr := &NftManager{
		available:          available,
		logging:            logging,
		acceptReplyTraffic: acceptReplyTraffic,
	}

	if available {
		if err := mgr.RecreateTables(); err != nil {
			log.Printf("[PLUGIN] Warning: Nftables table initialization failed: %v", err)
		} else {
			log.Println("[PLUGIN] NftManager initialized successfully")
		}
	} else {
		log.Println("[PLUGIN] nft command not available - nftables features disabled")
	}

	return mgr
}

func (m *NftManager) IsAvailable() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.available
}

func (m *NftManager) RecreateTables() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if !m.available {
		return nil
	}

	// delete existing table
	_ = exec.Command("nft", "delete", "table", "inet", TableName).Run()

	commands := []string{
		fmt.Sprintf("add table inet %s", TableName),
		// sets
		fmt.Sprintf("add set inet %s %s { type ipv4_addr; flags timeout; }", TableName, TorrentBlockerSetName),
		fmt.Sprintf("add set inet %s %s-v6 { type ipv6_addr; flags timeout; }", TableName, TorrentBlockerSetName),
		fmt.Sprintf("add set inet %s %s { type ipv4_addr; flags interval; }", TableName, IngressFilterIPSetName),
		fmt.Sprintf("add set inet %s %s-v6 { type ipv6_addr; flags interval; }", TableName, IngressFilterIPSetName),
		fmt.Sprintf("add set inet %s %s { type ipv4_addr; flags interval; }", TableName, EgressFilterIPSetName),
		fmt.Sprintf("add set inet %s %s-v6 { type ipv6_addr; flags interval; }", TableName, EgressFilterIPSetName),
		fmt.Sprintf("add set inet %s %s { type inet_service; }", TableName, EgressFilterPortSetName),

		// ingress chain
		fmt.Sprintf("add chain inet %s prerouting { type filter hook prerouting priority -150; policy accept; }", TableName),
	}

	if m.acceptReplyTraffic {
		commands = append(commands, fmt.Sprintf("add rule inet %s prerouting ct state established,related accept", TableName))
	}

	commands = append(commands,
		fmt.Sprintf("add rule inet %s prerouting ip saddr @%s drop", TableName, IngressFilterIPSetName),
		fmt.Sprintf("add rule inet %s prerouting ip6 saddr @%s-v6 drop", TableName, IngressFilterIPSetName),
		fmt.Sprintf("add rule inet %s prerouting ip saddr @%s drop", TableName, TorrentBlockerSetName),
		fmt.Sprintf("add rule inet %s prerouting ip6 saddr @%s-v6 drop", TableName, TorrentBlockerSetName),

		// egress chain
		fmt.Sprintf("add chain inet %s output { type filter hook output priority 0; policy accept; }", TableName),
		fmt.Sprintf("add rule inet %s output ip daddr @%s drop", TableName, EgressFilterIPSetName),
		fmt.Sprintf("add rule inet %s output ip6 daddr @%s-v6 drop", TableName, EgressFilterIPSetName),
		fmt.Sprintf("add rule inet %s output th dport @%s drop", TableName, EgressFilterPortSetName),
	)

	script := strings.Join(commands, "\n")
	cmd := exec.Command("nft", "-f", "-")
	cmd.Stdin = strings.NewReader(script)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("nft command failed: %w (output: %s)", err, string(out))
	}
	return nil
}

func (m *NftManager) DeleteTable() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if !m.available {
		return nil
	}

	return exec.Command("nft", "delete", "table", "inet", TableName).Run()
}

func isIPv6(ipStr string) bool {
	ip := net.ParseIP(strings.Split(ipStr, "/")[0])
	return ip != nil && ip.To4() == nil
}

func (m *NftManager) BlockIP(ip string, timeoutSeconds int) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if !m.available {
		return nil
	}

	setName := TorrentBlockerSetName
	if isIPv6(ip) {
		setName += "-v6"
	}

	var element string
	if timeoutSeconds > 0 {
		element = fmt.Sprintf("{ %s timeout %ds }", ip, timeoutSeconds)
	} else {
		element = fmt.Sprintf("{ %s }", ip)
	}

	cmd := exec.Command("nft", "add", "element", "inet", TableName, setName, element)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("nft add element failed: %w (output: %s)", err, string(out))
	}
	return nil
}

func (m *NftManager) UnblockIPs(ips []string, set string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if !m.available || len(ips) == 0 {
		return nil
	}

	var v4, v6 []string
	for _, ip := range ips {
		if isIPv6(ip) {
			v6 = append(v6, ip)
		} else {
			v4 = append(v4, ip)
		}
	}

	if len(v4) > 0 {
		elem := "{ " + strings.Join(v4, ", ") + " }"
		_ = exec.Command("nft", "delete", "element", "inet", TableName, set, elem).Run()
	}
	if len(v6) > 0 {
		elem := "{ " + strings.Join(v6, ", ") + " }"
		_ = exec.Command("nft", "delete", "element", "inet", TableName, set+"-v6", elem).Run()
	}
	return nil
}

func (m *NftManager) AddAddresses(ips []string, set string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if !m.available || len(ips) == 0 {
		return nil
	}

	var v4, v6 []string
	for _, ip := range ips {
		if isIPv6(ip) {
			v6 = append(v6, ip)
		} else {
			v4 = append(v4, ip)
		}
	}

	if len(v4) > 0 {
		elem := "{ " + strings.Join(v4, ", ") + " }"
		_ = exec.Command("nft", "add", "element", "inet", TableName, set, elem).Run()
	}
	if len(v6) > 0 {
		elem := "{ " + strings.Join(v6, ", ") + " }"
		_ = exec.Command("nft", "add", "element", "inet", TableName, set+"-v6", elem).Run()
	}
	return nil
}

func (m *NftManager) AddPorts(ports []int, set string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if !m.available || len(ports) == 0 {
		return nil
	}

	var portStrs []string
	for _, p := range ports {
		portStrs = append(portStrs, fmt.Sprintf("%d", p))
	}

	elem := "{ " + strings.Join(portStrs, ", ") + " }"
	return exec.Command("nft", "add", "element", "inet", TableName, set, elem).Run()
}

// killSockets drops active tcp connections for given ips using ss -K
func KillSockets(ips []string) {
	if len(ips) == 0 {
		return
	}

	for _, ip := range ips {
		ip = strings.TrimSpace(ip)
		if ip == "" {
			continue
		}
		// run ss -K dst <ip> and ss -K src <ip>
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		_ = exec.CommandContext(ctx, "ss", "-K", "dst", ip).Run()
		_ = exec.CommandContext(ctx, "ss", "-K", "src", ip).Run()
		cancel()
	}
}
