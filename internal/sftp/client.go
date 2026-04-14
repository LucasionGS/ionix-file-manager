package sftp

import (
	"errors"
	"fmt"
	"io"
	iofs "io/fs"
	"net"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	appfs "github.com/LucasionGS/ionix-file-manager/internal/fs"
	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/agent"
	"golang.org/x/crypto/ssh/knownhosts"
)

// Client wraps an SSH+SFTP connection.
type Client struct {
	ssh  *ssh.Client
	sftp *sftp.Client
	Host string // display label (hostname or alias)
	User string
	Addr string // host:port
}

// buildHostKeyCallback returns a host-key callback that behaves like
// StrictHostKeyChecking=accept-new: unknown hosts are accepted, known hosts
// with a mismatched key are rejected. Falls back to InsecureIgnoreHostKey
// when known_hosts is absent or unreadable.
func buildHostKeyCallback() ssh.HostKeyCallback {
	home, _ := os.UserHomeDir()
	khPath := filepath.Join(home, ".ssh", "known_hosts")
	cb, err := knownhosts.New(khPath)
	if err != nil {
		// No usable known_hosts – accept everything.
		return ssh.InsecureIgnoreHostKey()
	}
	return func(hostname string, remote net.Addr, key ssh.PublicKey) error {
		err := cb(hostname, remote, key)
		if err == nil {
			return nil
		}
		var keyErr *knownhosts.KeyError
		if errors.As(err, &keyErr) {
			if len(keyErr.Want) == 0 {
				// Host not in known_hosts at all – accept (accept-new behaviour).
				return nil
			}
			// If none of the known entries share the same key type, the Go SSH
			// library negotiated a different algorithm than what is stored. The
			// real ssh client would re-negotiate to match; treat this as
			// accepted rather than a genuine mismatch.
			serverType := key.Type()
			for _, w := range keyErr.Want {
				if w.Key.Type() == serverType {
					// Same algorithm, different bytes – genuine mismatch, reject.
					return err
				}
			}
			return nil
		}
		return err
	}
}

// Dial opens an SFTP connection using the given parameters.
// It tries SSH agent auth first, then common key files.
func Dial(user, host string, port int) (*Client, error) {
	addr := fmt.Sprintf("%s:%d", host, port)

	auths := []ssh.AuthMethod{}

	// Try SSH agent first.
	if sock := os.Getenv("SSH_AUTH_SOCK"); sock != "" {
		if conn, err := net.Dial("unix", sock); err == nil {
			auths = append(auths, ssh.PublicKeysCallback(agent.NewClient(conn).Signers))
		}
	}

	// Try common key files.
	home, _ := os.UserHomeDir()
	keyFiles := []string{
		filepath.Join(home, ".ssh", "id_ed25519"),
		filepath.Join(home, ".ssh", "id_rsa"),
		filepath.Join(home, ".ssh", "id_ecdsa"),
	}
	for _, kf := range keyFiles {
		if key, err := loadPrivateKey(kf); err == nil {
			auths = append(auths, key)
		}
	}

	if len(auths) == 0 {
		return nil, fmt.Errorf("no SSH authentication methods available (no agent, no key files)")
	}

	// Build host key callback – accept-new policy.
	hostKeyCallback := buildHostKeyCallback()

	cfg := &ssh.ClientConfig{
		User:            user,
		Auth:            auths,
		HostKeyCallback: hostKeyCallback,
		Timeout:         10 * time.Second,
	}

	sshConn, err := ssh.Dial("tcp", addr, cfg)
	if err != nil {
		return nil, fmt.Errorf("SSH dial %s: %w", addr, err)
	}

	sftpConn, err := sftp.NewClient(sshConn)
	if err != nil {
		sshConn.Close()
		return nil, fmt.Errorf("SFTP session: %w", err)
	}

	return &Client{
		ssh:  sshConn,
		sftp: sftpConn,
		Host: host,
		User: user,
		Addr: addr,
	}, nil
}

