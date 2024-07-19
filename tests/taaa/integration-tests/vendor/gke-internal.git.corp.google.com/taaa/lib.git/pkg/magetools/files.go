package magetools

import (
	"os"
)

// CopyFile copies a file from the given path to the other path.
// Optionally takes FileMode bits that are Or'ed to make the new file's
// permissions. Will otherwise create the copy with the same permissions.
// Golang does not provide this with such a simple signature by default.
func CopyFile(source, dest string, fileModes ...os.FileMode) error {
	scriptContent, err := os.ReadFile(source)
	if err != nil {
		return err
	}
	var newFilePerms os.FileMode
	if len(fileModes) > 0 {
		for _, perm := range fileModes {
			newFilePerms |= perm
		}
	} else {
		info, err := os.Stat(source)
		if err != nil {
			return err
		}
		newFilePerms = info.Mode()
	}
	return os.WriteFile(dest, scriptContent, newFilePerms)
}
