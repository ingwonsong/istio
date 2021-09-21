# Test Artifact

The image here compiles tests and stores them in an OCI image. We use
them to get test results into GKE Networking Guitar dataplanev2 TestGrid.

The image bundles in the tests images used by the integration tests,
the test binaries themselves and the `Tester` application at
`$REPO_ROOT/prow/asm/tester`. The Tester is used to install ASM on the
clusters and handle the necessary environment det up.

## Compiler image

The [`compiler.dockerfile`](compiler.dockerfile) specifies an image to
used to compile the istio integration tests. Golang is usually statically
compiled so needing to compile in the same environment as where it runs is
usually not needed. However the tests have dependencies on code that use
C/C++ networking DLLs. These libraries cause a panic during `init` if the
compiled Go was not built with a matching networking library:

``` txt
panic: qtls.Config doesn't match

goroutine 1 [running]:
github.com/marten-seemann/qtls-go1-17.init.0()
        /usr/local/google/home/root/go/pkg/mod/github.com/marten-seemann/qtls-go1-17@v0.1.0-rc.1/unsafe.go:20 +0x185
```

## Compiling the Image

[Mage](https://magefile.org/) is used to build the image here, which is the
standard build tool used by TaaA. Install mage and then you can see all the
build targets with `mage -l` in this directory. You mostly just need to know
`mage build:artifact` to build the whole image from scratch.

## Local run

Once you've built the image, create clusters. This is most easily done with
[kubetest2](http://go/kubetest2#installation). The following command can be
used:

``` bash
# You can change --network and --cluster-name as you like.
ENV='test' # 'staging' and 'prod' also acceptable depending on desired endpoint.
PROJECT='your-project'
NETWORK='taaa-network-test'

kubetest2 gke \
        --up \
        --skip-test-junit-report \
        --machine-type=e2-standard-4 \
        --num-nodes=2 --region=us-central1 \
        --enable-workload-identity \
        --ignore-gcp-ssh-key=true \
        -v=2 \
        --retryable-error-patterns='.*does not have enough resources available to fulfill.*,.*only \d+ nodes out of \d+ have registered; this is likely due to Nodes failing to start correctly.*,.*All cluster resources were brought up.+ but: component .+ from endpoint .+ is unhealthy.*' \
        --create-command='beta container clusters create --quiet --enable-network-policy' \
        --environment=${ENV} \
        --network=${NETWORK} \
        --project=${PROJECT} \
        --cluster-name=taaa-test1,taaa-test2
```

Then edit the [text proto](cmd/localrun/example.textpb) with the details of
your cluster(s).

Finally you can install ASM and/or run tests on your clusters with:

``` bash
go run cmd/localrun/main.go -t cmd/localrun/example.textpb
```