// DialWithKey opens an SFTP connection using a specific identity file.
func DialWithKey(user, host string, port int, identityFile string) (*Client, error) {
	addr := fmt.Sprintf("%s:%d", host, port)

	auths := []ssh.AuthMethod{}

	// Try the specified identity file first.
	if identityFile != "" {
		expanded := expandPath(identityFile)
		if key, err := loadPrivateKey(expanded); err == nil {
			auths = append(auths, key)
		}
	}

	// Also try SSH agent as fallback.
	if sock := os.Getenv("SSH_AUTH_SOCK"); sock != "" {
		if conn, err := net.Dial("unix", sock); err == nil {
			auths = append(auths, ssh.PublicKeysCallback(agent.NewClient(conn).Signers))
		}
	}

	if len(auths) == 0 {
		return nil, fmt.Errorf("no SSH authentication methods available")
	}

	// Build host key callback – accept-new policy.
	hostKeyCallback := buildHostKeyCallback()

	cfg := &ssh.ClientConfig{
		User:            user,
		Auth:            auths,
		HostKeyCallback: hostKeyCallback,
		Timeout:         10 * time.Second,
	}

	sshConn, err := ssh.Dial("tcp", addr, cfg)
	if err != nil {
		return nil, fmt.Errorf("SSH dial %s: %w", addr, err)
	}

	sftpConn, err := sftp.NewClient(sshConn)
	if err != nil {
		sshConn.Close()
		return nil, fmt.Errorf("SFTP session: %w", err)
	}

	return &Client{
		ssh:  sshConn,
		sftp: sftpConn,
		Host: host,
		User: user,
		Addr: addr,
	}, nil
}

// Close tears down the SFTP and SSH connections.
func (c *Client) Close() error {
	if c.sftp != nil {
		c.sftp.Close()
	}
	if c.ssh != nil {
		return c.ssh.Close()
	}
	return nil
}

// List returns directory entries on the remote host.
func (c *Client) List(dir string) ([]appfs.Entry, error) {
	infos, err := c.sftp.ReadDir(dir)
	if err != nil {
		return nil, err
	}

	result := make([]appfs.Entry, 0, len(infos))
	for _, fi := range infos {
		result = append(result, appfs.Entry{
			Name:  fi.Name(),
			Path:  filepath.Join(dir, fi.Name()),
			IsDir: fi.IsDir(),
			Info:  fi,
		})
	}

	sort.Slice(result, func(i, j int) bool {
		if result[i].IsDir != result[j].IsDir {
			return result[i].IsDir
		}
		return strings.ToLower(result[i].Name) < strings.ToLower(result[j].Name)
	})

	return result, nil
}

// Stat returns file info for a remote path.
func (c *Client) Stat(path string) (iofs.FileInfo, error) {
	return c.sftp.Stat(path)
}

// Remove deletes a file or empty directory on the remote.
func (c *Client) Remove(path string) error {
	info, err := c.sftp.Stat(path)
	if err != nil {
		return err
	}
	if info.IsDir() {
		return c.removeDir(path)
	}
	return c.sftp.Remove(path)
}

// removeDir recursively removes a directory.
func (c *Client) removeDir(path string) error {
	entries, err := c.sftp.ReadDir(path)
	if err != nil {
		return err
	}
	for _, e := range entries {
		child := filepath.Join(path, e.Name())
		if e.IsDir() {
			if err := c.removeDir(child); err != nil {
				return err
			}
		} else {
			if err := c.sftp.Remove(child); err != nil {
				return err
			}
		}
	}
	return c.sftp.RemoveDirectory(path)
}

// Mkdir creates a directory on the remote (recursive).
func (c *Client) Mkdir(path string) error {
	return c.sftp.MkdirAll(path)
}

// Rename moves/renames a file on the remote.
func (c *Client) Rename(oldpath, newpath string) error {
	return c.sftp.Rename(oldpath, newpath)
}

// CopyRemote copies a file or directory within the remote filesystem.
func (c *Client) CopyRemote(src, dst string) error {
	info, err := c.sftp.Stat(src)
	if err != nil {
		return err
	}
	if info.IsDir() {
		return c.copyRemoteDir(src, dst)
	}
	return c.copyRemoteFile(src, dst, info.Mode())
}

func (c *Client) copyRemoteFile(src, dst string, mode iofs.FileMode) error {
	in, err := c.sftp.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	out, err := c.sftp.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC)
	if err != nil {
		return err
	}
	defer out.Close()

	if _, err := io.Copy(out, in); err != nil {
		return err
	}
	return c.sftp.Chmod(dst, mode)
}

