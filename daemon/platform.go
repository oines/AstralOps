package main

import (
	"context"
	"os/exec"
	posixpath "path"
	"runtime"
	"strings"
)

const windowsTerminalDisabledReason = "windows_terminal_disabled"

type hostPlatformInfo struct {
	OS   string `json:"os"`
	Arch string `json:"arch"`
}

type hostFeatures struct {
	Terminal terminalFeature `json:"terminal"`
}

type terminalFeature struct {
	Available bool   `json:"available"`
	Reason    string `json:"reason,omitempty"`
}

func currentHostPlatform() hostPlatformInfo {
	return hostPlatformInfo{OS: runtime.GOOS, Arch: runtime.GOARCH}
}

func currentHostFeatures() hostFeatures {
	return hostFeaturesForOS(runtime.GOOS)
}

func hostFeaturesForOS(goos string) hostFeatures {
	feature := terminalFeature{Available: true}
	if goos == "windows" {
		feature.Available = false
		feature.Reason = windowsTerminalDisabledReason
	}
	return hostFeatures{Terminal: feature}
}

func terminalAvailableOnHost() bool {
	return currentHostFeatures().Terminal.Available
}

func localShellCommand(ctx context.Context, command string) *exec.Cmd {
	return localShellCommandForOS(ctx, runtime.GOOS, command)
}

func localShellCommandForOS(ctx context.Context, goos, command string) *exec.Cmd {
	if goos == "windows" {
		return exec.CommandContext(ctx, "cmd.exe", "/d", "/s", "/c", command)
	}
	return exec.CommandContext(ctx, "/bin/sh", "-lc", command)
}

func remotePathClean(value string) string {
	clean := posixpath.Clean(strings.TrimSpace(value))
	if clean == "." {
		return ""
	}
	return clean
}

func remotePathIsAbs(value string) bool {
	return posixpath.IsAbs(strings.TrimSpace(value))
}

func remotePathJoin(parts ...string) string {
	return posixpath.Join(parts...)
}

func remotePathRel(root, target string) (string, error) {
	root = remotePathClean(root)
	target = remotePathClean(target)
	if root == "" {
		root = "/"
	}
	if target == "" {
		target = "/"
	}
	if root == target {
		return ".", nil
	}
	rootParts := splitRemotePath(root)
	targetParts := splitRemotePath(target)
	i := 0
	for i < len(rootParts) && i < len(targetParts) && rootParts[i] == targetParts[i] {
		i++
	}
	rel := make([]string, 0, len(rootParts)-i+len(targetParts)-i)
	for j := i; j < len(rootParts); j++ {
		rel = append(rel, "..")
	}
	rel = append(rel, targetParts[i:]...)
	if len(rel) == 0 {
		return ".", nil
	}
	return strings.Join(rel, "/"), nil
}

func remotePathBase(value string) string {
	return posixpath.Base(value)
}

func splitRemotePath(value string) []string {
	value = strings.Trim(remotePathClean(value), "/")
	if value == "" {
		return nil
	}
	return strings.Split(value, "/")
}

func pathEscapesRoot(rel string) bool {
	return rel == ".." || strings.HasPrefix(rel, "../") || strings.HasPrefix(rel, `..\`)
}
