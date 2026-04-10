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
