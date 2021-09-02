# README

`tester` is a program for running ASM tests on existing k8s clusters. It
implements the [kubetest2 pipeline tester
interface](http://go/kubetest2-pipeline-tester-interface) and can be run as an
executable by the kubetest2 main program, e.g. `kubetest2 gke --up --tester=exec
 tester --run-tests`.

## Tester pipeline

With the `tester` implementation, the ASM test flow is divided into 7 steps:

### Setup Environment

In this step, necessary setups need to be run to prepare the test environment.

Normally they should include:

- Parse and validate the tester flags

- Fix any cluster configs that are needed for running ASM tests

- Setup permissions

- ...

### Setup System

In this step, the ASM images need to be built (if installing from source), and
ASM control plane needs to be installed.

### Setup Tests

Anything after installing ASM and before running the Go tests should be done in
this step.

Normally they include:

- Construct the test flags and inject environment variables.

- Run the test specific setups that are out of the Go test code.

### Run Tests

This step is expected to only run the `make [test_target]` command which
delegates to `go test` command.

### Teardown Tests

This step will teardown all the setups done in the `Setup Tests` step.

### Teardown System

This step will teardown all the setups done in the `Teardown System` step.

### Teardown Env

This step will teardown all the setups done in the `Teardown Env` step.

## Why is tester better than Bash?

1. It's written in Go, the same as all other ASM code. And it's much easier to write unit tests.

1. By written in Go, we can more easily avoid using shared environment variables,
   which are hard to discover and can be easy to get conflicts.

1. With the tester implementation, the result of each step is wrapped into a
   JUnit test with the error message if the step fails. With the result it'll be
   much easier to troubleshoot by using the UI of the CI tools.

1. With the pipeline tester, developers can choose to run arbitray test steps
   intead of always running the whole flow from scratch. For example, if someone
   only want to install ASM but not run the tests, they can run the tester CLI
   without passing the `--run-tests` flag.
