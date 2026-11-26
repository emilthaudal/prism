package tracks

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"html"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
)

// serveLabel is the launchd job serve runs as. A TRACKS_HOME other than the default gets a job
// of its own, so a test home never starts or stops Emil's serve.
func serveLabel() string {
	const label = "dev.emilthaudal.tracks-serve"
	h, _ := os.UserHomeDir()
	if home() == filepath.Join(h, ".local", "state", "tracks") {
		return label
	}
	sum := sha256.Sum256([]byte(home()))
	return label + "." + hex.EncodeToString(sum[:4])
}

// launchctl runs launchctl; tests replace it.
var launchctl = func(args ...string) ([]byte, error) {
	return exec.Command("/bin/launchctl", args...).CombinedOutput()
}

// detachServe starts serve outside the caller's process tree and returns without waiting for it.
//
// On macOS that is a launchd job of its own. An app's launchd job takes every process it started
// down with it when it quits, setsid or not, so a serve that Prism spawned, and the claude daemon
// that serve's claude --bg started, died with Prism, and with them every desk session. The job
// has AbandonProcessGroup, so the claude daemon also outlives serve itself. A job that is loaded
// already is kickstarted, which leaves a running serve alone.
func detachServe() error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(home(), 0o700); err != nil {
		return err
	}
	if runtime.GOOS != "darwin" {
		return spawnServe(exe)
	}
	target := fmt.Sprintf("gui/%d/%s", os.Getuid(), serveLabel())
	if _, err := launchctl("kickstart", target); err == nil {
		return nil
	}
	plist := filepath.Join(home(), "serve.plist")
	if err := writeAtomic(plist, servePlist(exe)); err != nil {
		return err
	}
	out, err := launchctl("bootstrap", fmt.Sprintf("gui/%d", os.Getuid()), plist)
	if err == nil {
		return nil
	}
	// Another client loaded it in between.
	if _, kerr := launchctl("kickstart", target); kerr == nil {
		return nil
	}
	return fmt.Errorf("launchctl bootstrap %s: %s", plist, strings.TrimSpace(string(out)+" "+err.Error()))
}

// servePlist is the launchd job for serve: started once on load, never kept alive, its output
// appended to serve.log, and its children left running when it exits.
func servePlist(exe string) []byte {
	logPath := filepath.Join(home(), "serve.log")
	env := ""
	if h := os.Getenv("TRACKS_HOME"); h != "" {
		env = "\t<key>EnvironmentVariables</key>\n\t<dict><key>TRACKS_HOME</key><string>" + html.EscapeString(h) + "</string></dict>\n"
	}
	var b bytes.Buffer
	fmt.Fprintf(&b, `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>Label</key><string>%s</string>
	<key>ProgramArguments</key>
	<array><string>%s</string><string>serve</string></array>
	<key>RunAtLoad</key><true/>
	<key>AbandonProcessGroup</key><true/>
	<key>ProcessType</key><string>Interactive</string>
	<key>StandardOutPath</key><string>%s</string>
	<key>StandardErrorPath</key><string>%s</string>
%s</dict>
</plist>
`, html.EscapeString(serveLabel()), html.EscapeString(exe), html.EscapeString(logPath), html.EscapeString(logPath), env)
	return b.Bytes()
}

// spawnServe starts serve in a session of its own, where there is no launchd.
func spawnServe(exe string) error {
	logf, err := os.OpenFile(filepath.Join(home(), "serve.log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	defer logf.Close()
	cmd := exec.Command(exe, "serve")
	cmd.Stdout, cmd.Stderr = logf, logf
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		return err
	}
	return cmd.Process.Release()
}
