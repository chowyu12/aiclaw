package selfupdate

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

const (
	repo       = "chowyu12/aiclaw"
	binaryName = "aiclaw"
)

// metadataHTTPClient 用于 GitHub API 查询（短超时，小响应）。
// downloadHTTPClient 用于实际二进制下载（长超时，大响应）。
// 两者共享 transport 模板，但保留不同的 Timeout 以符合各自场景。
var (
	metadataHTTPClient = &http.Client{Timeout: 15 * time.Second, Transport: newSelfUpdateTransport()}
	downloadHTTPClient = &http.Client{Timeout: 5 * time.Minute, Transport: newSelfUpdateTransport()}
)

func newSelfUpdateTransport() *http.Transport {
	return &http.Transport{
		MaxIdleConns:        4,
		MaxIdleConnsPerHost: 2,
		IdleConnTimeout:     60 * time.Second,
	}
}

type ghRelease struct {
	TagName string `json:"tag_name"`
}

func Run(currentVersion string) {
	fmt.Printf("当前版本: %s\n", currentVersion)
	fmt.Println("正在检查最新版本...")

	latest, err := fetchLatestTag()
	if err != nil {
		fmt.Fprintf(os.Stderr, "获取最新版本失败: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("最新版本: %s\n", latest)

	if latest == currentVersion {
		fmt.Println("已是最新版本，无需更新。")
		return
	}

	asset := assetName()
	url := fmt.Sprintf("https://github.com/%s/releases/download/%s/%s", repo, latest, asset)
	fmt.Printf("正在下载 %s ...\n", url)

	selfPath, err := os.Executable()
	if err != nil {
		fmt.Fprintf(os.Stderr, "无法获取当前可执行文件路径: %v\n", err)
		os.Exit(1)
	}
	selfPath, _ = filepath.EvalSymlinks(selfPath)

	tmpFile, err := download(url)
	if err != nil {
		fmt.Fprintf(os.Stderr, "下载失败: %v\n", err)
		os.Exit(1)
	}
	defer os.Remove(tmpFile)

	if err := replace(selfPath, tmpFile); err != nil {
		fmt.Fprintf(os.Stderr, "替换二进制文件失败: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("\n✅ 更新成功: %s → %s\n", currentVersion, latest)
	fmt.Println("请重启服务以使新版本生效：")
	fmt.Println("  aiclaw restart")
}

func fetchLatestTag() (string, error) {
	url := fmt.Sprintf("https://api.github.com/repos/%s/releases/latest", repo)
	resp, err := metadataHTTPClient.Get(url)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return "", fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(body))
	}
	var rel ghRelease
	if err := json.NewDecoder(resp.Body).Decode(&rel); err != nil {
		return "", fmt.Errorf("解析响应失败: %w", err)
	}
	if rel.TagName == "" {
		return "", fmt.Errorf("未找到 release tag")
	}
	return rel.TagName, nil
}

func assetName() string {
	os := runtime.GOOS
	arch := runtime.GOARCH
	name := fmt.Sprintf("%s-%s-%s", binaryName, os, arch)
	if os == "windows" {
		name += ".exe"
	}
	return name
}

func download(url string) (string, error) {
	resp, err := downloadHTTPClient.Get(url)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("HTTP %d", resp.StatusCode)
	}

	tmp, err := os.CreateTemp("", "aiclaw-update-*")
	if err != nil {
		return "", fmt.Errorf("创建临时文件失败: %w", err)
	}

	size, err := io.Copy(tmp, resp.Body)
	if err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return "", fmt.Errorf("下载写入失败: %w", err)
	}
	tmp.Close()

	sizeStr := formatBytes(size)
	fmt.Printf("下载完成 (%s)\n", sizeStr)
	return tmp.Name(), nil
}

func replace(target, newBinary string) error {
	if err := os.Chmod(newBinary, 0o755); err != nil {
		return fmt.Errorf("设置执行权限失败: %w", err)
	}

	backupPath := target + ".bak"
	os.Remove(backupPath)

	if err := os.Rename(target, backupPath); err != nil {
		return fmt.Errorf("备份旧文件失败: %w\n如果权限不足请尝试: sudo %s update", err, target)
	}

	if err := copyFile(newBinary, target); err != nil {
		os.Rename(backupPath, target)
		return fmt.Errorf("写入新文件失败: %w", err)
	}

	os.Remove(backupPath)
	return nil
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755)
	if err != nil {
		return err
	}
	defer out.Close()

	_, err = io.Copy(out, in)
	return err
}

func formatBytes(b int64) string {
	const unit = 1024
	if b < unit {
		return fmt.Sprintf("%d B", b)
	}
	div, exp := int64(unit), 0
	for n := b / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %s", float64(b)/float64(div), strings.TrimSpace(string("KMGTPE"[exp:exp+1]))+"iB")
}
