package fs

import (
	"io"
	"os"
	"path/filepath"
)

// CopyEntry copies a file or directory (recursively) from src to dst.
func CopyEntry(src, dst string) error {
	info, err := os.Stat(src)
	if err != nil {
		return err
	}
	if info.IsDir() {
		return copyDir(src, dst)
	}
	return copyFile(src, dst, info.Mode())
}

func copyFile(src, dst string, mode os.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
	if err != nil {
		return err
	}
	defer out.Close()

	_, err = io.Copy(out, in)
	return err
}

func copyDir(src, dst string) error {
	info, err := os.Stat(src)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dst, info.Mode()); err != nil {
		return err
	}
	entries, err := os.ReadDir(src)
	if err != nil {
		return err
	}
	for _, e := range entries {
		s := filepath.Join(src, e.Name())
		d := filepath.Join(dst, e.Name())
		if e.IsDir() {
			if err := copyDir(s, d); err != nil {
				return err
			}
		} else {
			fi, _ := e.Info()
			var mode os.FileMode = 0644
			if fi != nil {
				mode = fi.Mode()
			}
			if err := copyFile(s, d, mode); err != nil {
				return err
			}
		}
	}
	return nil
}

// DeleteEntry removes a file or directory (recursively).
func DeleteEntry(path string) error {
	return os.RemoveAll(path)
}

// MoveEntry moves src to dst. Falls back to copy+delete on cross-device moves.
func MoveEntry(src, dst string) error {
	if err := os.Rename(src, dst); err == nil {
		return nil
	}
	if err := CopyEntry(src, dst); err != nil {
		return err
	}
	return os.RemoveAll(src)
}
