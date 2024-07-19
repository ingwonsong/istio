package registry

import (
	"fmt"

	"github.com/google/go-containerregistry/pkg/crane"
)

// CopyOut copies all images on the local registry (running on localhost:5000)
//  to paths under the one you pass in.
// i.e. if your local registry has this image:
//   localhost:5000/example/image:55
// CopyOut("gcr.io/bucket/subdir") will copy it to:
//   gcr.io/bucket/subdir/example/image:55
// destPath must not end with a /
func (o *RegistryServer) CopyOut(destPath string) error {
	catalog, err := crane.Catalog(o.serverURL)
	if err != nil {
		return fmt.Errorf("catalog of localhost failed: %v", err)
	}
	for _, repo := range catalog {
		tags, err := crane.ListTags(o.serverURL + "/" + repo)
		if err != nil {
			return fmt.Errorf("couldn't list tags for %q: %v", repo, err)
		}
		for _, tag := range tags {
			src := fmt.Sprintf("%s/%s:%s", o.serverURL, repo, tag)
			dst := fmt.Sprintf("%s/%s:%s", destPath, repo, tag)
			if err := crane.Copy(src, dst); err != nil {
				return fmt.Errorf("failed copying %q: %v", src, err)
			}
		}
	}
	return nil
}
