// Package git contains methods for interacting with a git working directory
package git

import "github.com/magefile/mage/sh"

// DescribeCWD returns a string about the git repo's working directory
// It in the format of SHA{-dirty} i.e. f270d1af37cd5bb114fb7134b3d985b5e2999abf-dirty
func DescribeCWD() (string, error) {
	return sh.Output("git", "describe", "--match", "^$", "--dirty", "--always", "--abbrev=40")
}
