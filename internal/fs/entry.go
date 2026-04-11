package fs

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

type Entry struct {
	Name  string
	Path  string
	IsDir bool
	Info  fs.FileInfo
}

func List(dir string) ([]Entry, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}

	result := make([]Entry, 0, len(entries))
	for _, e := range entries {
		info, err := e.Info()
		if err != nil {
			continue
		}
		result = append(result, Entry{
			Name:  e.Name(),
			Path:  filepath.Join(dir, e.Name()),
			IsDir: e.IsDir(),
			Info:  info,
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

func IsHidden(name string) bool {
	return strings.HasPrefix(name, ".")
}

// IsImage reports whether the file extension is a common raster image format.
func IsImage(name string) bool {
	switch strings.ToLower(filepath.Ext(name)) {
	case ".png", ".jpg", ".jpeg", ".gif", ".webp", ".bmp", ".tiff", ".tif", ".avif":
		return true
	}
	return false
}

// IsText reports whether the file at path is likely a text file.
// It checks for known binary extensions first, then reads up to 512 bytes
// and returns false if any null byte is found.
func IsText(path string) bool {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".png", ".jpg", ".jpeg", ".gif", ".webp", ".bmp", ".tiff", ".tif", ".avif",
		".pdf", ".zip", ".tar", ".gz", ".bz2", ".xz", ".7z", ".rar",
		".exe", ".so", ".dylib", ".bin", ".dmg", ".iso",
		".mp3", ".mp4", ".mkv", ".avi", ".mov", ".flac", ".ogg", ".wav",
		".ttf", ".otf", ".woff", ".woff2":
		return false
	}
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()
	buf := make([]byte, 512)
	n, _ := f.Read(buf)
	// 0-byte files are valid text files (no binary content).
	if n == 0 {
		return true
	}
	for _, b := range buf[:n] {
		if b == 0 {
			return false
		}
	}
	return true
}

// ImageMIME returns the MIME type for a recognised image filename, or an error.
func ImageMIME(name string) (string, error) {
	switch strings.ToLower(filepath.Ext(name)) {
	case ".png":
		return "image/png", nil
	case ".jpg", ".jpeg":
		return "image/jpeg", nil
	case ".gif":
		return "image/gif", nil
	case ".webp":
		return "image/webp", nil
	case ".bmp":
		return "image/bmp", nil
	case ".tiff", ".tif":
		return "image/tiff", nil
	case ".avif":
		return "image/avif", nil
	default:
		return "", fmt.Errorf("unsupported image type: %s", filepath.Ext(name))
	}
}
