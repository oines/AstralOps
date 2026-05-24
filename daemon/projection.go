package main

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"time"
)

type projectionManifest struct {
	Files map[string]projectionFile `json:"files"`
}

type projectionFile struct {
	RemotePath   string `json:"remote_path"`
	LocalPath    string `json:"local_path"`
	MTime        string `json:"mtime,omitempty"`
	Size         int64  `json:"size,omitempty"`
	Dirty        bool   `json:"dirty"`
	LastHydrated string `json:"last_hydrated,omitempty"`
	LastPushed   string `json:"last_pushed,omitempty"`
}

func (a *app) handleProjectionAction(w http.ResponseWriter, r *http.Request, parts []string) {
	ws, ok := a.store.getWorkspace(parts[0])
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "workspace not found"})
		return
	}
	if ws.Target != "ssh" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "projection is only used for ssh workspaces"})
		return
	}
	if len(parts) == 3 && parts[2] == "files" && r.Method == http.MethodGet {
		manifest := a.loadProjectionManifest(ws)
		out := make([]projectionFile, 0, len(manifest.Files))
		for _, file := range manifest.Files {
			out = append(out, file)
		}
		writeJSON(w, http.StatusOK, map[string]any{"files": out})
		return
	}
	if len(parts) == 3 && parts[2] == "hydrate" && r.Method == http.MethodPost {
		var req struct {
			Path      string `json:"path"`
			Directory bool   `json:"directory"`
		}
		if err := decodeJSON(r.Body, &req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		local, remote, err := a.hydrateClaudePath(r.Context(), ws, req.Path, req.Directory)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		file := a.recordProjectionFile(ws, remote, local, false, true)
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "file": file})
		return
	}
	if len(parts) == 3 && parts[2] == "push" && r.Method == http.MethodPost {
		var req struct {
			Path string `json:"path"`
		}
		if err := decodeJSON(r.Body, &req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		local, remote, err := a.projectedLocalPath(ws, req.Path)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		body, err := os.ReadFile(local)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		proxy, _, err := a.ssh.proxyFor(r.Context(), ws)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		if err := proxy.call(r.Context(), "write", map[string]any{"path": remote, "content": string(body)}, nil); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		file := a.recordProjectionFile(ws, remote, local, false, false)
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "file": file})
		return
	}
	w.WriteHeader(http.StatusNotFound)
}

func (a *app) projectionManifestPath(ws Workspace) string {
	return filepath.Join(a.store.dataDir, "projections", ws.ID, "manifest.json")
}

func (a *app) loadProjectionManifest(ws Workspace) projectionManifest {
	manifest := projectionManifest{Files: map[string]projectionFile{}}
	body, err := os.ReadFile(a.projectionManifestPath(ws))
	if err == nil {
		_ = json.Unmarshal(body, &manifest)
	}
	if manifest.Files == nil {
		manifest.Files = map[string]projectionFile{}
	}
	return manifest
}

func (a *app) saveProjectionManifest(ws Workspace, manifest projectionManifest) {
	path := a.projectionManifestPath(ws)
	_ = os.MkdirAll(filepath.Dir(path), 0o700)
	body, _ := json.MarshalIndent(manifest, "", "  ")
	_ = os.WriteFile(path, body, 0o600)
}

func (a *app) recordProjectionFile(ws Workspace, remote, local string, dirty bool, hydrated bool) projectionFile {
	manifest := a.loadProjectionManifest(ws)
	now := time.Now().UTC().Format(time.RFC3339Nano)
	file := manifest.Files[remote]
	file.RemotePath = remote
	file.LocalPath = local
	file.Dirty = dirty
	if hydrated {
		file.LastHydrated = now
	} else if !dirty {
		file.LastPushed = now
	}
	if info, err := os.Stat(local); err == nil {
		file.Size = info.Size()
		file.MTime = info.ModTime().UTC().Format(time.RFC3339Nano)
	}
	manifest.Files[remote] = file
	a.saveProjectionManifest(ws, manifest)
	return file
}

func (a *app) rollbackDirtyProjection(ctx context.Context, ws Workspace) {
	if ws.Target != "ssh" || ws.SSH == nil {
		return
	}
	manifest := a.loadProjectionManifest(ws)
	dirty := []projectionFile{}
	for _, file := range manifest.Files {
		if file.Dirty {
			dirty = append(dirty, file)
		}
	}
	if len(dirty) == 0 {
		return
	}
	proxy, _, err := a.ssh.proxyFor(ctx, ws)
	if err != nil {
		return
	}
	for _, file := range dirty {
		var out map[string]any
		if err := proxy.call(ctx, "read", map[string]any{"path": file.RemotePath}, &out); err != nil {
			_ = os.Remove(file.LocalPath)
			file.Dirty = false
			manifest.Files[file.RemotePath] = file
			continue
		}
		_ = os.MkdirAll(filepath.Dir(file.LocalPath), 0o700)
		_ = os.WriteFile(file.LocalPath, []byte(stringValue(out["content"])), 0o600)
		file.Dirty = false
		file.LastHydrated = time.Now().UTC().Format(time.RFC3339Nano)
		if info, err := os.Stat(file.LocalPath); err == nil {
			file.Size = info.Size()
			file.MTime = info.ModTime().UTC().Format(time.RFC3339Nano)
		}
		manifest.Files[file.RemotePath] = file
	}
	a.saveProjectionManifest(ws, manifest)
}
