package stats

import (
	"net"
	"os"
	"runtime"
	"strings"

	"github.com/shirou/gopsutil/v3/cpu"
	"github.com/shirou/gopsutil/v3/host"
	"github.com/shirou/gopsutil/v3/load"
	"github.com/shirou/gopsutil/v3/mem"
)

type NodeSystemInfo struct {
	Arch              string   `json:"arch"`
	CPUs              int      `json:"cpus"`
	CPUModel          string   `json:"cpuModel"`
	MemoryTotal       uint64   `json:"memoryTotal"`
	Hostname          string   `json:"hostname"`
	Platform          string   `json:"platform"`
	Release           string   `json:"release"`
	Type              string   `json:"type"`
	Version           string   `json:"version"`
	NetworkInterfaces []string `json:"networkInterfaces"`
}

type NodeSystemStats struct {
	MemoryFree uint64                `json:"memoryFree"`
	MemoryUsed uint64                `json:"memoryUsed"`
	Uptime     uint64                `json:"uptime"`
	LoadAvg    []float64             `json:"loadAvg"`
	Interface  *NetworkInterfaceRate `json:"interface"`
}

type NodeSystemCombined struct {
	Info  NodeSystemInfo  `json:"info"`
	Stats NodeSystemStats `json:"stats"`
}

func GetSystemInfo() NodeSystemInfo {
	hostname, _ := os.Hostname()
	hi, _ := host.Info()

	cpuModel := "unknown"
	if cpuInfos, err := cpu.Info(); err == nil && len(cpuInfos) > 0 {
		cpuModel = cpuInfos[0].ModelName
	}

	totalMem := uint64(0)
	if vm, err := mem.VirtualMemory(); err == nil {
		totalMem = vm.Total
	}

	var ifaceNames []string
	if ifaces, err := net.Interfaces(); err == nil {
		for _, iface := range ifaces {
			ifaceNames = append(ifaceNames, iface.Name)
		}
	}

	platform := runtime.GOOS
	release := ""
	hType := runtime.GOOS
	version := ""
	if hi != nil {
		platform = hi.Platform
		release = hi.KernelVersion
		hType = hi.OS
		version = hi.PlatformVersion
	}

	if release == "" {
		if data, err := os.ReadFile("/proc/sys/kernel/osrelease"); err == nil {
			release = strings.TrimSpace(string(data))
		}
	}

	if data, err := os.ReadFile("/etc/os-release"); err == nil {
		for _, line := range strings.Split(string(data), "\n") {
			line = strings.TrimSpace(line)
			if strings.HasPrefix(line, "PRETTY_NAME=") {
				pretty := strings.Trim(strings.TrimPrefix(line, "PRETTY_NAME="), "\"")
				if pretty != "" {
					platform = pretty
				}
				break
			}
		}
	}

	if cpuModel == "unknown" || cpuModel == "" {
		if data, err := os.ReadFile("/proc/cpuinfo"); err == nil {
			for _, line := range strings.Split(string(data), "\n") {
				if strings.HasPrefix(line, "model name") {
					parts := strings.SplitN(line, ":", 2)
					if len(parts) == 2 {
						cpuModel = strings.TrimSpace(parts[1])
						break
					}
				}
			}
		}
	}

	return NodeSystemInfo{
		Arch:              runtime.GOARCH,
		CPUs:              runtime.NumCPU(),
		CPUModel:          cpuModel,
		MemoryTotal:       totalMem,
		Hostname:          hostname,
		Platform:          platform,
		Release:           release,
		Type:              hType,
		Version:           version,
		NetworkInterfaces: ifaceNames,
	}
}

func GetSystemStats(netPoller *NetworkPoller) NodeSystemStats {
	memFree := uint64(0)
	memUsed := uint64(0)
	if vm, err := mem.VirtualMemory(); err == nil {
		memFree = vm.Free
		memUsed = vm.Used
	}

	uptime := uint64(0)
	if up, err := host.Uptime(); err == nil {
		uptime = up
	}

	loadAvg := []float64{0, 0, 0}
	if l, err := load.Avg(); err == nil {
		loadAvg = []float64{l.Load1, l.Load5, l.Load15}
	}

	var defIface *NetworkInterfaceRate
	if netPoller != nil {
		defIface = netPoller.GetDefault()
	}

	return NodeSystemStats{
		MemoryFree: memFree,
		MemoryUsed: memUsed,
		Uptime:     uptime,
		LoadAvg:    loadAvg,
		Interface:  defIface,
	}
}

func GetSystemCombined(netPoller *NetworkPoller) NodeSystemCombined {
	return NodeSystemCombined{
		Info:  GetSystemInfo(),
		Stats: GetSystemStats(netPoller),
	}
}
