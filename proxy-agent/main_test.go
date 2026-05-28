package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"
	"time"
)

func TestProxyAgentProtocolSmokeE2EHelper(t *testing.T) {
	if os.Getenv("ASTRALOPS_PROXY_AGENT_TEST_HELPER") != "1" {
		return
	}
	rootCWD = os.Getenv("ASTRALOPS_PROXY_AGENT_TEST_CWD")
	encoder = json.NewEncoder(os.Stdout)
	scanner := bufio.NewScanner(os.Stdin)
	scanner.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)
	for scanner.Scan() {
		var req request
		if err := json.Unmarshal(scanner.Bytes(), &req); err != nil {
			writeLine(response{Error: err.Error()})
			continue
		}
		result, err := dispatch(req)
		res := response{ID: req.ID, Result: result}
		if err != nil {
			res.Error = err.Error()
			res.Result = nil
		}
		writeLine(res)
	}
	os.Exit(0)
}

func TestProxyAgentProtocolSmokeE2E(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("proxy protocol smoke uses POSIX shell and PTY")
	}
	dir := t.TempDir()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=TestProxyAgentProtocolSmokeE2EHelper", "--")
	cmd.Env = append(os.Environ(),
		"ASTRALOPS_PROXY_AGENT_TEST_HELPER=1",
		"ASTRALOPS_PROXY_AGENT_TEST_CWD="+dir,
	)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = stdin.Close()
		_ = cmd.Wait()
	}()
	decoder := json.NewDecoder(stdout)
	send := func(id, method string, params any) response {
		t.Helper()
		body, _ := json.Marshal(requestEnvelope(id, method, params))
		if _, err := stdin.Write(append(body, '\n')); err != nil {
			t.Fatal(err)
		}
		var res response
		if err := decoder.Decode(&res); err != nil {
			t.Fatal(err)
		}
		if res.ID != id || res.Error != "" {
			t.Fatalf("%s response = %#v", method, res)
		}
		return res
	}

	hello := send("hello", "hello", map[string]any{})
	methods := methodSet(mapValueForTest(hello.Result)["capabilities"])
	for _, method := range []string{"read", "write", "remove", "move", "exec_start", "exec_kill", "pty_start", "pty_kill"} {
		if !methods[method] {
			t.Fatalf("hello missing method %s: %#v", method, hello.Result)
		}
	}

	body := []byte{0, 1, 2, 0xff, '\n'}
	send("write", "write", map[string]any{"path": filepath.Join(dir, "blob.bin"), "dataBase64": base64.StdEncoding.EncodeToString(body)})
	read := send("read", "read", map[string]any{"path": filepath.Join(dir, "blob.bin")})
	if got := stringValue(mapValueForTest(read.Result)["dataBase64"]); got != base64.StdEncoding.EncodeToString(body) {
		t.Fatalf("read dataBase64 = %q", got)
	}

	send("exec-start", "exec_start", map[string]any{"id": "exec-smoke", "cwd": dir, "argv": []string{"/bin/sh", "-c", "printf smoke"}})
	execExit := readEvent(t, decoder, "exec-smoke", "exit")
	if got := stringValue(mapValueForTest(execExit.Result)["stdout"]); got != "smoke" {
		t.Fatalf("exec stdout = %q", got)
	}

	send("pty-start", "pty_start", map[string]any{"id": "pty-smoke", "cwd": dir, "argv": []string{"/bin/sh", "-c", "exit 7"}, "rows": 24, "cols": 80})
	ptyExit := readEvent(t, decoder, "pty-smoke", "exit")
	if got := int(numberValue(mapValueForTest(ptyExit.Result)["exit_code"])); got != 7 {
		t.Fatalf("pty exit_code = %d, want 7", got)
	}
}

func requestEnvelope(id, method string, params any) map[string]any {
	return map[string]any{"id": id, "method": method, "params": params}
}

