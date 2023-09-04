package fakefilesystem

import (
	"fmt"
	"os"
	"path"
)

// FS allows to setup isolated fake files structure used for the tests.
type FS struct {
	Dirs     []string
	Files    map[string][]byte
	Symlinks map[string]string
}

// Use function creates entire files structure and returns a function to tear it down.
// Example usage:
//
// ```
// rootDir, fakeFsClean, err := defer fs.Use()
// if err != nil { ... }
// defer fakeFsClean()
// ````
func (f *FS) Use() (string, func(), error) {
	// create the new fake fs root dir in /tmp/sriov...
	rootDir, err := os.MkdirTemp("", "sriov-operator")
	if err != nil {
		return "", nil, fmt.Errorf("error creating fake root dir: %w", err)
	}

	for _, dir := range f.Dirs {
		err := os.MkdirAll(path.Join(rootDir, dir), 0755)
		if err != nil {
			return "", nil, fmt.Errorf("error creating fake directory: %w", err)
		}
	}

	for filename, body := range f.Files {
		err := os.WriteFile(path.Join(rootDir, filename), body, 0600)
		if err != nil {
			return "", nil, fmt.Errorf("error creating fake file: %w", err)
		}
	}

	for link, target := range f.Symlinks {
		err = os.Symlink(target, path.Join(rootDir, link))
		if err != nil {
			return "", nil, fmt.Errorf("error creating fake symlink: %w", err)
		}
	}

	return rootDir, func() {
		// remove temporary fake fs
		err := os.RemoveAll(rootDir)
		if err != nil {
			panic(fmt.Errorf("error tearing down fake filesystem: %w", err))
		}
	}, nil
}
