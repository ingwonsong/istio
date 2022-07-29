# ASM test infrastructure

This folder contains the test infrastruture code for ASM, which are mainly
used by [Prow](http://prow-gob.gcpnode.com/) to run the integration test jobs.

## Layout

For better maintainability, The infrastructure setup and test execution phases
are decoupled and implemented in separate programs.

### Infrastructure setup

[infra](./infra) is used as the entrypoint for running Prow jobs. It brings up
the Kubernetes cluster(s) based on the input flags, and then invokes
[tester](./tester) which will setup the SUT and run the tests.

This script and the relevant helper functions are maintained by the [SOSS EngProd
team](https://moma.corp.google.com/team/2118084542162).

### SUT setup and test execution

[tester](./tester) will setup the SUT (in this case ASM
control plane), and execute the test cases. Some env vars used in this program
are injected by [infra](./infra), so they will only be available if it is run by
integ-suite-kubetest2.sh (a lightweight script that wraps the infra tool), which
is the case for the test jobs run with Prow.

This script and the relevant helper functions are maintained by the
[ASM/CSM team](https://moma.corp.google.com/team/12217806498).