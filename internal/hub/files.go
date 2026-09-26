package hub

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"mime"
	"net/http"
	"os"
	"path"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"

	"github.com/Jackolix/Lotse/internal/hub/store"
	"github.com/Jackolix/Lotse/internal/protocol"
)

// File transfer: the hub is an SFTP client on a session channel to the agent, and
// the browser talks to it over plain HTTP. Files stream through; the hub buffers
// nothing. Like the shell, this needs the agent's opt-in, the operator role and a
// recent re-authentication; changes and downloads are audited.

type fileEntry struct {
	Name  string `json:"name"`
	Size  int64  `json:"size"`
	Mode  string `json:"mode"` // e.g. drwxr-xr-x
	Dir   bool   `json:"dir"`
	Link  bool   `json:"link"`
	MTime int64  `json:"mtime"`
}

// fileSession opens an SFTP client for one request.
func (h *Hub) fileSession(w http.ResponseWriter, r *http.Request, s *store.Session) (*store.System, *sftp.Client, func(), bool) {
	sys, ac, ok := h.onlineAgent(w, r)
	if !ok {
		return nil, nil, nil, false
	}
	if !requireElevated(w, s) {
		return nil, nil, nil, false
	}
	if !ac.has(protocol.FeatureShell) {
		writeError(w, http.StatusForbidden, "file access is disabled on this machine; install the agent with --allow-shell")
		return nil, nil, nil, false
	}
	ch, reqs, err := ac.conn.OpenChannel("session", nil)
	if err != nil {
		writeError(w, http.StatusBadGateway, "the agent refused file access: "+channelError(err))
		return nil, nil, nil, false
	}
	go ssh.DiscardRequests(reqs)
	if ok, err := ch.SendRequest("subsystem", true, ssh.Marshal(struct{ Name string }{protocol.SubsystemSFTP})); err != nil || !ok {
		ch.Close()
		writeError(w, http.StatusBadGateway, "the agent could not start file access")
		return nil, nil, nil, false
	}
	client, err := sftp.NewClientPipe(ch, ch, sftp.UseConcurrentWrites(true))
	if err != nil {
		ch.Close()
		writeError(w, http.StatusBadGateway, "the agent could not start file access")
		return nil, nil, nil, false
	}
	// Signing out or losing the operator role aborts a transfer in progress.
	var once sync.Once
	stop := func() { once.Do(func() { client.Close(); ch.Close() }) }
	untrack := h.track(s, store.RoleOperator, stop)
	return sys, client, func() { untrack(); stop() }, true
}

func channelError(err error) string {
	var oce *ssh.OpenChannelError
	if errors.As(err, &oce) {
		return oce.Message
	}
	return err.Error()
}