func readEvent(t *testing.T, decoder *json.Decoder, id, event string) response {
	t.Helper()
	deadline := time.After(3 * time.Second)
	ch := make(chan response, 1)
	errCh := make(chan error, 1)
	go func() {
		for {
			var res response
			if err := decoder.Decode(&res); err != nil {
				errCh <- err
				return
			}
			if res.ID == id && res.Event == event {
				ch <- res
				return
			}
		}
	}()
	select {
	case res := <-ch:
		return res
	case err := <-errCh:
		t.Fatal(err)
	case <-deadline:
		t.Fatalf("timed out waiting for %s/%s event", id, event)
	}
	return response{}
}

func mapValueForTest(value any) map[string]any {
	body, _ := json.Marshal(value)
	var out map[string]any
	_ = json.Unmarshal(body, &out)
	return out
}

func methodSet(value any) map[string]bool {
	caps := mapValueForTest(value)
	out := map[string]bool{}
	for _, item := range caps["methods"].([]any) {
		if method := stringValue(item); method != "" {
			out[method] = true
		}
	}
	return out
}

func TestDispatchReadWriteGrepAndExec(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "notes", "todo.txt")

	writeRaw, _ := json.Marshal(writeParams{Path: target, Content: "alpha\nneedle\n"})
	if _, err := dispatch(request{ID: "1", Method: "write", Params: writeRaw}); err != nil {
		t.Fatal(err)
	}

	readRaw, _ := json.Marshal(pathParams{Path: target})
	readResult, err := dispatch(request{ID: "2", Method: "read", Params: readRaw})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(readResult.(map[string]any)["content"].(string), "needle") {
		t.Fatalf("read did not include written content: %#v", readResult)
	}

	grepRaw, _ := json.Marshal(grepParams{CWD: dir, Pattern: "needle", Limit: 10})
	grepResult, err := dispatch(request{ID: "3", Method: "grep", Params: grepRaw})
	if err != nil {
		t.Fatal(err)
	}
	matches := grepResult.(map[string]any)["matches"].([]map[string]any)
	if len(matches) != 1 {
		t.Fatalf("matches = %d, want 1", len(matches))
	}

	execRaw, _ := json.Marshal(execParams{CWD: dir, Command: "pwd"})
	execResult, err := dispatch(request{ID: "4", Method: "exec", Params: execRaw})
	if err != nil {
		t.Fatal(err)
	}
	wantDir, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatal(err)
	}
	gotDir, err := filepath.EvalSymlinks(strings.TrimSpace(execResult.(map[string]any)["output"].(string)))
	if err != nil {
		t.Fatal(err)
	}
	if gotDir != wantDir {
		t.Fatalf("pwd output = %#v", execResult)
	}
}

func TestGlobGoExpandsSimpleBracePattern(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"a.txt", "b.js", "c.go"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(name), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	matches, err := globGo(dir, "*.{txt,js}")
	if err != nil {
		t.Fatal(err)
	}
	got := []string{}
	for _, match := range matches {
		got = append(got, filepath.Base(match))
	}
	want := []string{"a.txt", "b.js"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("matches = %v, want %v", got, want)
	}
}

