package main

import (
	"bufio"
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// LBF_PS_SPAWN=1 starts a PowerShell for every run, as a user does; otherwise runs share long-lived PowerShells that read lbf.ps1 once.
func psSpawnEach() bool { return os.Getenv("LBF_PS_SPAWN") == "1" }

const psReplyMark = "\x01lbf-reply "

// Runs each request's command with the request's environment and folder, and replies with its exit code, stdout and stderr.
const psWorkerScript = `$ProgressPreference = 'SilentlyContinue'
$utf8 = New-Object System.Text.UTF8Encoding $false
$requests = New-Object System.IO.StreamReader -ArgumentList ([Console]::OpenStandardInput()), $utf8
$replies = New-Object System.IO.StreamWriter -ArgumentList ([Console]::OpenStandardOutput()), $utf8
$replies.AutoFlush = $true
[Console]::SetIn([System.IO.TextReader]::Null)
$consoleOut = [Console]::Out
$consoleErr = [Console]::Error
while ($null -ne ($line = $requests.ReadLine())) {
    $req = $utf8.GetString([Convert]::FromBase64String($line)) | ConvertFrom-Json
    foreach ($k in @([Environment]::GetEnvironmentVariables().Keys)) {
        try { [Environment]::SetEnvironmentVariable([string]$k, $null) } catch { }
    }
    foreach ($kv in @($req.env)) {
        $i = $kv.IndexOf('=', 1)
        if ($i -gt 0) { try { [Environment]::SetEnvironmentVariable($kv.Substring(0, $i), $kv.Substring($i + 1)) } catch { } }
    }
    Set-Location -LiteralPath $req.dir
    [Environment]::CurrentDirectory = $req.dir
    $out = New-Object System.Text.StringBuilder
    $err = New-Object System.IO.StringWriter
    $direct = New-Object System.IO.StringWriter
    [Console]::SetOut($direct)
    [Console]::SetError($err)
    $global:LASTEXITCODE = 0
    $ErrorActionPreference = 'Continue'
    $code = 0
    try {
        foreach ($o in @(& ([ScriptBlock]::Create($req.command)) 2>&1)) {
            if ($null -eq $o) { continue }
            if ($o -is [System.Management.Automation.ErrorRecord]) { $err.Write(($o | Out-String)); continue }
            if ($o -is [string]) { [void]$out.Append($o).Append("` + "`n" + `") } else { [void]$out.Append(($o | Out-String)) }
        }
        $code = $global:LASTEXITCODE
    } catch {
        $err.Write(($_ | Out-String))
        $code = 1
    } finally {
        [Console]::SetOut($consoleOut)
        [Console]::SetError($consoleErr)
    }
    $stdout = $direct.ToString() + $out.ToString()
    $replies.WriteLine([string][char]1 + 'lbf-reply ' + [string]$code + ' ' + [Convert]::ToBase64String($utf8.GetBytes($stdout)) + ' ' + [Convert]::ToBase64String($utf8.GetBytes($err.ToString())))
}
`

type psWorker struct {
	cmd     *exec.Cmd
	in      io.WriteCloser
	out     *bufio.Reader
	stderr  *lockedBuffer
	waitErr chan error
}

type lockedBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (l *lockedBuffer) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}

func (l *lockedBuffer) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.String()
}

var psWorkers struct {
	mu   sync.Mutex
	idle []*psWorker
	all  []*psWorker
}

var psWorkerScriptPath = sync.OnceValues(func() (string, error) {
	dir, err := os.MkdirTemp("", "lbf-ps-worker-")
	if err != nil {
		return "", err
	}
	path := filepath.Join(dir, "worker.ps1")
	// A BOM, so Windows PowerShell reads the script as UTF-8.
	return path, os.WriteFile(path, []byte("\ufeff"+psWorkerScript), 0o644)
})

func startPSWorker(ps string) (*psWorker, error) {
	script, err := psWorkerScriptPath()
	if err != nil {
		return nil, err
	}
	cmd := exec.Command(ps, "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-File", script)
	cmd.Dir = os.TempDir()
	w := &psWorker{cmd: cmd, stderr: &lockedBuffer{}, waitErr: make(chan error, 1)}
	cmd.Stderr = w.stderr
	if w.in, err = cmd.StdinPipe(); err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	w.out = bufio.NewReaderSize(stdout, 1<<20)
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	go func() { w.waitErr <- cmd.Wait() }()
	psWorkers.mu.Lock()
	psWorkers.all = append(psWorkers.all, w)
	psWorkers.mu.Unlock()
	return w, nil
}

