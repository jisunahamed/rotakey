package app

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

var (
	Version     = "0.2.2"
	BuildCommit = "unknown"
	BuildTime   = "unknown"
)

const (
	releaseAPI = "https://api.github.com/repos/jisunahamed/rotakey/releases/latest"
	releaseURL = "https://github.com/jisunahamed/rotakey/releases"
)

type versionInfo struct {
	CurrentVersion  string `json:"current_version"`
	Commit          string `json:"commit"`
	BuildTime       string `json:"build_time"`
	LatestVersion   string `json:"latest_version,omitempty"`
	UpdateAvailable bool   `json:"update_available"`
	ReleaseURL      string `json:"release_url"`
	PublishedAt     string `json:"published_at,omitempty"`
	CheckError      string `json:"check_error,omitempty"`
}

type releaseCache struct {
	mu        sync.Mutex
	checkedAt time.Time
	latest    string
	url       string
	published string
	err       string
}

func (s *Server) handleVersion(w http.ResponseWriter, r *http.Request) {
	latest, url, published, checkErr := s.release.latestRelease(r.Context())
	writeJSON(w, http.StatusOK, versionInfo{
		CurrentVersion: Version, Commit: BuildCommit, BuildTime: BuildTime,
		LatestVersion: latest, UpdateAvailable: compareVersions(latest, Version) > 0,
		ReleaseURL: valueOr(url, releaseURL), PublishedAt: published, CheckError: checkErr,
	})
}

func (c *releaseCache) latestRelease(ctx context.Context) (string, string, string, string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if time.Since(c.checkedAt) < time.Hour {
		return c.latest, c.url, c.published, c.err
	}
	c.checkedAt = time.Now()
	requestCtx, cancel := context.WithTimeout(ctx, 4*time.Second)
	defer cancel()
	request, _ := http.NewRequestWithContext(requestCtx, http.MethodGet, releaseAPI, nil)
	request.Header.Set("Accept", "application/vnd.github+json")
	request.Header.Set("User-Agent", "Rotakey/"+Version)
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		c.err = "Release check is temporarily unavailable."
		return c.latest, c.url, c.published, c.err
	}
	defer response.Body.Close()
	if response.StatusCode == http.StatusNotFound {
		c.err = "No public release has been published yet."
		return c.latest, c.url, c.published, c.err
	}
	if response.StatusCode != http.StatusOK {
		c.err = "GitHub release check returned HTTP " + strconv.Itoa(response.StatusCode) + "."
		return c.latest, c.url, c.published, c.err
	}
	var payload struct {
		TagName     string `json:"tag_name"`
		HTMLURL     string `json:"html_url"`
		PublishedAt string `json:"published_at"`
	}
	if json.NewDecoder(response.Body).Decode(&payload) != nil {
		c.err = "GitHub returned an invalid release response."
		return c.latest, c.url, c.published, c.err
	}
	c.latest = strings.TrimPrefix(strings.TrimSpace(payload.TagName), "v")
	c.url, c.published, c.err = payload.HTMLURL, payload.PublishedAt, ""
	return c.latest, c.url, c.published, c.err
}

func compareVersions(left, right string) int {
	parse := func(value string) [3]int {
		value = strings.TrimPrefix(strings.TrimSpace(value), "v")
		value = strings.SplitN(value, "-", 2)[0]
		parts := strings.Split(value, ".")
		var out [3]int
		for index := 0; index < len(parts) && index < len(out); index++ {
			out[index], _ = strconv.Atoi(parts[index])
		}
		return out
	}
	l, r := parse(left), parse(right)
	for index := range l {
		if l[index] > r[index] {
			return 1
		}
		if l[index] < r[index] {
			return -1
		}
	}
	return 0
}
