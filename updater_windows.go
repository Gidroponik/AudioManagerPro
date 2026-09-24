package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"golang.org/x/sys/windows"
)

const (
	repo       = "Gidroponik/AudioManagerPro"
	assetName  = "AudioManagerPro.exe"
	defaultAPI = "https://api.github.com/repos/" + repo + "/releases/latest"
	waitPidArg = "--wait-pid="
)

// version is stamped by CI (-X main.version=1.2.3); local builds stay "dev"
// and never offer updates.
var version = "dev"

// latestAPI is a variable so tests can point it at a local server.
var latestAPI = defaultAPI

type UpdateInfo struct {
	Version string `json:"version"`
	Notes   string `json:"notes"`
	URL     string `json:"url"` // release page
	exeURL  string
	sumURL  string
}

type ghRelease struct {
	TagName string `json:"tag_name"`
	Body    string `json:"body"`
	HTMLURL string `json:"html_url"`
	Draft   bool   `json:"draft"`
	Assets  []struct {
		Name string `json:"name"`
		URL  string `json:"browser_download_url"`
	} `json:"assets"`
}

var httpClient = &http.Client{Timeout: 30 * time.Second}

// checkUpdate returns the latest release when it is newer than this build.
func checkUpdate() (*UpdateInfo, error) {
	if version == "dev" {
		return nil, nil
	}
	req, _ := http.NewRequest("GET", latestAPI, nil)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "AudioManagerPro/"+version)
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return nil, nil // no releases yet
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GitHub: %s", resp.Status)
	}
	var rel ghRelease
	if err := json.NewDecoder(resp.Body).Decode(&rel); err != nil {
		return nil, err
	}
	latest := strings.TrimPrefix(rel.TagName, "v")
	if rel.Draft || !newerVersion(latest, version) {
		return nil, nil
	}
	info := &UpdateInfo{Version: latest, Notes: rel.Body, URL: rel.HTMLURL}
	for _, a := range rel.Assets {
		switch a.Name {
		case assetName:
			info.exeURL = a.URL
		case assetName + ".sha256":
			info.sumURL = a.URL
		}
	}
	if info.exeURL == "" || info.sumURL == "" {
		return nil, nil // release still being built
	}
	return info, nil
}

// newerVersion reports whether a > b for dotted numeric versions.
func newerVersion(a, b string) bool {
	pa, pb := strings.Split(a, "."), strings.Split(b, ".")
	for i := 0; i < max(len(pa), len(pb)); i++ {
		var x, y int
		if i < len(pa) {
			x, _ = strconv.Atoi(pa[i])
		}
		if i < len(pb) {
			y, _ = strconv.Atoi(pb[i])
		}
		if x != y {
			return x > y
		}
	}
	return false
}

// installUpdate downloads the new exe, verifies its SHA-256, swaps it in
// place of the running one and starts it. The caller must quit afterwards.
func installUpdate(info *UpdateInfo, progress func(pct int)) error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	exe, _ = filepath.EvalSymlinks(exe)

	want, err := fetchChecksum(info.sumURL)
	if err != nil {
		return fmt.Errorf("контрольная сумма: %w", err)
	}

	tmp := exe + ".new"
	if err := download(info.exeURL, tmp, progress); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("загрузка: %w", err)
	}
	if got, err := fileSHA256(tmp); err != nil || got != want {
		os.Remove(tmp)
		return errors.New("файл обновления повреждён (SHA-256 не совпадает)")
	}

	// A running exe cannot be overwritten but can be renamed.
	old := exe + ".old"
	os.Remove(old)
	if err := os.Rename(exe, old); err != nil {
		os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, exe); err != nil {
		os.Rename(old, exe)
		return err
	}

	// The new process waits for us to exit so the single-instance lock is free.
	cmd := hiddenCmd(exe, fmt.Sprintf("%s%d", waitPidArg, os.Getpid()))
	cmd.Dir = filepath.Dir(exe)
	if err := cmd.Start(); err != nil {
		os.Rename(old, exe)
		return err
	}
	return nil
}

func fetchChecksum(url string) (string, error) {
	resp, err := httpClient.Get(url)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", errors.New(resp.Status)
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, 1024))
	if err != nil {
		return "", err
	}
	fields := strings.Fields(string(b))
	if len(fields) == 0 || len(fields[0]) != 64 {
		return "", errors.New("неверный формат")
	}
	return strings.ToLower(fields[0]), nil
}

func download(url, dst string, progress func(int)) error {
	client := &http.Client{Timeout: 10 * time.Minute}
	resp, err := client.Get(url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return errors.New(resp.Status)
	}
	f, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer f.Close()
	var done int64
	last := -1
	buf := make([]byte, 64*1024)
	for {
		n, rerr := resp.Body.Read(buf)
		if n > 0 {
			if _, err := f.Write(buf[:n]); err != nil {
				return err
			}
			done += int64(n)
			if resp.ContentLength > 0 {
				if pct := int(done * 100 / resp.ContentLength); pct != last {
					last = pct
					progress(pct)
				}
			}
		}
		if rerr == io.EOF {
			return nil
		}
		if rerr != nil {
			return rerr
		}
	}
}

func fileSHA256(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// afterUpdate runs first thing on start: waits for the previous instance
// (when launched by the updater) and removes the replaced exe.
func afterUpdate(args []string) {
	for _, a := range args {
		if pid, ok := strings.CutPrefix(a, waitPidArg); ok {
			if id, err := strconv.Atoi(pid); err == nil {
				if h, err := windows.OpenProcess(windows.SYNCHRONIZE, false, uint32(id)); err == nil {
					windows.WaitForSingleObject(h, 15000)
					windows.CloseHandle(h)
				}
			}
		}
	}
	if exe, err := os.Executable(); err == nil {
		os.Remove(exe + ".old")
	}
}