func takePSWorker(ps string) (*psWorker, error) {
	psWorkers.mu.Lock()
	if n := len(psWorkers.idle); n > 0 {
		w := psWorkers.idle[n-1]
		psWorkers.idle = psWorkers.idle[:n-1]
		psWorkers.mu.Unlock()
		return w, nil
	}
	psWorkers.mu.Unlock()
	return startPSWorker(ps)
}

func returnPSWorker(w *psWorker) {
	psWorkers.mu.Lock()
	psWorkers.idle = append(psWorkers.idle, w)
	psWorkers.mu.Unlock()
}

func (w *psWorker) kill() {
	_ = w.cmd.Process.Kill()
	psWorkers.mu.Lock()
	for i, x := range psWorkers.all {
		if x == w {
			psWorkers.all = append(psWorkers.all[:i], psWorkers.all[i+1:]...)
			break
		}
	}
	psWorkers.mu.Unlock()
}

func stopPSWorkers() {
	psWorkers.mu.Lock()
	all := psWorkers.all
	psWorkers.all, psWorkers.idle = nil, nil
	psWorkers.mu.Unlock()
	for _, w := range all {
		_ = w.in.Close()
		select {
		case <-w.waitErr:
		case <-time.After(10 * time.Second):
			_ = w.cmd.Process.Kill()
		}
	}
}

type psRequest struct {
	Dir     string   `json:"dir"`
	Env     []string `json:"env"`
	Command string   `json:"command"`
}

// Runs command in a long-lived PowerShell; a worker that times out, dies or writes outside a reply is discarded.
func runInPSWorker(t *testing.T, ps string, req psRequest, timeout time.Duration) lbfResult {
	t.Helper()
	w, err := takePSWorker(ps)
	if err != nil {
		t.Fatalf("starting %s: %v", ps, err)
	}
	body, err := json.Marshal(req)
	must(t, err)
	type reply struct {
		r   lbfResult
		err error
	}
	done := make(chan reply, 1)
	go func() {
		if _, err := io.WriteString(w.in, base64.StdEncoding.EncodeToString(body)+"\n"); err != nil {
			done <- reply{err: err}
			return
		}
		r, err := w.readReply()
		done <- reply{r, err}
	}()
	select {
	case got := <-done:
		if got.err != nil {
			w.kill()
			t.Fatalf("PowerShell worker: %v\nits stderr:\n%s", got.err, w.stderr.String())
		}
		returnPSWorker(w)
		return got.r
	case <-time.After(timeout):
		w.kill()
		t.Fatalf("lbf.ps1 did not finish within %s", timeout)
	}
	return lbfResult{}
}

func (w *psWorker) readReply() (lbfResult, error) {
	var stray strings.Builder
	for {
		line, err := w.out.ReadString('\n')
		if err != nil {
			return lbfResult{}, fmt.Errorf("reading its reply: %w (it wrote %q)", err, stray.String())
		}
		rest, ok := strings.CutPrefix(line, psReplyMark)
		if !ok {
			stray.WriteString(line)
			continue
		}
		if stray.Len() > 0 {
			return lbfResult{}, fmt.Errorf("it wrote outside a reply: %q", stray.String())
		}
		parts := strings.Split(strings.TrimRight(rest, "\r\n"), " ")
		if len(parts) != 3 {
			return lbfResult{}, errors.New("a reply without exit code, stdout and stderr")
		}
		code, err := strconv.Atoi(parts[0])
		if err != nil {
			return lbfResult{}, fmt.Errorf("exit code %q", parts[0])
		}
		stdout, err1 := base64.StdEncoding.DecodeString(parts[1])
		stderr, err2 := base64.StdEncoding.DecodeString(parts[2])
		if err := errors.Join(err1, err2); err != nil {
			return lbfResult{}, err
		}
		return lbfResult{string(stdout), string(stderr), code}, nil
	}
}
