//go:build !windows

package engine

import "os"

// Automatic deletion currently requires Windows handle identity checks.
func createdFileIdentity(_ *os.File) string { return "" }
