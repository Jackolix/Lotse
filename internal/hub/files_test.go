package hub

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Jackolix/Lotse/internal/hub/store"
)

func upload(t *testing.T, base, cookie, path, content string, overwrite bool) int {
	t.Helper()
	u := base + "?path=" + url.QueryEscape(path)
	if overwrite {
		u += "&overwrite=1"
	}
	req, _ := http.NewRequest(http.MethodPut, u, strings.NewReader(content))
	req.Header.Set("Cookie", "session="+cookie)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	return resp.StatusCode
}

func TestFileTransfer(t *testing.T) {
	h, srv := newTestHub(t)
	sys := enroll(t, h, srv, true)
	locked := enroll(t, h, srv, false)
	cookie, _ := sessionFor(t, h, "otto", store.RoleOperator, true)
	base := fmt.Sprintf("%s/api/systems/%d/files", srv.URL, sys.ID)
	dir := filepath.ToSlash(t.TempDir())
	file := dir + "/a.txt"

	plain, _ := sessionFor(t, h, "otto", store.RoleOperator, false)
	if status, body := call(t, "GET", base+"?path="+url.QueryEscape(dir), plain, nil); status != http.StatusForbidden || body["reauth_required"] != true {
		t.Fatalf("listing without re-authentication = %d %v", status, body)
	}
	if status, _ := call(t, "GET", fmt.Sprintf("%s/api/systems/%d/files?path=/", srv.URL, locked.ID), cookie, nil); status != http.StatusForbidden {
		t.Fatalf("agent without --allow-shell = %d", status)
	}

	if status := upload(t, base, cookie, file, "hello", false); status != http.StatusNoContent {
		t.Fatalf("upload = %d", status)
	}
	if status := upload(t, base, cookie, file, "other", false); status != http.StatusConflict {
		t.Errorf("upload over an existing file without overwrite = %d, want 409", status)
	}
	os.Chmod(file, 0o640)
	if status := upload(t, base, cookie, file, "replaced", true); status != http.StatusNoContent {
		t.Fatalf("overwrite = %d", status)
	}
	if data, _ := os.ReadFile(file); string(data) != "replaced" {
		t.Errorf("file content = %q", data)
	}
	if fi, _ := os.Stat(file); fi.Mode().Perm() != 0o640 {
		t.Errorf("overwriting changed the mode to %v", fi.Mode().Perm())
	}

	_, raw := authedGet(t, base+"?path="+url.QueryEscape(dir), cookie)
	var listing struct {
		Path    string
		Entries []fileEntry
	}
	json.Unmarshal(raw, &listing)
	if listing.Path != dir || len(listing.Entries) != 1 || listing.Entries[0].Name != "a.txt" || listing.Entries[0].Size != 8 {
		t.Fatalf("listing = %s", raw)
	}

	status, data := authedGet(t, base+"/download?path="+url.QueryEscape(file), cookie)
	if status != http.StatusOK || string(data) != "replaced" {
		t.Fatalf("download = %d %q", status, data)
	}

	action := func(body map[string]any) int {
		status, _ := call(t, "POST", base, cookie, body)
		return status
	}
	if status := action(map[string]any{"action": "mkdir", "path": dir + "/sub"}); status != http.StatusNoContent {
		t.Fatalf("mkdir = %d", status)
	}
	if status := action(map[string]any{"action": "rename", "path": file, "to": dir + "/sub/b.txt"}); status != http.StatusNoContent {
		t.Fatalf("rename = %d", status)
	}
	if _, err := os.Stat(dir + "/sub/b.txt"); err != nil {
		t.Fatalf("renamed file missing: %v", err)
	}
	if status := action(map[string]any{"action": "delete", "path": dir + "/sub"}); status < 400 {
		t.Errorf("deleting a non-empty folder without recursive = %d", status)
	}
	if status := action(map[string]any{"action": "delete", "path": dir + "/sub", "recursive": true}); status != http.StatusNoContent {
		t.Fatalf("recursive delete = %d", status)
	}
	if _, err := os.Stat(dir + "/sub"); !os.IsNotExist(err) {
		t.Errorf("folder still exists: %v", err)
	}
	if status, _ := call(t, "GET", base+"/download?path="+url.QueryEscape(dir+"/missing"), cookie, nil); status != http.StatusNotFound {
		t.Errorf("missing file = %d", status)
	}

	entries, _ := h.store.Audit(0, 50)
	var actions []string
	for _, e := range entries {
		actions = append(actions, e.Action)
	}
	for _, want := range []string{"file_uploaded", "file_downloaded", "folder_created", "file_renamed", "file_deleted"} {
		if !strings.Contains(strings.Join(actions, " "), want) {
			t.Errorf("audit log misses %s: %v", want, actions)
		}
	}
}

// Large uploads stream through the hub in both directions.
func TestLargeFileRoundTrip(t *testing.T) {
	h, srv := newTestHub(t)
	sys := enroll(t, h, srv, true)
	cookie, _ := sessionFor(t, h, "otto", store.RoleOperator, true)
	base := fmt.Sprintf("%s/api/systems/%d/files", srv.URL, sys.ID)
	file := filepath.ToSlash(t.TempDir()) + "/big.bin"

	content := strings.Repeat("0123456789abcdef", 1<<16) // 1 MiB
	if status := upload(t, base, cookie, file, content, false); status != http.StatusNoContent {
		t.Fatalf("upload = %d", status)
	}
	req, _ := http.NewRequest(http.MethodGet, base+"/download?path="+url.QueryEscape(file), nil)
	req.Header.Set("Cookie", "session="+cookie)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	if string(data) != content || resp.ContentLength != int64(len(content)) {
		t.Fatalf("round trip: got %d bytes (Content-Length %d)", len(data), resp.ContentLength)
	}
}
