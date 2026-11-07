package main

// The offline POSIX retention tool is not available on Windows. Preserve the
// existing read-only workbench rather than inventing a Windows lock guarantee.
func opsRetainGeneration(string) (func(), error) { return func() {}, nil }
