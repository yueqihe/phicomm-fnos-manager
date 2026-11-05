package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"
)

const projectURL = "https://github.com/yueqihe/phicomm-fnos-manager"
const latestReleaseAPI = "https://api.github.com/repos/yueqihe/phicomm-fnos-manager/releases/latest"

func packagePlatform(arch string) (string, string) {
	switch arch {
	case "amd64":
		return "x86", "x86-64"
	case "arm64":
		return "arm", "ARM64"
	default:
		return "", arch
	}
}

func applicationAbout() Obj {
	_, architecture := packagePlatform(runtime.GOARCH)
	return Obj{"name": "IoT 设备管理", "version": version, "architecture": architecture,
		"projectUrl": projectURL, "releasesUrl": projectURL + "/releases", "author": "情深深雨蒙蒙",
		"authorUrl": "https://github.com/yueqihe", "upstreamUrl": "https://github.com/phicomm-io/"}
}

var stableVersionPattern = regexp.MustCompile(`^v?(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$`)

func stableVersion(value string) ([3]uint64, bool) {
	var result [3]uint64
	match := stableVersionPattern.FindStringSubmatch(value)
	if match == nil {
		return result, false
	}
	for i := range result {
		n, err := strconv.ParseUint(match[i+1], 10, 64)
		if err != nil {
			return result, false
		}
		result[i] = n
	}
	return result, true
}

type githubRelease struct {
	Tag        string    `json:"tag_name"`
	Body       string    `json:"body"`
	Draft      bool      `json:"draft"`
	Prerelease bool      `json:"prerelease"`
	Published  time.Time `json:"published_at"`
	Assets     []struct {
		Name  string `json:"name"`
		URL   string `json:"browser_download_url"`
		State string `json:"state"`
		Size  int64  `json:"size"`
	} `json:"assets"`
}

type updateResult struct {
	Status         string     `json:"status"`
	Message        string     `json:"message"`
	CurrentVersion string     `json:"currentVersion"`
	LatestVersion  string     `json:"latestVersion,omitempty"`
	CheckedAt      time.Time  `json:"checkedAt"`
	PublishedAt    *time.Time `json:"publishedAt,omitempty"`
	ReleaseURL     string     `json:"releaseUrl"`
	Notes          string     `json:"notes,omitempty"`
	DownloadURL    string     `json:"downloadUrl,omitempty"`
	PackageName    string     `json:"packageName,omitempty"`
	Architecture   string     `json:"architecture"`
	Source         string     `json:"source,omitempty"`
}

func releaseResult(release githubRelease, current, arch string) (updateResult, error) {
	if release.Published.IsZero() {
		return updateResult{}, fmt.Errorf("GitHub 返回的正式版本信息不完整，请查看发布页面")
	}
	return releaseDetails(release, current, arch)
}

func releaseDetails(release githubRelease, current, arch string) (updateResult, error) {
	latest, valid := stableVersion(release.Tag)
	installed, validCurrent := stableVersion(current)
	if !valid || !validCurrent || release.Draft || release.Prerelease {
		return updateResult{}, fmt.Errorf("GitHub 返回的正式版本信息不完整，请查看发布页面")
	}
	platform, architecture := packagePlatform(arch)
	result := updateResult{Status: "current", Message: "当前已是最新正式版", CurrentVersion: current,
		LatestVersion: strings.TrimPrefix(release.Tag, "v"),
		ReleaseURL:    projectURL + "/releases/tag/" + release.Tag, Notes: release.Body, Architecture: architecture}
	if !release.Published.IsZero() {
		result.PublishedAt = &release.Published
	}
	for i := range latest {
		if latest[i] > installed[i] {
			result.Status, result.Message = "update", "发现新版本"
			break
		}
		if latest[i] < installed[i] {
			result.Status, result.Message = "ahead", "当前版本高于 GitHub 最新正式版"
			break
		}
	}
	if platform != "" {
		name := "phicomm-manager-" + result.LatestVersion + "-" + platform + ".fpk"
		url := projectURL + "/releases/download/" + release.Tag + "/" + name
		for _, asset := range release.Assets {
			// Only link the expected FPK in this repository's release; metadata
			// cannot turn the download button into an arbitrary external link.
			if asset.Name == name && asset.URL == url && asset.State == "uploaded" && asset.Size > 0 {
				result.DownloadURL, result.PackageName = url, name
				break
			}
		}
	}
	return result, nil
}

type updateChecker struct {
	mu      sync.Mutex
	loading chan struct{}
	result  updateResult
	// Test injection stays inside the process; users cannot override the source.
	client *http.Client
	now    func() time.Time
}

func (c *updateChecker) clock() time.Time {
	if c.now != nil {
		return c.now()
	}
	return time.Now()
}

