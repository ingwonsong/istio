package entrypoint

import (
	"github.com/spf13/cobra"
)

var (
	RunCmd = &cobra.Command{
		Use:          "run",
		Short:        "Run the tests",
		Args:         cobra.NoArgs,
		SilenceUsage: true, // Without this, an error condition prints usage at the end. We shouldn't really need usage but it can be obtained with --help if desired.
		// The user is expected to implement RunE
	}
)
