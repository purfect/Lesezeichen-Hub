package main

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"

	_ "modernc.org/sqlite"
)

func validateURL(raw string) error {
	if raw == "" {
		return fmt.Errorf("url ist erforderlich")
	}
	u, err := url.ParseRequestURI(raw)
	if err != nil {
		return fmt.Errorf("url ist ungueltig")
	}
	if u.Scheme == "" && strings.HasPrefix(u.Path, "/modules/") {
		return nil
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("url muss mit http:// oder https:// beginnen")
	}
	return nil
}

func validateRemoteModuleArchiveURL(raw string) error {
	_, _, err := normalizeRemoteModuleSourceURL(raw)
	return err
}

func normalizeRemoteModuleSourceURL(raw string) (string, string, error) {
	sourceURL := strings.TrimSpace(raw)
	u, err := url.ParseRequestURI(sourceURL)
	if err != nil || u.Host == "" {
		return "", "", fmt.Errorf("url ist ungueltig")
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return "", "", fmt.Errorf("url muss mit http:// oder https:// beginnen")
	}
	if strings.HasSuffix(strings.ToLower(u.Path), ".zip") {
		return sourceURL, sourceURL, nil
	}
	if strings.EqualFold(u.Host, "github.com") || strings.EqualFold(u.Host, "www.github.com") {
		downloadURL, displayURL, ok := githubArchiveURLFromRepoURL(u)
		if ok {
			return downloadURL, displayURL, nil
		}
	}
	return "", "", fmt.Errorf("url muss direkt auf ein zip-archiv oder ein github-repository zeigen")
}

func githubArchiveURLFromRepoURL(u *url.URL) (string, string, bool) {
	parts := strings.Split(strings.Trim(u.EscapedPath(), "/"), "/")
	if len(parts) < 2 || parts[0] == "" || parts[1] == "" {
		return "", "", false
	}
	owner, err := url.PathUnescape(parts[0])
	if err != nil {
		return "", "", false
	}
	repo, err := url.PathUnescape(parts[1])
	if err != nil {
		return "", "", false
	}
	repo = strings.TrimSuffix(repo, ".git")
	if owner == "" || repo == "" {
		return "", "", false
	}
	branch := ""
	if len(parts) >= 4 && parts[2] == "tree" {
		branchParts := make([]string, 0, len(parts)-3)
		for _, part := range parts[3:] {
			decoded, err := url.PathUnescape(part)
			if err != nil || decoded == "" {
				return "", "", false
			}
			branchParts = append(branchParts, decoded)
		}
		branch = strings.Join(branchParts, "/")
	} else if len(parts) > 2 {
		return "", "", false
	}
	baseRepoURL := "https://github.com/" + url.PathEscape(owner) + "/" + url.PathEscape(repo)
	// zipball ohne ref liefert den tatsaechlichen default-branch (main, master, ...)
	apiURL := "https://api.github.com/repos/" + url.PathEscape(owner) + "/" + url.PathEscape(repo) + "/zipball"
	if branch != "" {
		apiURL += "/" + url.PathEscape(branch)
	}
	return apiURL, baseRepoURL, true
}

func safeModuleDirectoryName(name string) string {
	name = strings.TrimSpace(strings.ToLower(name))
	var builder strings.Builder
	lastDash := false
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			builder.WriteRune(r)
			lastDash = false
		case r == '.', r == '_':
			builder.WriteRune(r)
			lastDash = false
		default:
			if !lastDash {
				builder.WriteByte('-')
				lastDash = true
			}
		}
	}
	cleaned := strings.Trim(builder.String(), ".-_")
	if cleaned == "" {
		return "module"
	}
	return cleaned
}

func (app *application) findDuplicateBookmark(ctx context.Context, groupID int64, rawURL string, excludeID int64) (string, error) {
	targetURL := normalizeBookmarkURL(rawURL)
	query := `
		SELECT bookmarks.id, bookmarks.title, bookmarks.url, COALESCE(groups.name, ?)
		FROM bookmarks
		LEFT JOIN groups ON groups.id = bookmarks.group_id
		WHERE bookmarks.group_id = ? AND bookmarks.id != ?`
	args := []any{unsortedGroupName, groupID, excludeID}
	if groupID <= 0 {
		query = `
		SELECT bookmarks.id, bookmarks.title, bookmarks.url, COALESCE(groups.name, ?)
		FROM bookmarks
		LEFT JOIN groups ON groups.id = bookmarks.group_id
		WHERE bookmarks.group_id IS NULL AND bookmarks.id != ?`
		args = []any{unsortedGroupName, excludeID}
	}
	rows, err := app.db.QueryContext(ctx, query, args...)
	if err != nil {
		return "", err
	}
	defer rows.Close()

	for rows.Next() {
		var id int64
		var title, candidateURL, groupName string
		if err := rows.Scan(&id, &title, &candidateURL, &groupName); err != nil {
			return "", err
		}
		if normalizeBookmarkURL(candidateURL) == targetURL {
			return fmt.Sprintf("%q in Gruppe %q", title, groupName), nil
		}
	}
	return "", rows.Err()
}

func normalizeBookmarkURL(raw string) string {
	value := strings.TrimSpace(raw)
	u, err := url.Parse(value)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") {
		return value
	}

	u.Scheme = strings.ToLower(u.Scheme)
	u.Host = strings.ToLower(u.Host)
	if (u.Scheme == "http" && u.Port() == "80") || (u.Scheme == "https" && u.Port() == "443") {
		u.Host = u.Hostname()
	}
	if u.Path == "" {
		u.Path = "/"
	}
	if u.Path != "/" {
		u.Path = strings.TrimRight(u.Path, "/")
	}
	return u.String()
}

func boolToInt(value bool) int {
	if value {
		return 1
	}
	return 0
}

func envBool(key string, fallback bool) bool {
	raw, exists := os.LookupEnv(key)
	if !exists {
		return fallback
	}
	parsed, err := strconv.ParseBool(strings.TrimSpace(raw))
	if err != nil {
		return fallback
	}
	return parsed
}

func parseTags(raw string) []string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return []string{}
	}

	parts := strings.Split(raw, ",")
	items := make([]string, 0, len(parts))
	seen := make(map[string]struct{})
	for _, p := range parts {
		tag := strings.TrimSpace(p)
		if tag == "" {
			continue
		}
		key := strings.ToLower(tag)
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		items = append(items, tag)
	}
	return items
}

func serializeTags(tags []string) string {
	if len(tags) == 0 {
		return ""
	}

	items := make([]string, 0, len(tags))
	seen := make(map[string]struct{})
	for _, t := range tags {
		tag := strings.TrimSpace(t)
		if tag == "" {
			continue
		}
		key := strings.ToLower(tag)
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		items = append(items, tag)
	}

	return strings.Join(items, ",")
}

func methodNotAllowed(w http.ResponseWriter) {
	writeJSON(w, http.StatusMethodNotAllowed, apiError{Error: "method not allowed"})
}

func writeErr(w http.ResponseWriter, status int, err error) {
	writeJSON(w, status, apiError{Error: err.Error()})
}
