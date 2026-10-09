package cli

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

func RunStatusCLI(version string) {
	fmt.Printf("%s[STATUS]%s Checking Remnanode service status...\n\n", colorCyan, colorReset)

	socketPaths := []string{
		"/var/lib/remnanode/rw-node.sock",
		"rw-node.sock",
		"/tmp/rw-node.sock",
		"/run/remnanode/rw-node.sock",
		"\x00rw-node.sock",
	}

	var activeSocket string
	for _, p := range socketPaths {
		transport := &http.Transport{
			DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				var d net.Dialer
				return d.DialContext(ctx, "unix", p)
			},
		}
		c := &http.Client{Transport: transport, Timeout: 1 * time.Second}
		resp, err := c.Get("http://unix/internal/logs?type=node&lines=1&format=json")
		if err == nil {
			_ = resp.Body.Close()
			activeSocket = p
			break
		}
	}

	// 1. Daemon socket status
	if activeSocket != "" {
		fmt.Printf("  %s●%s Daemon (Socket):   %sRUNNING%s (%s)\n", colorGreen, colorReset, colorGreen, colorReset, activeSocket)
	} else {
		fmt.Printf("  %s○%s Daemon (Socket):   %sINACTIVE / NOT RESPONDING%s\n", colorRed, colorReset, colorRed, colorReset)
	}

	// 2. Systemd service status
	if _, err := exec.LookPath("systemctl"); err == nil {
		out, err := exec.Command("systemctl", "is-active", "remnanode").Output()
		status := strings.TrimSpace(string(out))
		if err == nil && status == "active" {
			fmt.Printf("  %s●%s Systemd Service:   %s%s%s (remnanode.service)\n", colorGreen, colorReset, colorGreen, status, colorReset)
		} else {
			if status == "" {
				status = "unknown / not installed"
			}
			fmt.Printf("  %s○%s Systemd Service:   %s%s%s\n", colorYellow, colorReset, colorYellow, status, colorReset)
		}
	}

	// 3. Xray Core detection
	corePaths := []string{"./rw-core", "/usr/local/bin/rw-core"}
	if exe, err := os.Executable(); err == nil {
		corePaths = append([]string{filepath.Join(filepath.Dir(exe), "rw-core")}, corePaths...)
	}
	var foundCore string
	var coreVer string
	for _, cp := range corePaths {
		if fi, err := os.Stat(cp); err == nil && !fi.IsDir() {
			foundCore = cp
			out, err := exec.Command(cp, "version").Output()
			if err == nil {
				firstLine := strings.Split(string(out), "\n")[0]
				coreVer = strings.TrimSpace(firstLine)
			}
			break
		}
	}

	if foundCore != "" {
		if coreVer == "" {
			coreVer = "detected"
		}
		fmt.Printf("  %s●%s Xray Core:         %s%s%s (%s)\n", colorGreen, colorReset, colorCyan, coreVer, colorReset, foundCore)
	} else {
		fmt.Printf("  %s○%s Xray Core:         %sNOT FOUND%s (run 'remnanode core install')\n", colorRed, colorReset, colorRed, colorReset)
	}

	// 4. Version info
	fmt.Printf("  %s●%s Node Version:      v%s\n\n", colorCyan, colorReset, version)
}

func RunRestartCLI() {
	fmt.Printf("%s[RESTART]%s Restarting Remnanode service...\n", colorCyan, colorReset)

	if _, err := exec.LookPath("systemctl"); err == nil {
		cmd := exec.Command("systemctl", "restart", "remnanode")
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err == nil {
			fmt.Printf("%s[SUCCESS]%s Remnanode restarted successfully via systemd.\n", colorGreen, colorReset)
			return
		}
	}

	if _, err := exec.LookPath("service"); err == nil {
		cmd := exec.Command("service", "remnanode", "restart")
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err == nil {
			fmt.Printf("%s[SUCCESS]%s Remnanode restarted successfully via service.\n", colorGreen, colorReset)
			return
		}
	}

	if _, err := os.Stat("/etc/init.d/remnanode"); err == nil {
		cmd := exec.Command("/etc/init.d/remnanode", "restart")
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err == nil {
			fmt.Printf("%s[SUCCESS]%s Remnanode restarted successfully via init.d.\n", colorGreen, colorReset)
			return
		}
	}

	fmt.Printf("%s[ERROR]%s Could not automatically restart service. Run 'systemctl restart remnanode' manually.\n", colorRed, colorReset)
	os.Exit(1)
}
