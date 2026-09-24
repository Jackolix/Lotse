package hub

import (
	"compress/gzip"
	"embed"
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/Jackolix/Lotse/internal/version"
)

//go:embed scripts/install.sh scripts/install.ps1
var scripts embed.FS

// getInstallScript serves the generic installer. It contains no secrets: the hub
// URL, key and token are passed as arguments by the command shown in the UI.
func (h *Hub) getInstallScript(w http.ResponseWriter, r *http.Request) {
	data, err := scripts.ReadFile("scripts/" + path.Base(r.URL.Path))
	if err != nil {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	w.Write(data)
}

var agentFileRE = regexp.MustCompile(`^` + regexp.QuoteMeta(version.AgentName) + `-(linux|darwin|windows)-(amd64|arm64)(\.exe)?$`)

// getAgentBinary serves prebuilt agents. The Docker image stores them gzipped;
// clients that don't accept gzip (wget, Windows PowerShell 5) get them unpacked.
// Binaries this hub does not carry are fetched from the matching GitHub release.
func (h *Hub) getAgentBinary(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("file")
	m := agentFileRE.FindStringSubmatch(name)
	if m == nil || (m[1] == "windows") != (m[3] == ".exe") {
		writeError(w, http.StatusNotFound, "unknown agent binary")
		return
	}
	file := filepath.Join(h.cfg.AgentDir, name)
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", name))

	if f, err := os.Open(file + ".gz"); err == nil {
		defer f.Close()
		w.Header().Set("Vary", "Accept-Encoding")
		if strings.Contains(r.Header.Get("Accept-Encoding"), "gzip") {
			if fi, err := f.Stat(); err == nil {
				w.Header().Set("Content-Length", strconv.FormatInt(fi.Size(), 10))
			}
			w.Header().Set("Content-Encoding", "gzip")
			io.Copy(w, f)
			return
		}
		zr, err := gzip.NewReader(f)
		if err != nil {
			h.internalError(w, err)
			return
		}
		io.Copy(w, zr)
		return
	}
	if f, err := os.Open(file); err == nil {
		defer f.Close()
		if fi, err := f.Stat(); err == nil {
			http.ServeContent(w, r, name, fi.ModTime(), f)
			return
		}
	}
	if url := version.ReleaseAssetURL(name); url != "" {
		http.Redirect(w, r, url, http.StatusFound)
		return
	}
	writeError(w, http.StatusNotFound, "this hub build has no agent for "+m[1]+"/"+m[2])
}

// InstallCommands returns ready-to-paste install commands per OS. The web UI builds
// the same commands itself (so the hub URL stays editable); this copy serves the CLI.
func InstallCommands(hubURL, hubKey, token string, allowShell bool) map[string]string {
	sh := func(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }
	ps := func(s string) string { return "'" + strings.ReplaceAll(s, "'", "''") + "'" }
	unix := fmt.Sprintf("curl -fsSL %s | sudo sh -s -- --hub %s --key %s --token %s",
		sh(hubURL+"/install.sh"), sh(hubURL), sh(hubKey), sh(token))
	win := fmt.Sprintf("& ([scriptblock]::Create((irm %s))) -Hub %s -Key %s -Token %s",
		ps(hubURL+"/install.ps1"), ps(hubURL), ps(hubKey), ps(token))
	if allowShell {
		unix += " --allow-shell"
		win += " -AllowShell"
	}
	return map[string]string{"linux": unix, "darwin": unix, "windows": win}
}
