package entrypoint

import (
	"log"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"
)

var (
	UtilCmd = &cobra.Command{
		Use:   "util",
		Short: "Debugging utilities",
		Args:  cobra.NoArgs,
	}
	DumpProtoCmd = &cobra.Command{
		Use:   "dump-proto",
		Short: "Print out proto in human-readable form",
		Args:  cobra.NoArgs,
		// The user is expected to override Run if they want this feature
		Run: func(cmd *cobra.Command, args []string) {
			log.Fatal("Not implemented")
		},
	}
	generateOutputCmd = &cobra.Command{
		Use:   "generate-output",
		Short: "Generate some files in the output directory (if given)",
		Args:  cobra.NoArgs,
		Run: func(cmd *cobra.Command, args []string) {
			if OutputDirectory != "" {
				if err := os.MkdirAll(OutputDirectory, 0775); err != nil {
					log.Fatal(err)
				}
				genFile := func(filename, contents string) {
					name := filepath.Join(OutputDirectory, filename)
					f, err := os.Create(name)
					if err != nil {
						log.Fatal(err)
					}
					f.WriteString(contents + "\n")
					f.Close()
					log.Printf("Created file %s\n", name)
				}
				for _, x := range []string{"hello", "world"} {
					genFile(x, x)
				}
				genFile("junit_Passing.xml", `<testsuites>
<testsuite tests="2" failures="0" time="0.080000" name="passing/package_name">
<testcase classname="passing/package_name" name="TestParent/SubTest" time="0.000000"/>
<testcase classname="passing/package_name" name="TestParent" time="0.000000"/>
</testsuite>
</testsuites>
`)
				genFile("junit_PassFail.xml", `<testsuites>
<testsuite tests="4" failures="1" time="0.080000" name="passing/failing/package-name">
<testcase classname="passing/failing/package-name" name="TestParent/FailingSub" time="0.000000"><failure message="Failed" type="">I am a failure message. Huzzah.</failure></testcase>
<testcase classname="passing/failing/package-name" name="TestParent/SkippedSub" time="0.000000"><skipped message="I am a skipping message. Huzzoo."/></testcase>
<testcase classname="passing/failing/package-name" name="TestParent/PassingSub" time="0.000000"/>
<testcase classname="passing/failing/package-name" name="TestParent" time="0.000000"/>
</testsuite>
</testsuites>
`)
			}

		},
	}
	generatePassingOutputCmd = &cobra.Command{
		Use:   "generate-pass-output",
		Short: "Generate passing file in the output directory (if given)",
		Args:  cobra.NoArgs,
		Run: func(cmd *cobra.Command, args []string) {
			if OutputDirectory != "" {
				if err := os.MkdirAll(OutputDirectory, 0775); err != nil {
					log.Fatal(err)
				}
				genFile := func(filename, contents string) {
					name := filepath.Join(OutputDirectory, filename)
					f, err := os.Create(name)
					if err != nil {
						log.Fatal(err)
					}
					f.WriteString(contents + "\n")
					f.Close()
					log.Printf("Created file %s\n", name)
				}
				for _, x := range []string{"hello", "world"} {
					genFile(x, x)
				}
				genFile("junit_Passing.xml", `<testsuites>
<testsuite tests="2" failures="0" time="0.080000" name="passing/package_name">
<testcase classname="passing/package_name" name="TestParent/SubTest" time="0.000000"/>
<testcase classname="passing/package_name" name="TestParent" time="0.000000"/>
</testsuite>
</testsuites>
`)
			}

		},
	}
)

func init() {
	UtilCmd.AddCommand(DumpProtoCmd)
	UtilCmd.AddCommand(generateOutputCmd)
}
