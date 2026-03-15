package config

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"runtime"
	"time"
)

const updateRepo = "zanfiel/synapse"

type GHRelease struct {
	TagName string    `json:"tag_name"`
	Assets  []GHAsset `json:"assets"`
	Body    string    `json:"body"`
	Date    string    `json:"published_at"`
}

type GHAsset struct {
	Name string `json:"name"`
	URL  string `json:"browser_download_url"`
	Size int64  `json:"size"`
}

// CheckUpdate checks GitHub for a newer release.
func CheckUpdate(currentVersion string) (*GHRelease, error) {
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Get(fmt.Sprintf("https://api.github.com/repos/%s/releases/latest", updateRepo))
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("github %d", resp.StatusCode)
	}

	var release GHRelease
	if err := json.NewDecoder(resp.Body).Decode(&release); err != nil {
		return nil, err
	}

	if release.TagName == "v"+currentVersion || release.TagName == currentVersion {
		return nil, nil // already current
	}

	return &release, nil
}

// SelfUpdate downloads and replaces the current binary.
func SelfUpdate(release *GHRelease, currentVersion string) error {
	// Find the right asset for this OS/arch
	suffix := runtime.GOOS + "-" + runtime.GOARCH
	if runtime.GOOS == "windows" {
		suffix = "windows-amd64.exe"
	} else {
		suffix = runtime.GOOS + "-" + runtime.GOARCH
	}

	var asset *GHAsset
	for _, a := range release.Assets {
		if a.Name == "synapse-"+suffix || a.Name == "synapse.exe" || a.Name == "synapse" {
			asset = &a
			break
		}
	}
	if asset == nil {
		return fmt.Errorf("no binary found for %s/%s in release %s", runtime.GOOS, runtime.GOARCH, release.TagName)
	}

	fmt.Printf("Downloading %s (%d MB)...\n", asset.Name, asset.Size/1024/1024)

	client := &http.Client{Timeout: 120 * time.Second}
	resp, err := client.Get(asset.URL)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	// Write to temp file
	exe, _ := os.Executable()
	tmp := exe + ".new"
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0755)
	if err != nil {
		return err
	}

	if _, err := io.Copy(f, resp.Body); err != nil {
		f.Close()
		os.Remove(tmp)
		return err
	}
	f.Close()

	// Replace current binary
	backup := exe + ".bak"
	os.Remove(backup)
	if err := os.Rename(exe, backup); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("backup current binary: %w", err)
	}
	if err := os.Rename(tmp, exe); err != nil {
		os.Rename(backup, exe) // rollback
		return fmt.Errorf("install new binary: %w", err)
	}

	os.Remove(backup)
	fmt.Printf("✓ Updated to %s\n", release.TagName)
	return nil
}
