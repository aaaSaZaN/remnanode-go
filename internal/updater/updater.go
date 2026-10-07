package updater

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

type ReleaseAsset struct {
	Name               string `json:"name"`
	BrowserDownloadURL string `json:"browser_download_url"`
	Size               int64  `json:"size"`
}

type ReleaseInfo struct {
	TagName     string         `json:"tag_name"`
	Name        string         `json:"name"`
	Prerelease  bool           `json:"prerelease"`
	PublishedAt time.Time      `json:"published_at"`
	Assets      []ReleaseAsset `json:"assets"`
}

type CheckResult struct {
	CurrentVersion   string        `json:"currentVersion"`
	LatestRelease    *ReleaseInfo  `json:"latestRelease,omitempty"`
	LatestPrerelease *ReleaseInfo  `json:"latestPrerelease,omitempty"`
	AllReleases      []ReleaseInfo `json:"allReleases"`
	Architecture     string        `json:"architecture"`
	OS               string        `json:"os"`
	CurrentCoreVer   string        `json:"currentCoreVersion"`
}

func FetchReleases(repo string) ([]ReleaseInfo, error) {
	if repo == "" {
		repo = "remnawave/node"
	}
	url := fmt.Sprintf("https://api.github.com/repos/%s/releases?per_page=15", repo)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "Remnawave-Node-Updater")

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("github api returned status %d", resp.StatusCode)
	}

	var releases []ReleaseInfo
	if err := json.NewDecoder(resp.Body).Decode(&releases); err != nil {
		return nil, err
	}
	for i := range releases {
		lower := strings.ToLower(releases[i].TagName)
		if releases[i].Prerelease || strings.Contains(lower, "alpha") || strings.Contains(lower, "beta") || strings.Contains(lower, "rc") || strings.Contains(lower, "pre") || strings.Contains(lower, "dev") {
			releases[i].Prerelease = true
		}
	}
	return releases, nil
}

func MatchAsset(assets []ReleaseAsset, targetOS, targetArch, binaryName string) (*ReleaseAsset, error) {
	archAliases := map[string][]string{
		"amd64":    {"amd64", "x86_64", "linux-64", "64bit"},
		"arm64":    {"arm64-v8a", "arm64v8", "arm64", "aarch64"},
		"arm":      {"arm32-v7a", "armv7", "arm32-v6", "armv6", "armhf", "arm"},
		"mipsle":   {"mips32le", "mipsle", "mipsel"},
		"mips":     {"mips32", "mips"},
		"mips64le": {"mips64le"},
		"mips64":   {"mips64"},
		"386":      {"linux-32", "386", "i386", "32bit", "x86"},
	}

	aliases := archAliases[targetArch]
	if len(aliases) == 0 {
		aliases = []string{targetArch}
	}

	for _, alias := range aliases {
		for _, a := range assets {
			lowerName := strings.ToLower(a.Name)
			if !strings.Contains(lowerName, strings.ToLower(targetOS)) {
				continue
			}
			if targetArch == "mips" && (strings.Contains(lowerName, "le") || strings.Contains(lowerName, "el")) {
				continue
			}
			if targetArch == "mips64" && (strings.Contains(lowerName, "le") || strings.Contains(lowerName, "el")) {
				continue
			}
			if strings.Contains(lowerName, alias) {
				return &a, nil
			}
		}
	}

	for _, alias := range aliases {
		for _, a := range assets {
			lowerName := strings.ToLower(a.Name)
			if targetArch == "mips" && (strings.Contains(lowerName, "le") || strings.Contains(lowerName, "el")) {
				continue
			}
			if targetArch == "mips64" && (strings.Contains(lowerName, "le") || strings.Contains(lowerName, "el")) {
				continue
			}
			if strings.Contains(lowerName, alias) {
				return &a, nil
			}
		}
	}

	return nil, fmt.Errorf("no matching asset found for %s/%s", targetOS, targetArch)
}