func TestGrepGoExpandsSimpleBraceGlob(t *testing.T) {
	dir := t.TempDir()
	files := map[string]string{
		"a.txt": "needle in txt\n",
		"b.js":  "needle in js\n",
		"c.go":  "needle in go\n",
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	result, err := grepGo(dir, grepParams{CWD: dir, Pattern: "needle", Glob: "*.{txt,js}", Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	matches := result.(map[string]any)["matches"].([]map[string]any)
	got := []string{}
	for _, match := range matches {
		got = append(got, filepath.Base(stringValue(match["path"])))
	}
	sort.Strings(got)
	want := []string{"a.txt", "b.js"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("matches = %v, want %v", got, want)
	}
}

func TestHelloAdvertisesCoreExecutionMethods(t *testing.T) {
	result, err := dispatch(request{ID: "hello", Method: "hello"})
	if err != nil {
		t.Fatal(err)
	}
	caps := result.(map[string]any)["capabilities"].(map[string]any)
	methods := map[string]bool{}
	for _, method := range caps["methods"].([]string) {
		methods[method] = true
	}
	for _, method := range []string{"read", "write", "remove", "move", "exec_start", "exec_kill", "pty_start", "pty_kill"} {
		if !methods[method] {
			t.Fatalf("hello did not advertise core method %s: %#v", method, caps["methods"])
		}
	}
}

func TestDirsListsDirectoriesWithoutFiles(t *testing.T) {
	dir := t.TempDir()
	oldRoot := rootCWD
	rootCWD = dir
	defer func() { rootCWD = oldRoot }()
	if err := os.MkdirAll(filepath.Join(dir, "src", "nested"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "src", "file.txt"), []byte("content"), 0o644); err != nil {
		t.Fatal(err)
	}

	raw, _ := json.Marshal(dirsParams{Path: dir, Limit: 10})
	result, err := dispatch(request{ID: "dirs", Method: "dirs", Params: raw})
	if err != nil {
		t.Fatal(err)
	}
	out := result.(map[string]any)
	dirs := out["dirs"].([]string)
	if !containsStringForTest(dirs, filepath.Join(dir, "src")) || !containsStringForTest(dirs, filepath.Join(dir, "src", "nested")) {
		t.Fatalf("dirs = %#v", dirs)
	}
	if containsStringForTest(dirs, filepath.Join(dir, "src", "file.txt")) {
		t.Fatalf("dirs included file path: %#v", dirs)
	}
}

func TestTerminalEnvDefaultsToUTF8Locale(t *testing.T) {
	env := terminalEnv([]string{"PATH=/bin", "LANG=", "LC_CTYPE=C"})
	if got := envValue(env, "TERM"); got != "xterm-256color" {
		t.Fatalf("TERM = %q", got)
	}
	if got := envValue(env, "COLORTERM"); got != "truecolor" {
		t.Fatalf("COLORTERM = %q", got)
	}
	if got := envValue(env, "LANG"); got != defaultTerminalLocale {
		t.Fatalf("LANG = %q", got)
	}
	if got := envValue(env, "LC_CTYPE"); got != defaultTerminalLocale {
		t.Fatalf("LC_CTYPE = %q", got)
	}
}

func TestTerminalEnvPreservesExistingUTF8Locale(t *testing.T) {
	env := terminalEnv([]string{"LANG=zh_CN.UTF-8", "LC_CTYPE=zh_CN.UTF-8", "LC_ALL="})
	if got := envValue(env, "LANG"); got != "zh_CN.UTF-8" {
		t.Fatalf("LANG = %q", got)
	}
	if got := envValue(env, "LC_CTYPE"); got != "zh_CN.UTF-8" {
		t.Fatalf("LC_CTYPE = %q", got)
	}
}

func TestRunExecPreservesArgvWithoutShellExpansion(t *testing.T) {
	dir := t.TempDir()
	result, err := runExec(execParams{
		CWD:  dir,
		Argv: []string{"/usr/bin/printf", "%s", "$HOME; echo bad"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := result.(map[string]any)["stdout"].(string); got != "$HOME; echo bad" {
		t.Fatalf("stdout = %q, want literal argv without shell expansion", got)
	}
}

func TestDispatchReadWritePreservesBinaryWithBase64(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "blob.bin")
	body := []byte{0, 1, 2, 0xff, '\n'}

	writeRaw, _ := json.Marshal(writeParams{Path: target, DataBase64: base64.StdEncoding.EncodeToString(body)})
	if _, err := dispatch(request{ID: "1", Method: "write", Params: writeRaw}); err != nil {
		t.Fatal(err)
	}

	readRaw, _ := json.Marshal(pathParams{Path: target})
	readResult, err := dispatch(request{ID: "2", Method: "read", Params: readRaw})
	if err != nil {
		t.Fatal(err)
	}
	encoded := readResult.(map[string]any)["dataBase64"].(string)
	decoded, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		t.Fatal(err)
	}
	if string(decoded) != string(body) {
		t.Fatalf("decoded body = %#v, want %#v", decoded, body)
	}
}

func TestDispatchList(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("a"), 0o644); err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(pathParams{Path: dir})
	result, err := dispatch(request{ID: "1", Method: "list", Params: raw})
	if err != nil {
		t.Fatal(err)
	}
	items := result.([]any)
	if len(items) != 1 {
		t.Fatalf("items = %d, want 1", len(items))
	}
}

func TestDispatchFileSystemOptionsMatchExecServerSemantics(t *testing.T) {
	root := t.TempDir()
	previous := rootCWD
	rootCWD = root
	defer func() { rootCWD = previous }()

	recursiveFalse := false
	mkdirRaw, _ := json.Marshal(mkdirParams{Path: filepath.Join(root, "missing", "child"), Recursive: &recursiveFalse})
	if _, err := dispatch(request{ID: "mkdir", Method: "mkdir", Params: mkdirRaw}); err == nil {
		t.Fatal("mkdir recursive=false created missing parents")
	}
	recursiveTrue := true
	mkdirRaw, _ = json.Marshal(mkdirParams{Path: filepath.Join(root, "missing", "child"), Recursive: &recursiveTrue})
	if _, err := dispatch(request{ID: "mkdir", Method: "mkdir", Params: mkdirRaw}); err != nil {
		t.Fatal(err)
	}

	dir := filepath.Join(root, "tree")
	if err := os.MkdirAll(filepath.Join(dir, "nested"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "nested", "file.txt"), []byte("ok"), 0o644); err != nil {
		t.Fatal(err)
	}
	copyRaw, _ := json.Marshal(copyParams{Source: dir, Destination: filepath.Join(root, "copy"), Recursive: false})
	if _, err := dispatch(request{ID: "copy", Method: "copy", Params: copyRaw}); err == nil {
		t.Fatal("copy recursive=false copied a directory")
	}
	copyRaw, _ = json.Marshal(copyParams{Source: dir, Destination: filepath.Join(root, "copy"), Recursive: true})
	if _, err := dispatch(request{ID: "copy", Method: "copy", Params: copyRaw}); err != nil {
		t.Fatal(err)
	}
	if body, err := os.ReadFile(filepath.Join(root, "copy", "nested", "file.txt")); err != nil || string(body) != "ok" {
		t.Fatalf("copied file = %q, %v", body, err)
	}

	moveRaw, _ := json.Marshal(moveParams{
		Source:        filepath.Join(root, "copy", "nested", "file.txt"),
		Destination:   filepath.Join(root, "moved", "file.txt"),
		CreateParents: true,
	})
	if _, err := dispatch(request{ID: "move", Method: "move", Params: moveRaw}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "copy", "nested", "file.txt")); !os.IsNotExist(err) {
		t.Fatalf("source after move stat err = %v, want not exist", err)
	}
	if body, err := os.ReadFile(filepath.Join(root, "moved", "file.txt")); err != nil || string(body) != "ok" {
		t.Fatalf("moved file = %q, %v", body, err)
	}

	removeRaw, _ := json.Marshal(removeParams{Path: filepath.Join(root, "does-not-exist"), Force: &recursiveFalse})
	if _, err := dispatch(request{ID: "remove", Method: "remove", Params: removeRaw}); err == nil {
		t.Fatal("remove force=false ignored missing path")
	}
	removeRaw, _ = json.Marshal(removeParams{Path: filepath.Join(root, "copy"), Recursive: &recursiveFalse})
	if _, err := dispatch(request{ID: "remove", Method: "remove", Params: removeRaw}); err == nil {
		t.Fatal("remove recursive=false removed non-empty directory")
	}
	removeRaw, _ = json.Marshal(removeParams{Path: filepath.Join(root, "copy"), Recursive: &recursiveTrue})
	if _, err := dispatch(request{ID: "remove", Method: "remove", Params: removeRaw}); err != nil {
		t.Fatal(err)
	}
}

func TestPTYExitEventIncludesExitCode(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("PTY test is POSIX-only")
	}
	dir := t.TempDir()
	previousRoot := rootCWD
	rootCWD = dir
	defer func() { rootCWD = previousRoot }()

	var buf bytes.Buffer
	previousEncoder := encoder
	encoder = json.NewEncoder(&buf)
	defer func() { encoder = previousEncoder }()

	if _, err := startPTY("pty-exit", ptyStartParams{
		ID:   "pty-exit",
		CWD:  dir,
		Argv: []string{"/bin/sh", "-c", "exit 7"},
		Rows: 24,
		Cols: 80,
	}); err != nil {
		t.Fatal(err)
	}

	deadline := time.Now().Add(2 * time.Second)
	var exit response
	for time.Now().Before(deadline) {
		dec := json.NewDecoder(strings.NewReader(buf.String()))
		for {
			var event response
			if err := dec.Decode(&event); err != nil {
				break
			}
			if event.Event == "exit" {
				exit = event
				break
			}
		}
		if exit.Event == "exit" {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if exit.Event != "exit" {
		t.Fatalf("exit event not found in %q", buf.String())
	}
	result := exit.Result.(map[string]any)
	if int(result["exit_code"].(float64)) != 7 {
		t.Fatalf("exit_code = %#v, want 7", result["exit_code"])
	}
}

func TestPTYKillTerminatesProcessGroup(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("PTY process group test is POSIX-only")
	}
	dir := t.TempDir()
	previousRoot := rootCWD
	rootCWD = dir
	defer func() { rootCWD = previousRoot }()

	var buf bytes.Buffer
	previousEncoder := encoder
	encoder = json.NewEncoder(&buf)
	defer func() { encoder = previousEncoder }()

	ready := filepath.Join(dir, "pty-child.ready")
	marker := filepath.Join(dir, "pty-child.survived")
	if _, err := startPTY("pty-kill-group", ptyStartParams{
		ID:  "pty-kill-group",
		CWD: dir,
		Argv: []string{
			"/bin/sh",
			"-c",
			`(trap '' HUP; printf ready > "$READY"; sleep 2; printf survived > "$MARKER") & wait`,
		},
		Env: map[string]string{
			"READY":  ready,
			"MARKER": marker,
		},
		Rows: 24,
		Cols: 80,
	}); err != nil {
		t.Fatal(err)
	}

	deadline := time.Now().Add(2 * time.Second)
	for {
		if _, err := os.Stat(ready); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("pty child did not signal ready")
		}
		time.Sleep(10 * time.Millisecond)
	}

	if err := killPTY("pty-kill-group"); err != nil {
		t.Fatal(err)
	}
	time.Sleep(2500 * time.Millisecond)
	if body, err := os.ReadFile(marker); err == nil {
		t.Fatalf("pty background child survived kill and wrote marker: %q", body)
	} else if !os.IsNotExist(err) {
		t.Fatalf("checking marker failed: %v", err)
	}
}

func TestCleanupManagedProcessesTerminatesPTYAndExecProcessGroups(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("process group test is POSIX-only")
	}
	dir := t.TempDir()
	previousRoot := rootCWD
	rootCWD = dir
	defer func() { rootCWD = previousRoot }()

	var buf bytes.Buffer
	previousEncoder := encoder
	encoder = json.NewEncoder(&buf)
	defer func() { encoder = previousEncoder }()

	ptyReady := filepath.Join(dir, "pty-child.ready")
	ptyMarker := filepath.Join(dir, "pty-child.survived")
	if _, err := startPTY("cleanup-pty", ptyStartParams{
		ID:  "cleanup-pty",
		CWD: dir,
		Argv: []string{
			"/bin/sh",
			"-c",
			`(trap '' HUP; printf ready > "$READY"; sleep 2; printf survived > "$MARKER") & wait`,
		},
		Env: map[string]string{
			"READY":  ptyReady,
			"MARKER": ptyMarker,
		},
		Rows: 24,
		Cols: 80,
	}); err != nil {
		t.Fatal(err)
	}

	execReady := filepath.Join(dir, "exec-child.ready")
	execMarker := filepath.Join(dir, "exec-child.survived")
	if _, err := startExec(execParams{
		ID:  "cleanup-exec",
		CWD: dir,
		Argv: []string{
			"/bin/sh",
			"-c",
			`(trap '' HUP; printf ready > "$READY"; sleep 2; printf survived > "$MARKER") & wait`,
		},
		Env: map[string]string{
			"READY":  execReady,
			"MARKER": execMarker,
		},
	}); err != nil {
		t.Fatal(err)
	}

	deadline := time.Now().Add(2 * time.Second)
	for {
		_, ptyErr := os.Stat(ptyReady)
		_, execErr := os.Stat(execReady)
		if ptyErr == nil && execErr == nil && lookupExec("cleanup-exec") != nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("managed children did not signal ready; events=%q", buf.String())
		}
		time.Sleep(10 * time.Millisecond)
	}

	cleanupManagedProcesses()
	time.Sleep(2500 * time.Millisecond)
	for _, marker := range []string{ptyMarker, execMarker} {
		if body, err := os.ReadFile(marker); err == nil {
			t.Fatalf("managed child survived cleanup and wrote %s: %q", marker, body)
		} else if !os.IsNotExist(err) {
			t.Fatalf("checking marker failed: %v", err)
		}
	}
}

