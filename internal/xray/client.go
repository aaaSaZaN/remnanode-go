package xray

import (
	"encoding/json"
	"os/exec"
	"context"
	"fmt"
	"net"
	"strings"
	"sync"
	"time"

	proxymancommand "github.com/xtls/xray-core/app/proxyman/command"
	statscommand "github.com/xtls/xray-core/app/stats/command"
	"github.com/xtls/xray-core/common/protocol"
	"github.com/xtls/xray-core/common/serial"
	"github.com/xtls/xray-core/proxy/shadowsocks"
	"github.com/xtls/xray-core/proxy/shadowsocks_2022"
	"github.com/xtls/xray-core/proxy/trojan"
	"github.com/xtls/xray-core/proxy/vless"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
)

type Client struct {
	mu            sync.RWMutex
	conn          *grpc.ClientConn
	handlerClient proxymancommand.HandlerServiceClient
	statsClient   statscommand.StatsServiceClient
	socketPath    string
}

func NewClient(socketPath string) *Client {
	return &Client{
		socketPath: socketPath,
	}
}

func (c *Client) connect(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.conn != nil {
		return nil
	}

	dialAddr := c.socketPath
	if dialAddr == "" || !strings.Contains(dialAddr, ":") {
		dialAddr = "127.0.0.1:62085"
	}

	var conn *grpc.ClientConn
	var err error

	if strings.Contains(dialAddr, ":") {
		conn, err = grpc.DialContext(
			ctx,
			dialAddr,
			grpc.WithTransportCredentials(insecure.NewCredentials()),
			grpc.WithDefaultCallOptions(grpc.MaxCallRecvMsgSize(100*1024*1024)),
		)
	} else {
		dialer := func(ctx context.Context, _ string) (net.Conn, error) {
			d := net.Dialer{Timeout: 5 * time.Second}
			conn, err := d.DialContext(ctx, "unix", "@"+c.socketPath)
			if err == nil {
				return conn, nil
			}
			return d.DialContext(ctx, "unix", c.socketPath)
		}
		conn, err = grpc.DialContext(
			ctx,
			"passthrough:///unix",
			grpc.WithContextDialer(dialer),
			grpc.WithTransportCredentials(insecure.NewCredentials()),
			grpc.WithDefaultCallOptions(grpc.MaxCallRecvMsgSize(100*1024*1024)),
		)
	}

	if err != nil {
		return fmt.Errorf("failed to dial xray gRPC on %s: %w", dialAddr, err)
	}

	c.conn = conn
	c.handlerClient = proxymancommand.NewHandlerServiceClient(conn)
	c.statsClient = statscommand.NewStatsServiceClient(conn)
	return nil
}

func (c *Client) getClients() (proxymancommand.HandlerServiceClient, statscommand.StatsServiceClient, *grpc.ClientConn, error) {
	c.mu.RLock()
	if c.conn != nil {
		defer c.mu.RUnlock()
		return c.handlerClient, c.statsClient, c.conn, nil
	}
	c.mu.RUnlock()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := c.connect(ctx); err != nil {
		return nil, nil, nil, err
	}

	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.handlerClient, c.statsClient, c.conn, nil
}

func (c *Client) Close() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.conn != nil {
		_ = c.conn.Close()
		c.conn = nil
		c.handlerClient = nil
		c.statsClient = nil
	}
}

// userConfig describes a user to add to an inbound
type UserConfig struct {
	Type       string // vless, trojan, shadowsocks, shadowsocks22, hysteria
	Tag        string
	Username   string
	UUID       string
	Password   string
	Flow       string
	CipherType int32
}

func (c *Client) AddUser(ctx context.Context, u UserConfig) error {
	handler, _, _, err := c.getClients()
	if err != nil {
		return err
	}

	var account proto.Message
	switch u.Type {
	case "vless":
		account = &vless.Account{
			Id:   u.UUID,
			Flow: u.Flow,
		}
	case "trojan":
		account = &trojan.Account{
			Password: u.Password,
		}
	case "shadowsocks":
		account = &shadowsocks.Account{
			Password:   u.Password,
			CipherType: shadowsocks.CipherType(u.CipherType),
		}
	case "shadowsocks22":
		account = &shadowsocks_2022.User{
			Key: u.Password,
		}
	case "hysteria":
		pw := u.Password
		if pw == "" {
			pw = u.UUID
		}
		buf := make([]byte, 0, len(pw)+2)
		buf = append(buf, 0x0a, byte(len(pw)))
		buf = append(buf, []byte(pw)...)
		typedAccount := &serial.TypedMessage{
			Type:  "xray.proxy.hysteria.account.Account",
			Value: buf,
		}
		user := &protocol.User{
			Level:   0,
			Email:   u.Username,
			Account: typedAccount,
		}
		addUserOp := &proxymancommand.AddUserOperation{
			User: user,
		}
		_, err = handler.AlterInbound(ctx, &proxymancommand.AlterInboundRequest{
			Tag:       u.Tag,
			Operation: serial.ToTypedMessage(addUserOp),
		})
		return err
	default:
		return fmt.Errorf("unsupported user type: %s", u.Type)
	}

	typedAccount := serial.ToTypedMessage(account)
	user := &protocol.User{
		Level:   0,
		Email:   u.Username,
		Account: typedAccount,
	}

	addUserOp := &proxymancommand.AddUserOperation{
		User: user,
	}

	_, err = handler.AlterInbound(ctx, &proxymancommand.AlterInboundRequest{
		Tag:       u.Tag,
		Operation: serial.ToTypedMessage(addUserOp),
	})
	return err
}

