package main

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const codexBundledSkillsMarker = ".astralops-codex-bundled-skills.json"

type codexBundledSkillFile struct {
	Rel  string
	Body []byte
}

type codexBundledSkillBundle struct {
	Source  string
	Version string
	Files   []codexBundledSkillFile
}

func (a *app) prepareCodexRemoteBundledSkills(ctx context.Context, ws Workspace, codexPath string) (string, error) {
	if ws.Target != "ssh" || ws.SSH == nil {
		return "", nil
	}
	bundle, err := loadCodexBundledSkillBundle(codexPath, a.store.dataDir)
	if err != nil {
		return "", err
	}
	if len(bundle.Files) == 0 {
		return "", errors.New("no Codex bundled skills found")
	}

	runtimeDir := ""
	if ssh := a.sshService(); ssh != nil {
		runtimeDir = ssh.RemoteWorkspaceRuntimeDir(ws)
	}
	if runtimeDir == "" {
		runtimeDir = remotePathJoin("/tmp/.astralops", ws.ID)
	}
	root := remotePathClean(remotePathJoin(runtimeDir, "codex-skills", bundle.Version))
	remoteCodexHome := remotePathJoin(root, "codex-home")
	remoteSystemRoot := remotePathJoin(remoteCodexHome, "skills/.system")
	remoteAgentsRoot := remotePathJoin(root, "agents-skills/_astralops_codex_bundled")
	workspaceAgentsRoot := remotePathJoin(ws.SSH.RemoteCWD, ".agents/skills/_astralops_codex_bundled")
	marker := codexBundledSkillsMarkerPayload(bundle.Version)

	if a.remoteCodexBundledSkillsReady(ctx, ws, remotePathJoin(root, codexBundledSkillsMarker), marker) &&
		a.remoteCodexBundledSkillsReady(ctx, ws, remotePathJoin(workspaceAgentsRoot, codexBundledSkillsMarker), marker) {
		return remoteCodexHome, nil
	}

	_ = a.sshService().Call(ctx, ws, "remove", map[string]any{"path": root, "recursive": true, "force": true}, nil)
	_ = a.sshService().Call(ctx, ws, "remove", map[string]any{"path": workspaceAgentsRoot, "recursive": true, "force": true}, nil)
	for _, dir := range []string{remoteSystemRoot, remoteAgentsRoot, workspaceAgentsRoot} {
		if err := a.sshService().Call(ctx, ws, "mkdir", map[string]any{"path": dir, "recursive": true}, nil); err != nil {
			return "", err
		}
	}

	for _, file := range bundle.Files {
		rel := filepath.ToSlash(file.Rel)
		for _, destRoot := range []string{remoteSystemRoot, remoteAgentsRoot, workspaceAgentsRoot} {
			if err := a.sshService().Call(ctx, ws, "write", map[string]any{
				"path":       remotePathJoin(destRoot, rel),
				"dataBase64": base64.StdEncoding.EncodeToString(file.Body),
			}, nil); err != nil {
				return "", err
			}
		}
	}
	for _, markerPath := range []string{
		remotePathJoin(root, codexBundledSkillsMarker),
		remotePathJoin(workspaceAgentsRoot, codexBundledSkillsMarker),
	} {
		if err := a.sshService().Call(ctx, ws, "write", map[string]any{
			"path":       markerPath,
			"dataBase64": base64.StdEncoding.EncodeToString(marker),
		}, nil); err != nil {
			return "", err
		}
	}
	return remoteCodexHome, nil
}

func (a *app) remoteCodexBundledSkillsReady(ctx context.Context, ws Workspace, markerPath string, expected []byte) bool {
	var out map[string]any
	if err := a.sshService().Call(ctx, ws, "read", map[string]any{"path": markerPath}, &out); err != nil {
		return false
	}
	body, err := remoteReadBytes(out)
	return err == nil && string(body) == string(expected)
}

func codexBundledSkillsMarkerPayload(bundleVersion string) []byte {
	body, _ := json.Marshal(map[string]string{
		"kind":             "astralops-codex-bundled-skills",
		"version":          bundleVersion,
		"astralopsVersion": version,
	})
	return append(body, '\n')
}

func loadCodexBundledSkillBundle(codexPath string, dataDir string) (codexBundledSkillBundle, error) {
	source, err := localCodexBundledSkillsSource(codexPath, dataDir)
	if err != nil {
		return codexBundledSkillBundle{}, err
	}
	files, err := readCodexBundledSkillFiles(source)
	if err != nil {
		return codexBundledSkillBundle{}, err
	}
	hash := sha256.New()
	hash.Write([]byte("astralops:" + version + "\n"))
	for _, file := range files {
		hash.Write([]byte(file.Rel))
		hash.Write([]byte{0})
		hash.Write(file.Body)
		hash.Write([]byte{0})
	}
	return codexBundledSkillBundle{
		Source:  source,
		Version: "v1-" + hex.EncodeToString(hash.Sum(nil))[:16],
		Files:   files,
	}, nil
}

func localCodexBundledSkillsSource(codexPath string, dataDir string) (string, error) {
	sourceHome := strings.TrimSpace(os.Getenv("CODEX_HOME"))
	if sourceHome == "" {
		if userHome, err := os.UserHomeDir(); err == nil {
			sourceHome = filepath.Join(userHome, ".codex")
		}
	}
	if sourceHome != "" {
		source := filepath.Join(sourceHome, "skills", ".system")
		if localCodexBundledSkillsUsable(source) {
			return source, nil
		}
	}

	cacheHome := filepath.Join(dataDir, "runtime", "codex-bundled-source", "home")
	source := filepath.Join(cacheHome, "skills", ".system")
	if localCodexBundledSkillsUsable(source) {
		return source, nil
	}
	if err := materializeCodexBundledSkills(codexPath, cacheHome); err != nil {
		return "", err
	}
	if !localCodexBundledSkillsUsable(source) {
		return "", fmt.Errorf("Codex bundled skills were not materialized under %s", source)
	}
	return source, nil
}

func localCodexBundledSkillsUsable(root string) bool {
	if strings.TrimSpace(root) == "" {
		return false
	}
	found := false
	_ = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil || found {
			return nil
		}
		if !d.IsDir() && d.Name() == "SKILL.md" {
			found = true
			return filepath.SkipAll
		}
		return nil
	})
	return found
}

func materializeCodexBundledSkills(codexPath string, cacheHome string) error {
	if strings.TrimSpace(codexPath) == "" {
		return errors.New("codex executable path is empty")
	}
	if err := os.MkdirAll(cacheHome, 0o700); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, codexPath, "app-server", "-c", "mcp_servers={}", "--listen", "stdio://")
	cmd.Env = withEnvValue(os.Environ(), "CODEX_HOME", cacheHome)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	time.Sleep(750 * time.Millisecond)
	_ = stdin.Close()
	if cmd.Process != nil {
		_ = cmd.Process.Kill()
	}
	_ = cmd.Wait()
	if ctx.Err() != nil && !localCodexBundledSkillsUsable(filepath.Join(cacheHome, "skills", ".system")) {
		return ctx.Err()
	}
	return nil
}

func readCodexBundledSkillFiles(root string) ([]codexBundledSkillFile, error) {
	var files []codexBundledSkillFile
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		if rel == "." || strings.HasPrefix(rel, "..") {
			return nil
		}
		body, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		files = append(files, codexBundledSkillFile{Rel: rel, Body: body})
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(files, func(i, j int) bool {
		return filepath.ToSlash(files[i].Rel) < filepath.ToSlash(files[j].Rel)
	})
	return files, nil
}
