package httpshared

import (
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// PromoteDest represents a named promote destination (shared dir or extra).
type PromoteDest struct {
	Name string `json:"name"`
	Path string `json:"path"`
}

// ResolveTargetBase resolves a targetBase string against the list of
// destinations. If targetBase is empty, returns sharedDir (default). Returns
// error if targetBase doesn't match any destination path.
func ResolveTargetBase(targetBase, sharedDir string, dests []PromoteDest) (string, error) {
	if targetBase == "" {
		return sharedDir, nil
	}
	for _, d := range dests {
		if d.Path == targetBase {
			return d.Path, nil
		}
	}
	return "", errors.New("destino inválido: " + targetBase)
}

// SanitizeSubdir valida o subdir digitado pelo usuário pra não escapar do
// sharedDir via "..", caminhos absolutos. Retorna o caminho limpo (Clean) ou
// erro descritivo.
func SanitizeSubdir(subdir string) (string, error) {
	if subdir == "" {
		return "", nil
	}
	if filepath.IsAbs(subdir) {
		return "", errors.New("subdir não pode ser absoluto")
	}
	clean := filepath.Clean(subdir)
	for _, seg := range strings.Split(clean, string(filepath.Separator)) {
		if seg == ".." {
			return "", errors.New("subdir não pode conter '..'")
		}
	}
	if clean == "." {
		return "", nil
	}
	return clean, nil
}

// ListDirs returns the sorted names of the non-hidden subdirectories in
// entries. Always non-nil so a folder with no subdirs serializes as JSON []
// (not null): the UI does `dirs.length` on the result.
func ListDirs(entries []os.DirEntry) []string {
	dirs := []string{}
	for _, e := range entries {
		if e.IsDir() && !strings.HasPrefix(e.Name(), ".") {
			dirs = append(dirs, e.Name())
		}
	}
	sort.Strings(dirs)
	return dirs
}
