package agent

import (
	"errors"
	"io"

	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"
)

// serveSFTP runs an SFTP server on the channel, with the agent's rights (root or
// SYSTEM). On Windows "/" lists the drives, and paths look like /C:/Users.
func (a *Agent) serveSFTP(ch ssh.Channel) {
	srv, err := sftp.NewServer(ch, sftp.WithServerWorkingDirectory(homeDir()), sftp.WindowsRootEnumeratesDrives())
	if err != nil {
		a.log.Warn("starting file transfer failed", "err", err)
		return
	}
	defer srv.Close()
	if err := srv.Serve(); err != nil && !errors.Is(err, io.EOF) {
		a.log.Warn("file transfer ended with an error", "err", err)
	}
}
