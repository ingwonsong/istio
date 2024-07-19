package entrypoint

import (
	"log"

	"github.com/spf13/cobra"
)

var (
	// Used for flags.
	ProtoFile       string
	OutputDirectory string

	RootCmd = &cobra.Command{
		Use:   "taaa",
		Short: "Root command",
	}
)

func Execute() {
	err := RootCmd.Execute()
	if err != nil {
		log.Fatal(err)
	}
}

func init() {
	RootCmd.PersistentFlags().StringVar(&ProtoFile, "proto", "", "Binary proto message filename")
	RootCmd.PersistentFlags().StringVar(&OutputDirectory, "output-directory", "", "If the subcommand outputs files, place them in this directory")

	RootCmd.AddCommand(RunCmd)
	RootCmd.AddCommand(UtilCmd)
}
