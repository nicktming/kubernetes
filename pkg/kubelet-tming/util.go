package kubelet_tming

import (
	"os"
)

// dirExists returns true if the path exists and represents a directory.
func dirExists(path string) bool {
	s, err := os.Stat(path)
	if err != nil {
		return false
	}
	return s.IsDir()
}

// empty is a placeholder type used to implement a set
type empty struct{}
