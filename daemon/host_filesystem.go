package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"
)

const hostFileSystemBrowseEntryLimit = 500

type hostFileSystemBrowseParams struct {
	Target string     `json:"target"`
	Path   string     `json:"path,omitempty"`
	SSH    *SSHConfig `json:"ssh,omitempty"`
}

type hostFileSystemRoot struct {
	ID    string `json:"id"`
	Label string `json:"label"`
	Path  string `json:"path"`
	Kind  string `json:"kind"`
}

type hostFileSystemEntry struct {
	Name    string `json:"name"`
	Path    string `json:"path"`
	Kind    string `json:"kind"`
	Size    int64  `json:"size,omitempty"`
	ModTime string `json:"mod_time,omitempty"`
}

type hostFileSystemBrowseResult struct {
	Target     string                `json:"target"`
	Platform   string                `json:"platform"`
	Separator  string                `json:"separator"`
	Path       string                `json:"path"`
	ParentPath string                `json:"parent_path,omitempty"`
	Roots      []hostFileSystemRoot  `json:"roots"`
	Entries    []hostFileSystemEntry `json:"entries"`
	Truncated  bool                  `json:"truncated,omitempty"`
}

func (a *app) handleHostFileSystemBrowse(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	var params hostFileSystemBrowseParams
	if err := decodeJSON(r.Body, &params); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	result, err := a.browseHostFileSystem(r.Context(), params)
	if err != nil {
		writeActionError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (a *app) browseHostFileSystem(ctx context.Context, params hostFileSystemBrowseParams) (hostFileSystemBrowseResult, error) {
	switch strings.TrimSpace(params.Target) {
	case "local", "":
		return browseLocalHostFileSystem(params.Path)
	case "ssh":
		if a.ssh == nil {
			return hostFileSystemBrowseResult{}, newActionError(http.StatusNotImplemented, "ssh_unavailable", "ssh manager unavailable")
		}
		return a.browseSSHHostFileSystem(ctx, params)
	default:
		return hostFileSystemBrowseResult{}, newActionError(http.StatusBadRequest, "host_fs_target_invalid", "target must be local or ssh")
	}
}

func browseLocalHostFileSystem(path string) (hostFileSystemBrowseResult, error) {
	roots := localHostFileSystemRoots()
	target := strings.TrimSpace(path)
	if target == "" {
		if len(roots) > 0 {
			target = roots[0].Path
		} else {
			target = string(filepath.Separator)
		}
	}
	target = filepath.Clean(target)
	if !filepath.IsAbs(target) {
		return hostFileSystemBrowseResult{}, newActionError(http.StatusBadRequest, "host_fs_path_not_absolute", "path must be absolute")
	}
	info, err := os.Stat(target)
	if err != nil {
		return hostFileSystemBrowseResult{}, newActionError(http.StatusBadRequest, "host_fs_path_unreadable", err.Error())
	}
	if !info.IsDir() {
		return hostFileSystemBrowseResult{}, newActionError(http.StatusBadRequest, "host_fs_path_not_directory", "path is not a directory")
	}
	entries, truncated, err := localHostFileSystemEntries(target)
	if err != nil {
		return hostFileSystemBrowseResult{}, newActionError(http.StatusBadRequest, "host_fs_list_failed", err.Error())
	}
	return hostFileSystemBrowseResult{
		Target:     "local",
		Platform:   runtime.GOOS,
		Separator:  string(filepath.Separator),
		Path:       target,
		ParentPath: localHostFileSystemParent(target),
		Roots:      roots,
		Entries:    entries,
		Truncated:  truncated,
	}, nil
}

func localHostFileSystemRoots() []hostFileSystemRoot {
	roots := []hostFileSystemRoot{}
	if home, err := os.UserHomeDir(); err == nil && strings.TrimSpace(home) != "" {
		roots = append(roots, hostFileSystemRoot{ID: "home", Label: "Home", Path: filepath.Clean(home), Kind: "home"})
	}
	if runtime.GOOS == "windows" {
		for drive := 'A'; drive <= 'Z'; drive++ {
			path := fmt.Sprintf("%c:\\", drive)
			if _, err := os.Stat(path); err == nil {
				roots = append(roots, hostFileSystemRoot{ID: strings.ToLower(string(drive)), Label: path, Path: path, Kind: "drive"})
			}
		}
		return dedupeHostFileSystemRoots(roots)
	}
	roots = append(roots, hostFileSystemRoot{ID: "root", Label: "/", Path: "/", Kind: "root"})
	if runtime.GOOS == "darwin" {
		if _, err := os.Stat("/Volumes"); err == nil {
			roots = append(roots, hostFileSystemRoot{ID: "volumes", Label: "Volumes", Path: "/Volumes", Kind: "volume"})
		}
	}
	return dedupeHostFileSystemRoots(roots)
}

func dedupeHostFileSystemRoots(roots []hostFileSystemRoot) []hostFileSystemRoot {
	seen := map[string]bool{}
	out := make([]hostFileSystemRoot, 0, len(roots))
	for _, root := range roots {
		root.Path = filepath.Clean(strings.TrimSpace(root.Path))
		if root.Path == "" || seen[root.Path] {
			continue
		}
		seen[root.Path] = true
		out = append(out, root)
	}
	return out
}

func localHostFileSystemEntries(path string) ([]hostFileSystemEntry, bool, error) {
	raw, err := os.ReadDir(path)
	if err != nil {
		return nil, false, err
	}
	entries := make([]hostFileSystemEntry, 0, len(raw))
	for _, entry := range raw {
		info, err := entry.Info()
		if err != nil {
			continue
		}
		kind := "file"
		if entry.Type()&os.ModeSymlink != 0 {
			kind = "symlink"
		} else if entry.IsDir() {
			kind = "dir"
		}
		entries = append(entries, hostFileSystemEntry{
			Name:    entry.Name(),
			Path:    filepath.Join(path, entry.Name()),
			Kind:    kind,
			Size:    info.Size(),
			ModTime: info.ModTime().UTC().Format(time.RFC3339),
		})
	}
	return sortAndLimitHostFileSystemEntries(entries)
}

func localHostFileSystemParent(path string) string {
	parent := filepath.Dir(filepath.Clean(path))
	if parent == path || parent == "." {
		return ""
	}
	return parent
}

func (a *app) browseSSHHostFileSystem(ctx context.Context, params hostFileSystemBrowseParams) (hostFileSystemBrowseResult, error) {
	if params.SSH == nil {
		return hostFileSystemBrowseResult{}, newActionError(http.StatusBadRequest, "ssh_config_required", "ssh config required")
	}
	ssh := *params.SSH
	ssh.Endpoint = strings.TrimSpace(ssh.Endpoint)
	ssh.RemoteCWD = remotePathClean(ssh.RemoteCWD)
	if ssh.Endpoint == "" {
		return hostFileSystemBrowseResult{}, newActionError(http.StatusBadRequest, "ssh_endpoint_required", "ssh endpoint required")
	}
	if ssh.Port <= 0 {
		ssh.Port = 22
	}
	path := remotePathClean(params.Path)
	if path == "" {
		path = ssh.RemoteCWD
	}
	if path == "" {
		path = "/"
	}
	if !remotePathIsAbs(path) {
		return hostFileSystemBrowseResult{}, newActionError(http.StatusBadRequest, "host_fs_path_not_absolute", "path must be absolute")
	}
	ws := Workspace{
		ID:     "fsbrowse_" + randomID(12),
		Name:   "Filesystem Browse",
		Target: "ssh",
		Agent:  AgentCodex,
		SSH:    &ssh,
	}
	var raw []map[string]any
	if err := a.ssh.callBrowse(ctx, ws, "list", map[string]any{"path": path}, &raw); err != nil {
		return hostFileSystemBrowseResult{}, newActionError(http.StatusBadRequest, "host_fs_list_failed", err.Error())
	}
	entries := make([]hostFileSystemEntry, 0, len(raw))
	for _, item := range raw {
		kind := "file"
		if boolValue(item["is_dir"]) {
			kind = "dir"
		}
		entries = append(entries, hostFileSystemEntry{
			Name:    stringValue(item["name"]),
			Path:    remotePathClean(stringValue(item["path"])),
			Kind:    kind,
			Size:    int64(numberValue(item["size"])),
			ModTime: stringValue(item["modified"]),
		})
	}
	entries, truncated, err := sortAndLimitHostFileSystemEntries(entries)
	if err != nil {
		return hostFileSystemBrowseResult{}, err
	}
	return hostFileSystemBrowseResult{
		Target:     "ssh",
		Platform:   "posix",
		Separator:  "/",
		Path:       path,
		ParentPath: sshHostFileSystemParent(path),
		Roots:      sshHostFileSystemRoots(ssh.RemoteCWD),
		Entries:    entries,
		Truncated:  truncated,
	}, nil
}

func sshHostFileSystemRoots(remoteCWD string) []hostFileSystemRoot {
	roots := []hostFileSystemRoot{{ID: "root", Label: "/", Path: "/", Kind: "root"}}
	remoteCWD = remotePathClean(remoteCWD)
	if remoteCWD != "" && remoteCWD != "/" {
		roots = append([]hostFileSystemRoot{{ID: "cwd", Label: remotePathBase(remoteCWD), Path: remoteCWD, Kind: "custom"}}, roots...)
	}
	return roots
}

func sshHostFileSystemParent(path string) string {
	path = remotePathClean(path)
	parent := remotePathDir(path)
	if parent == path || parent == "." {
		return ""
	}
	return parent
}

func sortAndLimitHostFileSystemEntries(entries []hostFileSystemEntry) ([]hostFileSystemEntry, bool, error) {
	sort.SliceStable(entries, func(i, j int) bool {
		if entries[i].Kind != entries[j].Kind {
			return entries[i].Kind == "dir"
		}
		return strings.ToLower(entries[i].Name) < strings.ToLower(entries[j].Name)
	})
	if len(entries) > hostFileSystemBrowseEntryLimit {
		return entries[:hostFileSystemBrowseEntryLimit], true, nil
	}
	return entries, false, nil
}