func (c *Client) RemoveUser(ctx context.Context, tag string, username string) error {
	handler, _, _, err := c.getClients()
	if err != nil {
		return err
	}

	removeUserOp := &proxymancommand.RemoveUserOperation{
		Email: username,
	}

	_, err = handler.AlterInbound(ctx, &proxymancommand.AlterInboundRequest{
		Tag:       tag,
		Operation: serial.ToTypedMessage(removeUserOp),
	})
	return err
}

func (c *Client) RemoveOutbound(ctx context.Context, tag string) error {
	handler, _, _, err := c.getClients()
	if err != nil {
		return err
	}

	_, err = handler.RemoveOutbound(ctx, &proxymancommand.RemoveOutboundRequest{
		Tag: tag,
	})
	return err
}

func (c *Client) GetSysStats(ctx context.Context) (*statscommand.SysStatsResponse, error) {
	_, stats, _, err := c.getClients()
	if err != nil {
		return nil, err
	}

	return stats.GetSysStats(ctx, &statscommand.SysStatsRequest{})
}

type UserTraffic struct {
	Username string `json:"username"`
	Downlink int64  `json:"downlink"`
	Uplink   int64  `json:"uplink"`
}

type InboundTraffic struct {
	Inbound  string `json:"inbound"`
	Downlink int64  `json:"downlink"`
	Uplink   int64  `json:"uplink"`
}

type OutboundTraffic struct {
	Outbound string `json:"outbound"`
	Downlink int64  `json:"downlink"`
	Uplink   int64  `json:"uplink"`
}

func (c *Client) GetAllUsersStats(ctx context.Context, reset bool) ([]UserTraffic, error) {
	_, stats, _, err := c.getClients()
	if err != nil {
		return nil, err
	}

	res, err := stats.QueryStats(ctx, &statscommand.QueryStatsRequest{
		Pattern: "user>>>",
		Reset_:  reset,
	})
	if err != nil {
		return nil, err
	}

	usersMap := make(map[string]*UserTraffic)
	for _, stat := range res.GetStat() {
		// format: user>>><username>>>>traffic>>><downlink|uplink>
		parts := strings.Split(stat.GetName(), ">>>")
		if len(parts) >= 4 && parts[0] == "user" && parts[2] == "traffic" {
			username := parts[1]
			kind := parts[3]
			u, exists := usersMap[username]
			if !exists {
				u = &UserTraffic{Username: username}
				usersMap[username] = u
			}
			if kind == "downlink" {
				u.Downlink += stat.GetValue()
			} else if kind == "uplink" {
				u.Uplink += stat.GetValue()
			}
		}
	}

	var result []UserTraffic
	for _, u := range usersMap {
		if u.Downlink != 0 || u.Uplink != 0 {
			result = append(result, *u)
		}
	}
	return result, nil
}

func (c *Client) GetInboundStats(ctx context.Context, tag string, reset bool) (*InboundTraffic, error) {
	_, stats, _, err := c.getClients()
	if err != nil {
		return nil, err
	}

	res, err := stats.QueryStats(ctx, &statscommand.QueryStatsRequest{
		Pattern: fmt.Sprintf("inbound>>>%s>>>traffic>>>", tag),
		Reset_:  reset,
	})
	if err != nil {
		return nil, err
	}

	inbound := &InboundTraffic{Inbound: tag}
	for _, s := range res.GetStat() {
		if strings.HasSuffix(s.GetName(), "downlink") {
			inbound.Downlink = s.GetValue()
		} else if strings.HasSuffix(s.GetName(), "uplink") {
			inbound.Uplink = s.GetValue()
		}
	}
	return inbound, nil
}

func (c *Client) GetOutboundStats(ctx context.Context, tag string, reset bool) (*OutboundTraffic, error) {
	_, stats, _, err := c.getClients()
	if err != nil {
		return nil, err
	}

	res, err := stats.QueryStats(ctx, &statscommand.QueryStatsRequest{
		Pattern: fmt.Sprintf("outbound>>>%s>>>traffic>>>", tag),
		Reset_:  reset,
	})
	if err != nil {
		return nil, err
	}

	outbound := &OutboundTraffic{Outbound: tag}
	for _, s := range res.GetStat() {
		if strings.HasSuffix(s.GetName(), "downlink") {
			outbound.Downlink = s.GetValue()
		} else if strings.HasSuffix(s.GetName(), "uplink") {
			outbound.Uplink = s.GetValue()
		}
	}
	return outbound, nil
}

