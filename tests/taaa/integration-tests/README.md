# Test Artifact

The image here compiles tests and stores them in an OCI image. We use them
to get test results into GKE Networking Guitar dataplanev2 TestGrid.

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