func (c *updateChecker) check(ctx context.Context, force bool) (updateResult, error) {
	for {
		if err := ctx.Err(); err != nil {
			return updateResult{}, err
		}
		c.mu.Lock()
		ttl := 15 * time.Minute
		if force {
			ttl = time.Minute
		}
		if c.result.Status == "error" {
			ttl = 30 * time.Second
		}
		age := c.clock().Sub(c.result.CheckedAt)
		if !c.result.CheckedAt.IsZero() && age >= 0 && age < ttl {
			result := c.result
			c.mu.Unlock()
			return result, nil
		}
		if done := c.loading; done != nil {
			c.mu.Unlock()
			select {
			case <-ctx.Done():
				return updateResult{}, ctx.Err()
			case <-done:
				continue
			}
		}
		done := make(chan struct{})
		c.loading = done
		c.mu.Unlock()
		result := c.fetch(ctx)
		c.mu.Lock()
		if ctx.Err() == nil {
			c.result = result
		}
		c.loading = nil
		close(done)
		c.mu.Unlock()
		return result, ctx.Err()
	}
}

func (c *updateChecker) fetch(ctx context.Context) updateResult {
	_, architecture := packagePlatform(runtime.GOARCH)
	failed := updateResult{Status: "error", Message: "暂时无法连接 GitHub，请稍后重试或查看发布页面", CurrentVersion: version,
		CheckedAt: c.clock().UTC(), ReleaseURL: projectURL + "/releases", Architecture: architecture}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	apiContext, cancelAPI := context.WithTimeout(ctx, 5*time.Second)
	defer cancelAPI()
	request, err := http.NewRequestWithContext(apiContext, "GET", latestReleaseAPI, nil)
	if err != nil {
		return failed
	}
	request.Header.Set("Accept", "application/vnd.github+json")
	request.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	request.Header.Set("User-Agent", "IoT-fnOS-Manager/"+version)
	clientConfig := http.Client{Timeout: 10 * time.Second}
	if c.client != nil {
		clientConfig = *c.client
	}
	clientConfig.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	client := &clientConfig
	response, err := client.Do(request)
	if err != nil {
		return c.releasePageFallback(ctx, client, failed)
	}
	defer response.Body.Close()
	switch response.StatusCode {
	case http.StatusOK:
	case http.StatusNotFound:
		failed.Message = "GitHub 暂无可用的正式版本，请查看发布页面"
		return failed
	case http.StatusForbidden, http.StatusTooManyRequests:
		failed.Message = "GitHub 暂时限制了检查请求，请稍后重试"
		return c.releasePageFallback(ctx, client, failed)
	default:
		return c.releasePageFallback(ctx, client, failed)
	}
	const maxBytes = 2 << 20
	body, err := io.ReadAll(io.LimitReader(response.Body, maxBytes+1))
	if err != nil || len(body) > maxBytes {
		return failed
	}
	var release githubRelease
	if err = json.Unmarshal(body, &release); err != nil {
		failed.Message = "无法读取 GitHub 版本信息，请稍后重试"
		return failed
	}
	result, err := releaseResult(release, version, runtime.GOARCH)
	if err != nil {
		failed.Message = err.Error()
		return failed
	}
	result.CheckedAt = c.clock().UTC()
	result.Source = "api"
	return result
}

// GitHub's latest-release redirect excludes drafts and prereleases without
// consuming the REST API's shared anonymous quota. It has no notes/asset list,
// so verify the exact package URL separately and link to the release for notes.
func (c *updateChecker) releasePageFallback(ctx context.Context, client *http.Client, failed updateResult) updateResult {
	request, err := http.NewRequestWithContext(ctx, "HEAD", projectURL+"/releases/latest", nil)
	if err != nil {
		return failed
	}
	request.Header.Set("User-Agent", "IoT-fnOS-Manager/"+version)
	response, err := client.Do(request)
	if err != nil {
		return failed
	}
	response.Body.Close()
	if response.StatusCode != http.StatusFound && response.StatusCode != http.StatusMovedPermanently && response.StatusCode != http.StatusTemporaryRedirect && response.StatusCode != http.StatusPermanentRedirect {
		return failed
	}
	location := response.Header.Get("Location")
	prefix := projectURL + "/releases/tag/"
	if !strings.HasPrefix(location, prefix) {
		return failed
	}
	tag := strings.TrimPrefix(location, prefix)
	result, err := releaseDetails(githubRelease{Tag: tag}, version, runtime.GOARCH)
	if err != nil {
		return failed
	}
	result.Source, result.CheckedAt = "release-page", c.clock().UTC()
	platform, _ := packagePlatform(runtime.GOARCH)
	if result.Status != "update" || platform == "" {
		return result
	}
	name := "phicomm-manager-" + result.LatestVersion + "-" + platform + ".fpk"
	download := projectURL + "/releases/download/" + tag + "/" + name
	request, err = http.NewRequestWithContext(ctx, "HEAD", download, nil)
	if err != nil {
		return result
	}
	request.Header.Set("User-Agent", "IoT-fnOS-Manager/"+version)
	response, err = client.Do(request)
	if err != nil {
		return result
	}
	response.Body.Close()
	destination, err := url.Parse(response.Header.Get("Location"))
	if err == nil && response.StatusCode == http.StatusFound && destination.Scheme == "https" && destination.User == nil && destination.Port() == "" && destination.Hostname() == "release-assets.githubusercontent.com" {
		result.PackageName, result.DownloadURL = name, download
	}
	return result
}
