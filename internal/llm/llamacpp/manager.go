package llamacpp

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
	"time"
)

const (
	// Using a lightweight 1.5B model perfectly suited for a 4GB RAM environment.
	modelName = "qwen2.5-coder-1.5b-instruct-q4_k_m.gguf"
	modelURL  = "https://huggingface.co/Qwen/Qwen2.5-Coder-1.5B-Instruct-GGUF/resolve/main/qwen2.5-coder-1.5b-instruct-q4_k_m.gguf"
)

// ensureDirectories creates the bin and models directories inside ~/.pitty
func ensureDirectories() (binDir, modelsDir string, err error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", "", err
	}
	base := filepath.Join(home, ".pitty")
	binDir = filepath.Join(base, "bin")
	modelsDir = filepath.Join(base, "models")
	if err := os.MkdirAll(binDir, 0755); err != nil {
		return "", "", err
	}
	if err := os.MkdirAll(modelsDir, 0755); err != nil {
		return "", "", err
	}
	return binDir, modelsDir, nil
}

// downloadFile downloads a URL to a local path with a simple progress callback.
func downloadFile(url, dest string, onProgress func(int64, int64)) error {
	tmpPath := dest + ".tmp"
	out, err := os.Create(tmpPath)
	if err != nil {
		return err
	}
	defer out.Close()

	resp, err := http.Get(url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("bad status: %s", resp.Status)
	}

	total := resp.ContentLength
	var downloaded int64
	buf := make([]byte, 32*1024)
	for {
		n, err := resp.Body.Read(buf)
		if n > 0 {
			out.Write(buf[:n])
			downloaded += int64(n)
			if onProgress != nil {
				onProgress(downloaded, total)
			}
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
	}
	out.Close()
	return os.Rename(tmpPath, dest)
}

// getLatestServerURL fetches the latest llama-server binary URL for Ubuntu x64.
func getLatestServerURL() (string, error) {
	resp, err := http.Get("https://api.github.com/repos/ggml-org/llama.cpp/releases")
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	var releases []struct {
		Assets []struct {
			Name               string `json:"name"`
			BrowserDownloadURL string `json:"browser_download_url"`
		} `json:"assets"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&releases); err != nil {
		return "", err
	}

	re := regexp.MustCompile(`^llama-b\d+-bin-ubuntu-x64\.tar\.gz$`)
	for _, release := range releases {
		for _, a := range release.Assets {
			if re.MatchString(a.Name) {
				return a.BrowserDownloadURL, nil
			}
		}
	}
	return "", fmt.Errorf("ubuntu-x64 binary not found in recent releases")
}

// downloadAndExtractServer downloads the tar.gz and extracts all files (binary and .so libs) to binDir.
func downloadAndExtractServer(binDir string, onLog func(string)) error {
	onLog("Mencari rilis terbaru llama.cpp...")
	url, err := getLatestServerURL()
	if err != nil {
		return err
	}
	
	onLog("Mengunduh llama-server...")
	tarPath := filepath.Join(binDir, "download.tar.gz")
	var lastPct int64
	err = downloadFile(url, tarPath, func(dl, total int64) {
		if total <= 0 {
			return
		}
		pct := (dl * 100) / total
		if pct > lastPct+10 || pct == 100 {
			onLog(fmt.Sprintf("Mengunduh server: %d%%", pct))
			lastPct = pct
		}
	})
	if err != nil {
		return err
	}
	defer os.Remove(tarPath)

	onLog("Mengekstrak llama-server...")
	f, err := os.Open(tarPath)
	if err != nil {
		return err
	}
	defer f.Close()

	gzr, err := gzip.NewReader(f)
	if err != nil {
		return err
	}
	defer gzr.Close()

	tr := tar.NewReader(gzr)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		if hdr.Typeflag == tar.TypeDir {
			continue
		}

		outPath := filepath.Join(binDir, filepath.Base(hdr.Name))
		
		if hdr.Typeflag == tar.TypeSymlink {
			_ = os.Remove(outPath) // clean if exists
			if err := os.Symlink(filepath.Base(hdr.Linkname), outPath); err != nil {
				return err
			}
			continue
		}

		out, err := os.OpenFile(outPath, os.O_CREATE|os.O_RDWR|os.O_TRUNC, 0755)
		if err != nil {
			return err
		}
		if _, err := io.Copy(out, tr); err != nil {
			out.Close()
			return err
		}
		out.Close()
	}

	if _, err := os.Stat(filepath.Join(binDir, "llama-server")); err != nil {
		return fmt.Errorf("llama-server binary not found inside archive")
	}
	return nil
}

// AutoStart checks if llama-server is running on baseURL. If not, it provisions the
// binary and model, spawns it in the background, and waits for it to become ready.
func AutoStart(ctx context.Context, baseURL string, threads int, onLog func(string)) error {
	// 1. Check if already running
	client := http.Client{Timeout: 2 * time.Second}
	if resp, err := client.Get(strings.TrimRight(baseURL, "/") + "/health"); err == nil {
		resp.Body.Close()
		if resp.StatusCode == http.StatusOK {
			return nil // Already running!
		}
	}

	onLog("Server lokal tidak merespons, memulai setup otomatis...")

	binDir, modelsDir, err := ensureDirectories()
	if err != nil {
		return fmt.Errorf("gagal membuat direktori: %w", err)
	}

	serverPath := filepath.Join(binDir, "llama-server")
	soPath := filepath.Join(binDir, "libllama-server-impl.so")
	_, errServer := os.Stat(serverPath)
	_, errSo := os.Stat(soPath)

	if os.IsNotExist(errServer) || os.IsNotExist(errSo) {
		if err := downloadAndExtractServer(binDir, onLog); err != nil {
			return fmt.Errorf("gagal mengunduh server: %w", err)
		}
	}

	modelPath := filepath.Join(modelsDir, modelName)
	if _, err := os.Stat(modelPath); os.IsNotExist(err) {
		onLog(fmt.Sprintf("Mengunduh model %s (1.1GB, cocok untuk RAM laptop Anda)...", modelName))
		var lastPct int64
		err := downloadFile(modelURL, modelPath, func(dl, total int64) {
			if total <= 0 {
				return
			}
			pct := (dl * 100) / total
			if pct > lastPct+5 || pct == 100 {
				onLog(fmt.Sprintf("Mengunduh model: %d%%", pct))
				lastPct = pct
			}
		})
		if err != nil {
			return fmt.Errorf("gagal mengunduh model: %w", err)
		}
	}

	onLog("Menjalankan llama-server di latar belakang...")
	port := "8080"
	// Extract port from baseURL (assuming format http://127.0.0.1:8080)
	if parts := strings.Split(baseURL, ":"); len(parts) >= 3 {
		port = parts[len(parts)-1]
	}

	// 4096 context is sufficient for typical coding tasks and saves RAM.
	args := []string{"-m", modelPath, "-c", "4096", "--port", port}
	if threads > 0 {
		args = append(args, "-t", fmt.Sprintf("%d", threads))
	}

	cmd := exec.Command(serverPath, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	cmd.Env = append(os.Environ(), "LD_LIBRARY_PATH="+binDir)
	
	// Create a log file for the server
	logFile, err := os.Create(filepath.Join(binDir, "llama-server.log"))
	if err == nil {
		cmd.Stdout = logFile
		cmd.Stderr = logFile
	}
	
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("gagal menjalankan server: %w", err)
	}

	// Wait for the server to spin up
	onLog("Menunggu server siap (loading model ke memory)...")
	start := time.Now()
	for time.Since(start) < 60*time.Second {
		time.Sleep(1 * time.Second)
		if resp, err := client.Get(strings.TrimRight(baseURL, "/") + "/health"); err == nil {
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				onLog("Server lokal siap digunakan!")
				return nil
			}
		}
	}

	return fmt.Errorf("timeout menunggu server lokal berjalan")
}
