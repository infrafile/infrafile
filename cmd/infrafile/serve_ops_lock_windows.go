package main

import (
	"fmt"
	"os"
)

func opsPrivateOwner(os.FileInfo) bool { return false }

func opsGenerationLock(string) (func(), error) {
	return nil, fmt.Errorf("shared POSIX lock unavailable")
}

func opsRetentionReadLock(string) (func(), error) {
	return nil, fmt.Errorf("shared POSIX lock unavailable")
}