func TestRemoveSelfExecutableDeletesConfiguredPath(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "astral-proxy-agent")
	if err := os.WriteFile(target, []byte("helper"), 0o700); err != nil {
		t.Fatal(err)
	}
	previousExecutable := proxyAgentExecutable
	previousRemove := removeProxyAgentFile
	proxyAgentExecutable = func() (string, error) { return target, nil }
	removeProxyAgentFile = os.Remove
	defer func() {
		proxyAgentExecutable = previousExecutable
		removeProxyAgentFile = previousRemove
	}()

	removeSelfExecutable()
	if _, err := os.Stat(target); !os.IsNotExist(err) {
		t.Fatalf("self executable still exists or stat failed: %v", err)
	}
}

func TestStartExecCanBeKilled(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("exec kill test is POSIX-only")
	}
	dir := t.TempDir()
	previousRoot := rootCWD
	rootCWD = dir
	defer func() { rootCWD = previousRoot }()

	var buf bytes.Buffer
	previousEncoder := encoder
	encoder = json.NewEncoder(&buf)
	defer func() { encoder = previousEncoder }()

	if _, err := startExec(execParams{
		ID:   "exec-kill",
		CWD:  dir,
		Argv: []string{"/bin/sh", "-c", "sleep 30"},
	}); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for lookupExec("exec-kill") == nil && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if result := killExec("exec-kill"); !result["running"].(bool) {
		t.Fatalf("killExec result = %#v, want running true", result)
	}

	var exit response
	for time.Now().Before(deadline) {
		dec := json.NewDecoder(strings.NewReader(buf.String()))
		for {
			var event response
			if err := dec.Decode(&event); err != nil {
				break
			}
			if event.ID == "exec-kill" && event.Event == "exit" {
				exit = event
				break
			}
		}
		if exit.Event == "exit" {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if exit.Event != "exit" {
		t.Fatalf("exit event not found in %q", buf.String())
	}
	result := exit.Result.(map[string]any)
	if int(result["exit_code"].(float64)) == 0 {
		t.Fatalf("exit_code = %#v, want killed process to be non-zero", result["exit_code"])
	}
}

func TestStartExecKillTerminatesProcessGroup(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("process group test is POSIX-only")
	}
	dir := t.TempDir()
	previousRoot := rootCWD
	rootCWD = dir
	defer func() { rootCWD = previousRoot }()

	var buf bytes.Buffer
	previousEncoder := encoder
	encoder = json.NewEncoder(&buf)
	defer func() { encoder = previousEncoder }()

	ready := filepath.Join(dir, "child.ready")
	marker := filepath.Join(dir, "child.survived")
	if _, err := startExec(execParams{
		ID:  "exec-kill-group",
		CWD: dir,
		Argv: []string{
			"/bin/sh",
			"-c",
			`(printf ready > "$READY"; sleep 2; printf survived > "$MARKER") & wait`,
		},
		Env: map[string]string{
			"READY":  ready,
			"MARKER": marker,
		},
	}); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for lookupExec("exec-kill-group") == nil && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	for {
		if _, err := os.Stat(ready); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("child process did not signal ready; events=%q", buf.String())
		}
		time.Sleep(10 * time.Millisecond)
	}

	if result := killExec("exec-kill-group"); !result["running"].(bool) {
		t.Fatalf("killExec result = %#v, want running true", result)
	}

	var exit response
	for time.Now().Before(deadline) {
		dec := json.NewDecoder(strings.NewReader(buf.String()))
		for {
			var event response
			if err := dec.Decode(&event); err != nil {
				break
			}
			if event.ID == "exec-kill-group" && event.Event == "exit" {
				exit = event
				break
			}
		}
		if exit.Event == "exit" {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if exit.Event != "exit" {
		t.Fatalf("exit event not found in %q", buf.String())
	}

	time.Sleep(2500 * time.Millisecond)
	if body, err := os.ReadFile(marker); err == nil {
		t.Fatalf("background child survived kill and wrote marker: %q", body)
	} else if !os.IsNotExist(err) {
		t.Fatalf("checking marker failed: %v", err)
	}
}

func TestResolveRemotePathUsesRootCWDOnlyForRelativePaths(t *testing.T) {
	previous := rootCWD
	root := t.TempDir()
	rootCWD = root
	defer func() { rootCWD = previous }()

	relative, err := resolveRemotePath("ok.txt")
	if err != nil {
		t.Fatalf("relative path was rejected: %v", err)
	}
	if relative != filepath.Join(root, "ok.txt") {
		t.Fatalf("relative path = %q, want %q", relative, filepath.Join(root, "ok.txt"))
	}

	outside := filepath.Join(root, "..", "outside.txt")
	absolute, err := resolveRemotePath(outside)
	if err != nil {
		t.Fatalf("absolute path outside root was rejected: %v", err)
	}
	if absolute != filepath.Clean(outside) {
		t.Fatalf("absolute path = %q, want %q", absolute, filepath.Clean(outside))
	}
}

func TestGlobAndGrepPreferRGWhenAvailable(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell script fake rg is POSIX-only")
	}
	dir := t.TempDir()
	bin := filepath.Join(dir, "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	rgPath := filepath.Join(bin, "rg")
	script := `#!/bin/sh
if [ "$1" = "--files" ]; then
  printf 'notes/todo.txt\n'
  exit 0
fi
printf '%s\n' '{"type":"match","data":{"path":{"text":"notes/todo.txt"},"line_number":2,"lines":{"text":"needle here\\n"}}}'
`
	if err := os.WriteFile(rgPath, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin)
	previous := rootCWD
	rootCWD = dir
	defer func() { rootCWD = previous }()

	globResult, err := glob(globParams{CWD: dir, Pattern: "*.txt"})
	if err != nil {
		t.Fatal(err)
	}
	globMap := globResult.(map[string]any)
	if globMap["backend"] != "rg" {
		t.Fatalf("glob backend = %#v, want rg", globMap["backend"])
	}
	if got := globMap["matches"].([]string)[0]; got != filepath.Join(dir, "notes", "todo.txt") {
		t.Fatalf("glob match = %s", got)
	}

	grepResult, err := grep(grepParams{CWD: dir, Pattern: "needle", Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	grepMap := grepResult.(map[string]any)
	if grepMap["backend"] != "rg" {
		t.Fatalf("grep backend = %#v, want rg", grepMap["backend"])
	}
	matches := grepMap["matches"].([]map[string]any)
	if len(matches) != 1 || matches[0]["path"] != filepath.Join(dir, "notes", "todo.txt") {
		t.Fatalf("grep matches = %#v", matches)
	}
}

func TestGrepFallsBackWithoutRG(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("needle\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", t.TempDir())
	result, err := grep(grepParams{CWD: dir, Pattern: "needle", Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	value := result.(map[string]any)
	if value["backend"] != "go" {
		t.Fatalf("backend = %#v, want go", value["backend"])
	}
}

func containsStringForTest(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}