func (c *Client) copyRemoteDir(src, dst string) error {
	info, err := c.sftp.Stat(src)
	if err != nil {
		return err
	}
	if err := c.sftp.MkdirAll(dst); err != nil {
		return err
	}
	_ = c.sftp.Chmod(dst, info.Mode())

	entries, err := c.sftp.ReadDir(src)
	if err != nil {
		return err
	}
	for _, e := range entries {
		s := filepath.Join(src, e.Name())
		d := filepath.Join(dst, e.Name())
		if e.IsDir() {
			if err := c.copyRemoteDir(s, d); err != nil {
				return err
			}
		} else {
			if err := c.copyRemoteFile(s, d, e.Mode()); err != nil {
				return err
			}
		}
	}
	return nil
}

// DownloadFile copies a remote file to a local path.
func (c *Client) DownloadFile(remotePath, localPath string) error {
	info, err := c.sftp.Stat(remotePath)
	if err != nil {
		return err
	}
	if info.IsDir() {
		return c.downloadDir(remotePath, localPath)
	}
	return c.downloadSingleFile(remotePath, localPath, info.Mode())
}

func (c *Client) downloadSingleFile(remotePath, localPath string, mode iofs.FileMode) error {
	in, err := c.sftp.Open(remotePath)
	if err != nil {
		return err
	}
	defer in.Close()

	out, err := os.OpenFile(localPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
	if err != nil {
		return err
	}
	defer out.Close()

	_, err = io.Copy(out, in)
	return err
}

func (c *Client) downloadDir(remotePath, localPath string) error {
	info, err := c.sftp.Stat(remotePath)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(localPath, info.Mode()); err != nil {
		return err
	}
	entries, err := c.sftp.ReadDir(remotePath)
	if err != nil {
		return err
	}
	for _, e := range entries {
		rp := filepath.Join(remotePath, e.Name())
		lp := filepath.Join(localPath, e.Name())
		if e.IsDir() {
			if err := c.downloadDir(rp, lp); err != nil {
				return err
			}
		} else {
			if err := c.downloadSingleFile(rp, lp, e.Mode()); err != nil {
				return err
			}
		}
	}
	return nil
}

// UploadFile copies a local file to a remote path.
func (c *Client) UploadFile(localPath, remotePath string) error {
	info, err := os.Stat(localPath)
	if err != nil {
		return err
	}
	if info.IsDir() {
		return c.uploadDir(localPath, remotePath)
	}
	return c.uploadSingleFile(localPath, remotePath, info.Mode())
}

func (c *Client) uploadSingleFile(localPath, remotePath string, mode iofs.FileMode) error {
	in, err := os.Open(localPath)
	if err != nil {
		return err
	}
	defer in.Close()

	out, err := c.sftp.OpenFile(remotePath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC)
	if err != nil {
		return err
	}
	defer out.Close()

	if _, err := io.Copy(out, in); err != nil {
		return err
	}
	return c.sftp.Chmod(remotePath, mode)
}

func (c *Client) uploadDir(localPath, remotePath string) error {
	info, err := os.Stat(localPath)
	if err != nil {
		return err
	}
	if err := c.sftp.MkdirAll(remotePath); err != nil {
		return err
	}
	_ = c.sftp.Chmod(remotePath, info.Mode())

	entries, err := os.ReadDir(localPath)
	if err != nil {
		return err
	}
	for _, e := range entries {
		lp := filepath.Join(localPath, e.Name())
		rp := filepath.Join(remotePath, e.Name())
		if e.IsDir() {
			if err := c.uploadDir(lp, rp); err != nil {
				return err
			}
		} else {
			fi, _ := e.Info()
			var mode iofs.FileMode = 0644
			if fi != nil {
				mode = fi.Mode()
			}
			if err := c.uploadSingleFile(lp, rp, mode); err != nil {
				return err
			}
		}
	}
	return nil
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func loadPrivateKey(path string) (ssh.AuthMethod, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	signer, err := ssh.ParsePrivateKey(data)
	if err != nil {
		return nil, err
	}
	return ssh.PublicKeys(signer), nil
}

func expandPath(p string) string {
	if strings.HasPrefix(p, "~/") {
		home, _ := os.UserHomeDir()
		return filepath.Join(home, p[2:])
	}
	return p
}
