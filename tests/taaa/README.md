# Tests as an Artifact

See go/taaa for an overview of the project.

Each subdirectory here is a Go module that defines a TaaA docker image.

## Arbitrary Notes

*  The magefile build `out` subdirectories generated during build is in .gitignore

## Local testing

Each image has a cmd/entrypoint subdirectory with code to run the TaaA images via your local machine.
A `test-artifact` execution will of course require you first run the `install` TaaA image or have installed ASM some other way.
You can manually build the `localrun` executables within the root of the module directories via something like `go build -o localrun ./cmd/localrun/` and then run them with `./localrun -t cmd/localrun/example.textpb`.