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

func GetStandardAssetDirs(extraDirs ...string) []string {
	dirs := []string{"/usr/local/share/xray", "/opt/remnanode", "/usr/share/xray"}
	if exe, err := os.Executable(); err == nil {
		dirs = append([]string{filepath.Dir(exe)}, dirs...)
	}
	for _, ed := range extraDirs {
		if ed != "" {
			dirs = append(dirs, ed)
		}
	}
	seen := make(map[string]bool)
	res := make([]string, 0, len(dirs))
	for _, d := range dirs {
		clean := filepath.Clean(d)
		if !seen[clean] {
			seen[clean] = true
			res = append(res, clean)
		}
	}
	return res
}

func CopyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	if err := os.MkdirAll(filepath.Dir(dst), 0755); err != nil {
		return err
	}

	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644)
	if err != nil {
		return err
	}
	defer out.Close()

	_, err = io.Copy(out, in)
	return err
}

func extractZip(data []byte, binaryName, destPath string) (string, error) {
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return "", err
	}

	destDir := filepath.Dir(destPath)
	foundBinary := false

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
			foundBinary = true
		} else if strings.EqualFold(base, "geoip.dat") || strings.EqualFold(base, "geosite.dat") {
			fileName := strings.ToLower(base)
			rc, err := f.Open()
			if err == nil {
				content, readErr := io.ReadAll(rc)
				_ = rc.Close()
				if readErr == nil && len(content) > 1024 {
					_ = os.WriteFile(filepath.Join(destDir, fileName), content, 0644)
					for _, d := range GetStandardAssetDirs(destDir) {
						_ = os.MkdirAll(d, 0755)
						_ = os.WriteFile(filepath.Join(d, fileName), content, 0644)
					}
				}
			}
		}
	}
	if foundBinary {
		return destPath, nil
	}
	return "", fmt.Errorf("binary %s not found in zip archive", binaryName)
}

func extractTarGz(data []byte, binaryName, destPath string) (string, error) {
	gr, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return "", err
	}
	defer gr.Close()

	destDir := filepath.Dir(destPath)
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
		} else if strings.EqualFold(base, "geoip.dat") || strings.EqualFold(base, "geosite.dat") {
			fileName := strings.ToLower(base)
			content, readErr := io.ReadAll(tr)
			if readErr == nil && len(content) > 1024 {
				_ = os.WriteFile(filepath.Join(destDir, fileName), content, 0644)
				for _, d := range GetStandardAssetDirs(destDir) {
					_ = os.MkdirAll(d, 0755)
					_ = os.WriteFile(filepath.Join(d, fileName), content, 0644)
				}
			}
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


func EnsureCore(targetPath string) (string, error) {
	if targetPath != "" {
		if fi, err := os.Stat(targetPath); err == nil && !fi.IsDir() {
			_ = EnsureGeodata(filepath.Dir(targetPath), "/opt/remnanode", "/usr/local/share/xray")
			return targetPath, nil
		}
	}

	repo := "XTLS/Xray-core"
	fmt.Printf("[CORE] Xray core not found. Automatically downloading latest release from %s...\n", repo)

	releases, err := FetchReleases(repo)
	if err != nil {
		return "", fmt.Errorf("failed to fetch releases: %w", err)
	}
	if len(releases) == 0 {
		return "", fmt.Errorf("no releases found for %s", repo)
	}

	var latest *ReleaseInfo
	for _, r := range releases {
		if !r.Prerelease {
			latest = &r
			break
		}
	}
	if latest == nil {
		latest = &releases[0]
	}

	asset, err := MatchAsset(latest.Assets, GetCurrentOS(), GetCurrentArch(), "rw-core")
	if err != nil {
		return "", fmt.Errorf("could not find compatible asset for %s/%s: %w", GetCurrentOS(), GetCurrentArch(), err)
	}

	fmt.Printf("[CORE] Downloading %s (%s)...\n", asset.Name, latest.TagName)
	tmpPath, err := DownloadAndSaveBinary(asset.BrowserDownloadURL, "rw-core")
	if err != nil {
		return "", fmt.Errorf("download failed: %w", err)
	}

	if err := ValidateCoreBinary(tmpPath); err != nil {
		_ = os.Remove(tmpPath)
		return "", fmt.Errorf("downloaded core failed validation: %w", err)
	}

	if targetPath == "" {
		if exe, err := os.Executable(); err == nil {
			targetPath = filepath.Join(filepath.Dir(exe), "rw-core")
		} else {
			targetPath = "./rw-core"
		}
	}

	_ = os.MkdirAll(filepath.Dir(targetPath), 0755)

	if err := AtomicReplace(tmpPath, targetPath); err != nil {
		return "", fmt.Errorf("failed to install core to %s: %w", targetPath, err)
	}

	_ = EnsureGeodata(filepath.Dir(targetPath), "/opt/remnanode", "/usr/local/share/xray")

	fmt.Printf("[CORE] Successfully installed Xray core to %s!\n", targetPath)
	return targetPath, nil
}

func EnsureGeodata(targetDirs ...string) error {
	requiredFiles := []string{"geoip.dat", "geosite.dat"}
	allDirs := GetStandardAssetDirs(targetDirs...)

	for _, reqFile := range requiredFiles {
		var foundPath string
		searchList := append([]string{"/tmp"}, allDirs...)
		for _, dir := range searchList {
			candidate := filepath.Join(dir, reqFile)
			if fi, err := os.Stat(candidate); err == nil && fi.Size() > 1024 {
				foundPath = candidate
				break
			}
		}

		if foundPath != "" {
			for _, dir := range allDirs {
				target := filepath.Join(dir, reqFile)
				if target != foundPath {
					if fi, err := os.Stat(target); err != nil || fi.Size() == 0 {
						_ = CopyFile(foundPath, target)
					}
				}
			}
			continue
		}

		downloadURLs := []string{}
		if reqFile == "geoip.dat" {
			downloadURLs = []string{
				"https://github.com/Loyalsoldier/v2ray-rules-dat/releases/latest/download/geoip.dat",
				"https://github.com/v2fly/geoip/releases/latest/download/geoip.dat",
			}
		} else {
			downloadURLs = []string{
				"https://github.com/Loyalsoldier/v2ray-rules-dat/releases/latest/download/geosite.dat",
				"https://github.com/v2fly/domain-list-community/releases/latest/download/dlc.dat",
			}
		}

		fmt.Printf("[CORE] Geodata asset %s missing. Downloading from CDN...\n", reqFile)
		downloaded := false
		for _, u := range downloadURLs {
			data, err := downloadFileBytes(u)
			if err == nil && len(data) > 1024 {
				for _, dir := range allDirs {
					_ = os.MkdirAll(dir, 0755)
					_ = os.WriteFile(filepath.Join(dir, reqFile), data, 0644)
				}
				fmt.Printf("[CORE] Successfully installed %s (%d bytes).\n", reqFile, len(data))
				downloaded = true
				break
			}
		}
		if !downloaded {
			fmt.Printf("[CORE] Warning: Failed to download %s from all sources.\n", reqFile)
		}
	}
	return nil
}

func downloadFileBytes(url string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "Remnanode-Geodata-Downloader")
	client := &http.Client{Timeout: 2 * time.Minute}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	return io.ReadAll(resp.Body)
}
