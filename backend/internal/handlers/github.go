package handlers

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	neturl "net/url"
	"strings"
	"time"
)

const githubArchiveProxyTimeout = 45 * time.Second

func ProxyGitHubRepoArchive(w http.ResponseWriter, r *http.Request) {
	owner := strings.TrimSpace(r.URL.Query().Get("owner"))
	repo := strings.TrimSpace(r.URL.Query().Get("repo"))
	ref := strings.TrimSpace(r.URL.Query().Get("ref"))
	log.Printf("[canvas-github] event=request owner=%s repo=%s ref=%s remote=%s", owner, repo, ref, r.RemoteAddr)

	if !isValidGitHubRepoSegment(owner) || !isValidGitHubRepoSegment(repo) {
		log.Printf("[canvas-github] event=invalid-owner-repo owner=%s repo=%s remote=%s", owner, repo, r.RemoteAddr)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{
			"error": "owner and repo query params are required",
		})
		return
	}
	if ref != "" && !isValidGitHubRef(ref) {
		log.Printf("[canvas-github] event=invalid-ref owner=%s repo=%s ref=%s remote=%s", owner, repo, ref, r.RemoteAddr)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{
			"error": "ref contains unsupported characters",
		})
		return
	}

	archiveURL := fmt.Sprintf(
		"https://api.github.com/repos/%s/%s/zipball",
		neturl.PathEscape(owner),
		neturl.PathEscape(repo),
	)
	if ref != "" {
		archiveURL = fmt.Sprintf("%s/%s", archiveURL, neturl.PathEscape(ref))
	}

	request, err := http.NewRequestWithContext(r.Context(), http.MethodGet, archiveURL, nil)
	if err != nil {
		log.Printf("[canvas-github] event=request-build-failed owner=%s repo=%s ref=%s err=%v", owner, repo, ref, err)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(map[string]string{
			"error": "Failed to prepare GitHub import request",
		})
		return
	}
	request.Header.Set("Accept", "application/vnd.github+json")
	request.Header.Set("User-Agent", "ConverseCanvas/1.0")

	client := &http.Client{Timeout: githubArchiveProxyTimeout}
	log.Printf("[canvas-github] event=github-fetch owner=%s repo=%s ref=%s url=%s", owner, repo, ref, archiveURL)
	response, err := client.Do(request)
	if err != nil {
		log.Printf("[canvas-github] event=github-fetch-error owner=%s repo=%s ref=%s err=%v", owner, repo, ref, err)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadGateway)
		_ = json.NewEncoder(w).Encode(map[string]string{
			"error": "Unable to fetch repository archive from GitHub",
		})
		return
	}
	defer response.Body.Close()

	if response.StatusCode != http.StatusOK {
		log.Printf("[canvas-github] event=github-fetch-non-ok owner=%s repo=%s ref=%s status=%d", owner, repo, ref, response.StatusCode)
		errorMessage := fmt.Sprintf("GitHub import failed (%d)", response.StatusCode)
		if body, readErr := io.ReadAll(io.LimitReader(response.Body, 4096)); readErr == nil {
			trimmed := strings.TrimSpace(string(body))
			if trimmed != "" {
				errorMessage = trimmed
			}
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(response.StatusCode)
		_ = json.NewEncoder(w).Encode(map[string]string{
			"error": errorMessage,
		})
		return
	}

	downloadName := fmt.Sprintf("%s-%s.zip", owner, repo)
	if ref != "" {
		safeRef := strings.NewReplacer("/", "-", "\\", "-", " ", "-").Replace(ref)
		downloadName = fmt.Sprintf("%s-%s-%s.zip", owner, repo, safeRef)
	}

	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", downloadName))
	w.WriteHeader(http.StatusOK)
	written, copyErr := io.Copy(w, response.Body)
	if copyErr != nil {
		log.Printf("[canvas-github] event=response-copy-error owner=%s repo=%s ref=%s err=%v", owner, repo, ref, copyErr)
		return
	}
	log.Printf("[canvas-github] event=response-ok owner=%s repo=%s ref=%s bytes=%d", owner, repo, ref, written)
}

func isValidGitHubRepoSegment(value string) bool {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return false
	}
	return !strings.ContainsAny(trimmed, `/\?#&`)
}

func isValidGitHubRef(value string) bool {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return false
	}
	if strings.HasPrefix(trimmed, ".") || strings.HasSuffix(trimmed, ".") {
		return false
	}
	if strings.Contains(trimmed, "..") {
		return false
	}
	return !strings.ContainsAny(trimmed, `\?#&`)
}
