//go:build !windows

package main

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
)

var opsGenerationName = regexp.MustCompile(`^[a-f0-9]{32}$`)

// Hold a shared root lease for the whole server lifetime, including startup.
// Offline retention needs an exclusive lease, so no generation in this family
// can be retired while an updated reader is serving or loading it. This is not
// an automatic janitor or an assertion about non-participating older binaries.
func opsRetainGeneration(overview string) (func(), error) {
	noop := func() {}
	if overview == "" {
		return noop, nil
	}
	absolute, err := filepath.Abs(overview)
	if err != nil {
		return nil, err
	}
	packageDir := filepath.Dir(absolute)
	if filepath.Base(absolute) != "ops.json" || !opsGenerationName.MatchString(filepath.Base(packageDir)) {
		return noop, nil
	}
	root := filepath.Dir(packageDir)
	resolved, err := filepath.EvalSymlinks(root)
	if err != nil || resolved != root {
		return noop, nil
	}
	info, err := os.Lstat(root)
	if err != nil || !info.IsDir() || info.Mode().Perm() != 0700 || !opsPrivateOwner(info) {
		return noop, nil
	}
	release, err := opsRetentionReadLock(filepath.Join(root, ".retention.lock"))
	if err != nil {
		return nil, err
	}
	current, err := os.Lstat(root)
	if err != nil || !os.SameFile(info, current) {
		release()
		return nil, fmt.Errorf("generation root changed")
	}
	return release, nil
}
