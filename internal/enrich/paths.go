package enrich

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"

	"mrinspect/internal/rag/intake"
)

func resolve(root, rel string) (string, string) {
	if filepath.IsAbs(rel) {
		return "", "path-rejected"
	}

	candidate := filepath.Join(root, filepath.Clean(rel))
	relative, err := filepath.Rel(root, candidate)
	if outsideRoot(relative, err) || relative == "." {
		return "", "path-rejected"
	}

	resolved, err := filepath.EvalSymlinks(candidate)
	if err != nil {
		return "", "path-rejected"
	}
	resolvedRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return "", "path-rejected"
	}
	resolvedRelative, err := filepath.Rel(resolvedRoot, resolved)
	if outsideRoot(resolvedRelative, err) {
		return "", "path-rejected"
	}

	for _, segment := range strings.Split(filepath.ToSlash(rel), "/") {
		if segment == ".git" || segment == ".docker" {
			return "", "path-rejected"
		}
	}

	base := filepath.Base(candidate)
	lowerBase := strings.ToLower(base)
	if intake.IsDenylisted(base) || strings.Contains(lowerBase, "credential") || strings.Contains(lowerBase, "secret") {
		return "", "path-rejected"
	}
	return resolved, ""
}

func outsideRoot(relative string, err error) bool {
	return err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator))
}

func isBinary(file *os.File) bool {
	var sample [8 * 1024]byte
	n, _ := file.Read(sample[:])
	_, _ = file.Seek(0, 0)
	return bytes.IndexByte(sample[:n], 0) >= 0
}

func segmentPrefix(rel, prefix string) bool {
	rel = strings.TrimSuffix(filepath.ToSlash(filepath.Clean(rel)), "/")
	prefix = strings.TrimSuffix(filepath.ToSlash(filepath.Clean(prefix)), "/")
	return rel == prefix || strings.HasPrefix(rel, prefix+"/")
}