// remotePath cleans a path from the browser. Paths are always absolute, with "/"
// separators; on Windows agents they look like /C:/Users.
func remotePath(p string) (string, bool) {
	if p == "" || strings.ContainsRune(p, 0) {
		return "", false
	}
	return path.Clean("/" + strings.ReplaceAll(p, `\`, "/")), true
}

func (h *Hub) fileError(w http.ResponseWriter, err error) {
	var se *sftp.StatusError
	switch {
	case errors.Is(err, fs.ErrNotExist):
		writeError(w, http.StatusNotFound, "no such file or folder")
	case errors.Is(err, fs.ErrPermission):
		writeError(w, http.StatusForbidden, "permission denied")
	case errors.Is(err, fs.ErrExist):
		writeError(w, http.StatusConflict, "that name exists already")
	case errors.As(err, &se):
		writeError(w, http.StatusUnprocessableEntity, strings.TrimPrefix(se.Error(), "sftp: "))
	default:
		writeError(w, http.StatusBadGateway, err.Error())
	}
}

func (h *Hub) getFiles(w http.ResponseWriter, r *http.Request, s *store.Session) {
	dir, ok := remotePath(r.URL.Query().Get("path"))
	if !ok {
		dir = "/"
	}
	_, client, done, ok := h.fileSession(w, r, s)
	if !ok {
		return
	}
	defer done()
	infos, err := client.ReadDir(dir)
	if err != nil {
		h.fileError(w, err)
		return
	}
	entries := make([]fileEntry, 0, len(infos))
	for _, fi := range infos {
		e := fileEntry{Name: fi.Name(), Size: fi.Size(), Mode: fi.Mode().String(), Dir: fi.IsDir(), MTime: fi.ModTime().Unix()}
		if fi.Mode()&fs.ModeSymlink != 0 {
			e.Link = true
			// Show links to folders as folders, so they can be opened.
			if target, err := client.Stat(path.Join(dir, fi.Name())); err == nil {
				e.Dir = target.IsDir()
			}
		}
		entries = append(entries, e)
	}
	slices.SortFunc(entries, func(a, b fileEntry) int {
		if a.Dir != b.Dir {
			if a.Dir {
				return -1
			}
			return 1
		}
		return strings.Compare(strings.ToLower(a.Name), strings.ToLower(b.Name))
	})
	writeJSON(w, http.StatusOK, map[string]any{"path": dir, "entries": entries})
}

func (h *Hub) downloadFile(w http.ResponseWriter, r *http.Request, s *store.Session) {
	p, ok := remotePath(r.URL.Query().Get("path"))
	if !ok {
		writeError(w, http.StatusBadRequest, "missing path")
		return
	}
	sys, client, done, ok := h.fileSession(w, r, s)
	if !ok {
		return
	}
	defer done()
	f, err := client.Open(p)
	if err != nil {
		h.fileError(w, err)
		return
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		h.fileError(w, err)
		return
	}
	if fi.IsDir() {
		writeError(w, http.StatusBadRequest, "folders cannot be downloaded")
		return
	}
	name := path.Base(p)
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": name}))
	w.Header().Set("Content-Length", fmt.Sprint(fi.Size()))
	w.Header().Set("Cache-Control", "no-store")
	start := time.Now()
	n, err := io.Copy(w, f)
	detail := fmt.Sprintf("%s (%s)", p, humanBytes(n))
	if err != nil {
		h.audit(r, s.Username, "file_download_failed", sys, detail+": "+err.Error())
		return
	}
	h.audit(r, s.Username, "file_downloaded", sys, fmt.Sprintf("%s in %s", detail, time.Since(start).Round(time.Second)))
}

// putFile uploads the request body to path. It writes a temporary file next to the
// target and renames it into place, so a failed upload never leaves half a file. An
// existing file is only replaced with ?overwrite=1, and keeps its mode and owner.
func (h *Hub) putFile(w http.ResponseWriter, r *http.Request, s *store.Session) {
	p, ok := remotePath(r.URL.Query().Get("path"))
	if !ok || p == "/" {
		writeError(w, http.StatusBadRequest, "missing path")
		return
	}
	overwrite := r.URL.Query().Get("overwrite") == "1"
	sys, client, done, ok := h.fileSession(w, r, s)
	if !ok {
		return
	}
	defer done()
	existing, err := client.Stat(p)
	if err == nil {
		if existing.IsDir() {
			writeError(w, http.StatusConflict, "a folder with that name exists already")
			return
		}
		if !overwrite {
			writeError(w, http.StatusConflict, "a file with that name exists already")
			return
		}
	}

	tmp := path.Join(path.Dir(p), "."+path.Base(p)+".upload-"+randomToken()[:8])
	f, err := client.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL)
	if err != nil {
		h.fileError(w, err)
		return
	}
	var body io.Reader = r.Body
	if r.ContentLength > 0 {
		body = &io.LimitedReader{R: r.Body, N: r.ContentLength} // a known size enables concurrent writes
	}
	n, err := f.ReadFrom(body)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil && existing != nil {
		client.Chmod(tmp, existing.Mode().Perm())
		if st, ok := existing.Sys().(*sftp.FileStat); ok {
			client.Chown(tmp, int(st.UID), int(st.GID)) // fails harmlessly on Windows
		}
	}
	if err == nil {
		if err = client.PosixRename(tmp, p); err != nil {
			err = client.Rename(tmp, p) // servers without the posix-rename extension
		}
	}
	detail := fmt.Sprintf("%s (%s)", p, humanBytes(n))
	if err != nil {
		client.Remove(tmp)
		h.audit(r, s.Username, "file_upload_failed", sys, detail+": "+err.Error())
		h.fileError(w, err)
		return
	}
	if existing != nil {
		detail += ", replaced the existing file"
	}
	h.audit(r, s.Username, "file_uploaded", sys, detail)
	w.WriteHeader(http.StatusNoContent)
}

// postFileAction creates folders, renames and deletes.
func (h *Hub) postFileAction(w http.ResponseWriter, r *http.Request, s *store.Session) {
	var body struct {
		Action    string `json:"action"` // mkdir, rename, delete
		Path      string `json:"path"`
		To        string `json:"to"`
		Recursive bool   `json:"recursive"`
	}
	if !readJSON(w, r, &body) {
		return
	}
	p, ok := remotePath(body.Path)
	if !ok || p == "/" {
		writeError(w, http.StatusBadRequest, "missing path")
		return
	}
	var to string
	if body.Action == "rename" {
		if to, ok = remotePath(body.To); !ok || to == "/" {
			writeError(w, http.StatusBadRequest, "missing new name")
			return
		}
	}
	sys, client, done, ok := h.fileSession(w, r, s)
	if !ok {
		return
	}
	defer done()

	var err error
	var action, detail string
	switch body.Action {
	case "mkdir":
		action, detail = "folder_created", p
		err = client.Mkdir(p)
	case "rename":
		action, detail = "file_renamed", p+" → "+to
		if _, serr := client.Lstat(to); serr == nil {
			writeError(w, http.StatusConflict, "that name exists already")
			return
		}
		err = client.PosixRename(p, to)
		if err != nil {
			err = client.Rename(p, to) // servers without the posix-rename extension
		}
	case "delete":
		action, detail = "file_deleted", p
		var fi os.FileInfo
		if fi, err = client.Lstat(p); err == nil {
			switch {
			case !fi.IsDir():
				err = client.Remove(p)
			case body.Recursive:
				detail += " (folder with its contents)"
				err = client.RemoveAll(p)
			default:
				err = client.RemoveDirectory(p)
			}
		}
	default:
		writeError(w, http.StatusBadRequest, "action must be mkdir, rename or delete")
		return
	}
	if err != nil {
		h.fileError(w, err)
		return
	}
	h.audit(r, s.Username, action, sys, detail)
	w.WriteHeader(http.StatusNoContent)
}
