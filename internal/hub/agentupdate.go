package hub

import (
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"time"

	"golang.org/x/crypto/ssh"

	"github.com/Jackolix/Lotse/internal/hub/store"
	"github.com/Jackolix/Lotse/internal/protocol"
	"github.com/Jackolix/Lotse/internal/update"
)

// Agent self-updates: the hub offers the signed agent binaries it carries (release
// builds put a FILE.sig manifest next to each) to agents running an older version.
// The binary travels over the agent's SSH link; the agent checks the signature,
// hash and version before it replaces itself.

// loadUpdates reads the manifests in the agent directory. Only manifests with a
// valid signature and a binary next to them are offered.
func (h *Hub) loadUpdates() {
	h.updates = map[string]*update.Manifest{}
	paths, _ := filepath.Glob(filepath.Join(h.cfg.AgentDir, "*"+update.ManifestExt))
	for _, p := range paths {
		m, err := update.ReadManifest(p)
		if err == nil {
			err = m.Verify()
		}
		if err == nil && m.File+update.ManifestExt != filepath.Base(p) {
			err = fmt.Errorf("manifest is for %s", m.File)
		}
		if err != nil {
			h.log.Warn("ignoring agent update manifest", "file", p, "err", err)
			continue
		}
		bin, err := h.openAgentBinary(m.File)
		if err != nil {
			h.log.Warn("ignoring agent update manifest without binary", "file", p)
			continue
		}
		bin.Close()
		h.updates[m.File] = m
	}
	if len(h.updates) > 0 {
		h.log.Info("signed agent updates available", "platforms", len(h.updates))
	}
}

// openAgentBinary opens a binary from the agent directory, unpacking .gz files.
func (h *Hub) openAgentBinary(name string) (io.ReadCloser, error) {
	file := filepath.Join(h.cfg.AgentDir, name)
	if f, err := os.Open(file + ".gz"); err == nil {
		zr, err := gzip.NewReader(f)
		if err != nil {
			f.Close()
			return nil, err
		}
		return struct {
			io.Reader
			io.Closer
		}{zr, f}, nil
	}
	return os.Open(file)
}

// updateFor returns the update an agent should get, or nil.
func (h *Hub) updateFor(info protocol.SystemInfo, agentVersion string) *update.Manifest {
	if info.OS == "" || info.Arch == "" {
		return nil
	}
	m := h.updates[update.AgentFile(info.OS, info.Arch)]
	if m == nil || !update.Newer(m.Version, agentVersion) {
		return nil
	}
	return m
}

func (h *Hub) postAgentUpdate(w http.ResponseWriter, r *http.Request, s *store.Session) {
	sys, ac, ok := h.onlineAgent(w, r)
	if !ok {
		return
	}
	if !ac.has(protocol.FeatureUpdate) {
		writeError(w, http.StatusConflict, "this agent cannot update itself (it is too old or was installed with --no-updates); re-run the install command")
		return
	}
	m := h.updateFor(ac.info, sys.AgentVersion)
	if m == nil {
		writeError(w, http.StatusConflict, "this hub has no newer signed agent for "+ac.info.OS+"/"+ac.info.Arch)
		return
	}
	detail := fmt.Sprintf("%s → %s", sys.AgentVersion, m.Version)
	fail := func(msg string) {
		h.audit(r, s.Username, "agent_update_failed", sys, detail+": "+msg)
		writeError(w, http.StatusUnprocessableEntity, msg)
	}

	bin, err := h.openAgentBinary(m.File)
	if err != nil {
		h.internalError(w, err)
		return
	}
	defer bin.Close()
	manifest, _ := json.Marshal(m)
	ch, reqs, err := ac.conn.OpenChannel(protocol.ChanUpdate, manifest)
	if err != nil {
		fail("the agent refused the update: " + channelError(err))
		return
	}
	defer ch.Close()
	go ssh.DiscardRequests(reqs)
	timer := time.AfterFunc(5*time.Minute, func() { ch.Close() })
	defer timer.Stop()

	if _, err := io.Copy(ch, bin); err != nil {
		fail("sending the update failed: " + err.Error())
		return
	}
	ch.CloseWrite()
	var res protocol.UpdateResult
	if err := json.NewDecoder(ch).Decode(&res); err != nil {
		fail("the agent did not confirm the update")
		return
	}
	if res.Error != "" {
		fail(res.Error)
		return
	}
	h.log.Info("agent updated", "system", sys.Name, "from", sys.AgentVersion, "to", m.Version, "by", s.Username)
	h.audit(r, s.Username, "agent_updated", sys, detail)
	writeJSON(w, http.StatusOK, map[string]string{"version": m.Version})
}

// updateVersion is the version a system's agent can update to, or "".
func (h *Hub) updateVersion(s *store.System, info protocol.SystemInfo, features []string) string {
	if !slices.Contains(features, protocol.FeatureUpdate) {
		return ""
	}
	if m := h.updateFor(info, s.AgentVersion); m != nil {
		return m.Version
	}
	return ""
}
