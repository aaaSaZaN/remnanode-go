package xray

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/remnawave/node-go/internal/logger"
)

var versionRe = regexp.MustCompile(`(?:Xray|rw-core)\s+v?([0-9]+\.[0-9]+\.[0-9]+[a-zA-Z0-9.-]*)`)

type ProcessStatus struct {
	Up  bool
	PID int
	Raw string
}

type ProcessManager struct {
	mu          sync.Mutex
	serviceDir  string
	controlFifo string
	s6Available bool
	directCmd   *exec.Cmd
	configPath  string
	execPath    string
	ringBuffer  *logger.RingBuffer
}

func NewProcessManager(serviceDir string, ringBuffer *logger.RingBuffer) *ProcessManager {
	if serviceDir == "" {
		serviceDir = "/run/service/xray"
	}
	controlFifo := filepath.Join(serviceDir, "supervise", "control")
	_, err := os.Stat(controlFifo)
	s6Available := err == nil

	execPath := "/usr/local/bin/rw-core"
	if _, err := os.Stat(execPath); err != nil {
		if path, err := exec.LookPath("rw-core"); err == nil {
			execPath = path
		} else if path, err := exec.LookPath("xray"); err == nil {
			execPath = path
		}
	}

	return &ProcessManager{
		serviceDir:  serviceDir,
		controlFifo: controlFifo,
		s6Available: s6Available,
		configPath:  "/tmp/rw-xray-config.json",
		execPath:    execPath,
		ringBuffer:  ringBuffer,
	}
}

func (p *ProcessManager) IsControlAvailable() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.s6Available || p.execPath != ""
}

func (p *ProcessManager) Start(configMap map[string]interface{}) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	configBytes, err := json.MarshalIndent(configMap, "", "  ")
	if err == nil {
		_ = os.WriteFile(p.configPath, configBytes, 0644)
		_ = os.WriteFile("/etc/xray/config.json", configBytes, 0644)
	}

	if p.s6Available {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, "/command/s6-svc", "-wu", "-T", "10000", "-o", p.serviceDir)
		out, err := cmd.CombinedOutput()
		if err != nil {
			return fmt.Errorf("s6 start failed: %w (output: %s)", err, string(out))
		}
		return nil
	}

	if p.directCmd != nil && p.directCmd.Process != nil {
		_ = p.directCmd.Process.Kill()
		_ = p.directCmd.Wait()
		p.directCmd = nil
	}

	cmd := exec.Command(p.execPath, "run", "-c", p.configPath)
	if p.ringBuffer != nil {
		cmd.Stdout = io.MultiWriter(os.Stdout, p.ringBuffer)
		cmd.Stderr = io.MultiWriter(os.Stderr, p.ringBuffer)
	} else {
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
	}

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("direct core start failed: %w", err)
	}
	p.directCmd = cmd
	return nil
}

func (p *ProcessManager) Stop() error {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.s6Available {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, "/command/s6-svc", "-wd", "-T", "10000", "-d", p.serviceDir)
		out, err := cmd.CombinedOutput()
		if err != nil {
			return fmt.Errorf("s6 stop failed: %w (output: %s)", err, string(out))
		}
		return nil
	}

	if p.directCmd != nil && p.directCmd.Process != nil {
		_ = p.directCmd.Process.Kill()
		_ = p.directCmd.Wait()
		p.directCmd = nil
	}
	return nil
}

func (p *ProcessManager) GetCoreVersion() (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, p.execPath, "version")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", err
	}

	matches := versionRe.FindSubmatch(out)
	if len(matches) > 1 {
		return string(matches[1]), nil
	}

	lines := strings.Split(string(out), "\n")
	if len(lines) > 0 {
		return strings.TrimSpace(lines[0]), nil
	}
	return "unknown", nil
}

func (p *ProcessManager) GetStatus() ProcessStatus {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.s6Available {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()

		cmd := exec.CommandContext(ctx, "/command/s6-svstat", "-o", "up,pid", p.serviceDir)
		out, err := cmd.CombinedOutput()
		if err != nil {
			return ProcessStatus{Up: false, PID: 0, Raw: err.Error()}
		}
		fields := strings.Fields(string(out))
		up := len(fields) > 0 && fields[0] == "true"
		pid := 0
		if len(fields) > 1 {
			pid, _ = strconv.Atoi(fields[1])
		}
		return ProcessStatus{Up: up, PID: pid, Raw: strings.TrimSpace(string(out))}
	}

	if p.directCmd != nil && p.directCmd.Process != nil {
		pid := p.directCmd.Process.Pid
		return ProcessStatus{Up: pid > 0, PID: pid, Raw: fmt.Sprintf("up, pid %d", pid)}
	}

	return ProcessStatus{Up: false, PID: 0, Raw: "stopped"}
}

func (p *ProcessManager) GetStatusLine() string {
	st := p.GetStatus()
	if st.Up {
		return fmt.Sprintf("up (pid %d)", st.PID)
	}
	return "down"
}

func (p *ProcessManager) TailLog(lines int) []string {
	if p.ringBuffer != nil && p.ringBuffer.Size() > 0 {
		return p.ringBuffer.GetLines(lines)
	}

	logPaths := []string{"/var/log/xray/current", "/var/log/xray.log"}
	for _, lp := range logPaths {
		if _, err := os.Stat(lp); err == nil {
			cmd := exec.Command("tail", "-n", strconv.Itoa(lines), lp)
			out, err := cmd.Output()
			if err == nil {
				var res []string
				for _, line := range strings.Split(string(out), "\n") {
					line = strings.TrimSpace(line)
					if line != "" {
						res = append(res, line)
					}
				}
				return res
			}
		}
	}
	return nil
}