func DownloadAndSaveBinary(downloadURL, targetBinaryName string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, "GET", downloadURL, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", "Remnawave-Node-Updater")

	client := &http.Client{Timeout: 3 * time.Minute}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("download failed with status %d", resp.StatusCode)
	}

	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}

	lowerURL := strings.ToLower(downloadURL)
	tmpFile, err := os.CreateTemp("", targetBinaryName+"-*")
	if err != nil {
		return "", err
	}
	tmpPath := tmpFile.Name()

	if strings.HasSuffix(lowerURL, ".zip") {
		_ = tmpFile.Close()
		_ = os.Remove(tmpPath)
		return extractZip(bodyBytes, targetBinaryName, tmpPath)
	} else if strings.HasSuffix(lowerURL, ".tar.gz") || strings.HasSuffix(lowerURL, ".tgz") {
		_ = tmpFile.Close()
		_ = os.Remove(tmpPath)
		return extractTarGz(bodyBytes, targetBinaryName, tmpPath)
	}

	if _, err := tmpFile.Write(bodyBytes); err != nil {
		_ = tmpFile.Close()
		_ = os.Remove(tmpPath)
		return "", err
	}
	_ = tmpFile.Close()
	if err := os.Chmod(tmpPath, 0755); err != nil {
		_ = os.Remove(tmpPath)
		return "", err
	}
	return tmpPath, nil
}

func extractZip(data []byte, binaryName, destPath string) (string, error) {
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return "", err
	}

	for _, f := range zr.File {
		base := filepath.Base(f.Name)
		if base == binaryName || strings.EqualFold(base, binaryName) || strings.EqualFold(base, binaryName+".exe") || (binaryName == "rw-core" && strings.EqualFold(base, "xray")) {
			rc, err := f.Open()
			if err != nil {
				return "", err
			}
			out, err := os.OpenFile(destPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0755)
			if err != nil {
				_ = rc.Close()
				return "", err
			}
			_, err = io.Copy(out, rc)
			_ = out.Close()
			_ = rc.Close()
			if err != nil {
				return "", err
			}
			return destPath, nil
		}
	}
	return "", fmt.Errorf("binary %s not found in zip archive", binaryName)
}

func extractTarGz(data []byte, binaryName, destPath string) (string, error) {
	gr, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return "", err
	}
	defer gr.Close()

	tr := tar.NewReader(gr)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return "", err
		}
		base := filepath.Base(hdr.Name)
		if base == binaryName || strings.EqualFold(base, binaryName) || (binaryName == "rw-core" && strings.EqualFold(base, "xray")) {
			out, err := os.OpenFile(destPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0755)
			if err != nil {
				return "", err
			}
			_, err = io.Copy(out, tr)
			_ = out.Close()
			if err != nil {
				return "", err
			}
			return destPath, nil
		}
	}
	return "", fmt.Errorf("binary %s not found in tar.gz archive", binaryName)
}

func AtomicReplace(tmpBinaryPath, targetPath string) error {
	dir := filepath.Dir(targetPath)
	sameDirTmp := filepath.Join(dir, "."+filepath.Base(targetPath)+".new")
	_ = os.Remove(sameDirTmp)

	in, err := os.Open(tmpBinaryPath)
	if err != nil {
		return err
	}
	defer in.Close()

	out, err := os.OpenFile(sameDirTmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0755)
	if err != nil {
		return err
	}
	_, err = io.Copy(out, in)
	_ = out.Close()
	if err != nil {
		_ = os.Remove(sameDirTmp)
		return err
	}

	_ = os.Chmod(sameDirTmp, 0755)
	if err := os.Rename(sameDirTmp, targetPath); err != nil {
		_ = os.Remove(sameDirTmp)
		return err
	}
	_ = os.Remove(tmpBinaryPath)
	return nil
}

func ValidateCoreBinary(corePath string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, corePath, "version")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("core validation failed: %w (output: %s)", err, string(out))
	}
	return nil
}

func RestartNodeProcess() {
	go func() {
		time.Sleep(1 * time.Second)
		_ = exec.Command("systemctl", "restart", "remnanode").Run()
	}()
}

func GetCurrentArch() string {
	return runtime.GOARCH
}

func GetCurrentOS() string {
	return runtime.GOOS
}