func (c *Client) GetAllInboundsStats(ctx context.Context, reset bool) ([]InboundTraffic, error) {
	_, stats, _, err := c.getClients()
	if err != nil {
		return nil, err
	}

	res, err := stats.QueryStats(ctx, &statscommand.QueryStatsRequest{
		Pattern: "inbound>>>",
		Reset_:  reset,
	})
	if err != nil {
		return nil, err
	}

	inboundMap := make(map[string]*InboundTraffic)
	for _, stat := range res.GetStat() {
		parts := strings.Split(stat.GetName(), ">>>")
		if len(parts) >= 4 && parts[0] == "inbound" && parts[2] == "traffic" {
			tag := parts[1]
			kind := parts[3]
			ib, exists := inboundMap[tag]
			if !exists {
				ib = &InboundTraffic{Inbound: tag}
				inboundMap[tag] = ib
			}
			if kind == "downlink" {
				ib.Downlink += stat.GetValue()
			} else if kind == "uplink" {
				ib.Uplink += stat.GetValue()
			}
		}
	}

	var result []InboundTraffic
	for _, ib := range inboundMap {
		result = append(result, *ib)
	}
	return result, nil
}

func (c *Client) GetAllOutboundsStats(ctx context.Context, reset bool) ([]OutboundTraffic, error) {
	_, stats, _, err := c.getClients()
	if err != nil {
		return nil, err
	}

	res, err := stats.QueryStats(ctx, &statscommand.QueryStatsRequest{
		Pattern: "outbound>>>",
		Reset_:  reset,
	})
	if err != nil {
		return nil, err
	}

	outboundMap := make(map[string]*OutboundTraffic)
	for _, stat := range res.GetStat() {
		parts := strings.Split(stat.GetName(), ">>>")
		if len(parts) >= 4 && parts[0] == "outbound" && parts[2] == "traffic" {
			tag := parts[1]
			kind := parts[3]
			ob, exists := outboundMap[tag]
			if !exists {
				ob = &OutboundTraffic{Outbound: tag}
				outboundMap[tag] = ob
			}
			if kind == "downlink" {
				ob.Downlink += stat.GetValue()
			} else if kind == "uplink" {
				ob.Uplink += stat.GetValue()
			}
		}
	}

	var result []OutboundTraffic
	for _, ob := range outboundMap {
		result = append(result, *ob)
	}
	return result, nil
}

func (c *Client) GetUserOnlineStatus(ctx context.Context, username string) (bool, error) {
	_, stats, _, err := c.getClients()
	if err != nil {
		return false, err
	}

	res, err := stats.GetStats(ctx, &statscommand.GetStatsRequest{
		Name:   fmt.Sprintf("user>>>%s>>>online", username),
		Reset_: false,
	})
	if err != nil {
		if status.Code(err) == codes.NotFound {
			return false, nil
		}
		return false, err
	}

	return res.GetStat().GetValue() > 0, nil
}

type UserIPSeen struct {
	IP       string    `json:"ip"`
	LastSeen time.Time `json:"lastSeen"`
}

// getStatsOnlineIPList attempts custom xtls call for online ips
func (c *Client) GetStatsOnlineIPList(ctx context.Context, username string, reset bool) ([]UserIPSeen, error) {
	corePath := FindCorePath(false)
	if corePath == "" {
		corePath = "rw-core"
	}
	apiAddr := c.socketPath
	if apiAddr == "" || !strings.Contains(apiAddr, ":") {
		apiAddr = "127.0.0.1:62085"
	}

	cmd := exec.CommandContext(ctx, corePath, "api", "statsonlineiplist", "--server="+apiAddr, "-email", username)
	out, err := cmd.Output()
	if err != nil {
		return nil, nil
	}

	var res struct {
		IPs map[string]int64 `json:"ips"`
	}
	if err := json.Unmarshal(out, &res); err != nil {
		return nil, nil
	}

	var result []UserIPSeen
	for ip, ts := range res.IPs {
		result = append(result, UserIPSeen{
			IP:       ip,
			LastSeen: time.Unix(ts, 0).UTC(),
		})
	}
	return result, nil
}

func (c *Client) GetAllOnlineUsers(ctx context.Context) ([]string, error) {
	corePath := FindCorePath(false)
	if corePath == "" {
		corePath = "rw-core"
	}
	apiAddr := c.socketPath
	if apiAddr == "" || !strings.Contains(apiAddr, ":") {
		apiAddr = "127.0.0.1:62085"
	}

	cmd := exec.CommandContext(ctx, corePath, "api", "statsgetallonlineusers", "--server="+apiAddr)
	out, err := cmd.Output()
	if err != nil {
		return nil, nil
	}

	var res struct {
		Users []string `json:"users"`
	}
	if err := json.Unmarshal(out, &res); err != nil {
		return nil, nil
	}
	return res.Users, nil
}
