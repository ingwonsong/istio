//go:build mage
// +build mage

package main

// Copyright Google
// not licensed under Apache License, Version 2

import (
	"bufio"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strings"
	"sync"

	"github.com/magefile/mage/mg"
	"github.com/magefile/mage/sh"
	"gke-internal.git.corp.google.com/taaa/lib.git/pkg/git"
	"gke-internal.git.corp.google.com/taaa/lib.git/pkg/magetools"
	"gke-internal.git.corp.google.com/taaa/lib.git/pkg/registry"
	"istio.io/istio/tests/taaa/test-artifact/internal"
	"knative.dev/test-infra/rundk/interactive"
)

const (
	compilerImgPath = "gcr.io/istio-testing/build-tools:master-latest"
	repoRoot        = "../../.."
	outPath         = "./out"
	outBinPath      = outPath + "/usr/bin"
	repoRootOutPath = outPath + internal.RepoCopyRoot
	// The directory inside the compiler image that is bound to the outPath.
	// The compiler image will output it's binaries here.
	internalOutDir = "/tmp/artifacts/"
)

func init() {
	// Use docker buildkit.
	os.Setenv("DOCKER_BUILDKIT", "1")
}

type Build mg.Namespace

// Build the entire Test Artifact and dependencies
func (Build) Artifact() error {
	mg.Deps(
		Build.TestImages,
		Build.Binaries)
	mg.Deps(Build.ArtifactNoDeps)
	return nil
}

// TestImages build takes a bloody long time, so bypass the dependencies.
// If this is your first time, please run Artifact at least once.
// Then only run build:Entrypoint/Tests/TestImages depending on what you changed.
func (Build) ArtifactNoDeps() error {
	imgTag, err := git.DescribeCWD()
	if err != nil {
		return err
	}
	err = sh.RunV("docker", "build",
		"--pull",
		"-t", internal.ImgPath+":"+imgTag,
		"-f", "Dockerfile",
		outPath)
	if err != nil {
		return err
	}
	return sh.RunV("docker", "tag", internal.ImgPath+":"+imgTag, internal.ImgPath+":latest")
}

// Docker push the image to its destination.
// Should only be run by CI system. Permissions should stop mortals from pushing.
// Pushes both the generated image path and one with a tag matching the argument to this function.
// If a series of commits merge in a short period of time, there's no guarantee the final status of the :branchName tag matches the very last commit.
// Due to rebasing in ASM we can't check for ancestor to confirm whether we should overwrite.
// This shouldn't be a serious problem.
func (Build) Push(branchName string) error {
	mg.Deps(Build.Artifact)
	mg.Deps(mg.F(Build.PushNoDeps, branchName))
	return nil
}

// Docker push the image to its destination.
// Useful when iterating on image locally before pushing to remote to test on guitar.
// Should not be used to push with `master-asm` tag, only for dev branch tags.
func (Build) PushNoDeps(branchName string) error {
	imgTag, err := git.DescribeCWD()
	if err != nil {
		return err
	}
	err = sh.RunV("docker", "tag", internal.ImgPath+":"+imgTag, internal.ImgPath+":"+branchName)
	if err != nil {
		return err
	}
	err = sh.RunV("docker", "push", internal.ImgPath+":"+imgTag)
	if err != nil {
		return err
	}
	return sh.RunV("docker", "push", internal.ImgPath+":"+branchName)
}

// Binaries builds all the non image based binaries and their dependencies.
func (Build) Binaries() error {
	mg.Deps(
		Build.Entrypoint,
		Build.Tests,
		Build.Tester)
	return nil
}

// Entrypoint compiles the entrypoint binary.
func (Build) Entrypoint() error {
	// The entrypoint doesn't appear to link in any C libraries so it shouldn't
	// necessary to use the compiler image to build it.
	// If it does in the future it will require some extra work to get the compiler
	// image to work with the gerrit permissions needed.
	log.Println("Copying docker config used for image repo authentication.")
	dockerConfigPath := outPath + "/root/.docker/"
	if err := os.MkdirAll(dockerConfigPath, 0775); err != nil {
		return err
	}
	magetools.CopyFile("cmd/entrypoint/docker-config.json", dockerConfigPath+"config.json")
	if err := os.MkdirAll(outBinPath, 0775); err != nil {
		return err
	}
	log.Println("Compiling TaaA entrypoint binary.")
	return sh.Run("go", "build", "-o", outBinPath+"/entrypoint", "./cmd/entrypoint")
}

// Tester compiler the "Tester" application in the istio repo used to install and run tests.
func (Build) Tester() error {
	mg.Deps(Build.compilerImage, Build.TestSupplements)
	log.Println("Compiling ASM tester binary.")
	return compileGo(false, "asm_tester", "prow/asm/tester")
}

// Tests compilers the ASM integration go test binaries.
func (Build) Tests() error {
	mg.Deps(Build.compilerImage, Build.TestSupplements)
	// Now use that image to build all the tests.
	// We need to use an image to build the test to keep a consistent
	// environment for the binary otherwise errors occur.
	var wg sync.WaitGroup
	var anyErr error
	for _, intTest := range internal.Tests {
		intTest := intTest
		wg.Add(1)
		go func() {
			defer wg.Done()
			name := fmt.Sprintf("%s.test", strings.ReplaceAll(intTest, "/", "_"))
			log.Printf("Compiling %s", name)
			if err := compileGo(true, name, "tests/integration/"+intTest, "--tags=integ"); err != nil {
				anyErr = err
			}
		}()
	}
	wg.Wait()
	return anyErr
}

// TestSupplements copies over the non binary files buried in the source code
// that are needed at run time.
func (Build) TestSupplements() error {
	log.Println("Copying supplementary test files.")
	if err := os.MkdirAll(repoRootOutPath, os.ModePerm); err != nil {
		return err
	}
	rsyncFlags := []string{
		"-avr",
		"--delete",
	}
	rsyncExcludes := make([]string, len(internal.SupplementFilters))
	for i, filter := range internal.SupplementFilters {
		rsyncExcludes[i] = "--exclude=" + filter
	}
	rsyncArgs := append(rsyncFlags, rsyncExcludes...)
	for _, supplement := range internal.TestSupplementDirs {
		source := repoRoot + "/" + supplement
		destination := repoRootOutPath + "/" + supplement
		os.MkdirAll(path.Dir(destination), 0755)
		if err := sh.Run("rsync", append(rsyncArgs, source, destination)...); err != nil {
			return err
		}
	}
	return nil
}

// TestImages builds the images needed during test execution.
func (Build) TestImages() error {
	// Make output directories.
	if err := os.MkdirAll("out"+internal.RegistryDestinationDirectory, 0775); err != nil {
		return err
	}

	dir, err := ioutil.TempDir("/tmp", "taaa-*")
	if err != nil {
		return fmt.Errorf("cannot create temp directory %q", dir)
	}

	// Launch a local registry to hold test images.
	log.Println("Creaing local registry for test images.")
	dsr, err := registry.Create(dir)
	if err != nil {
		return fmt.Errorf("failed to start the docker registry image")
	}

	// Define a tag for the images to use.
	imageTag, err := git.DescribeCWD()
	if err != nil {
		return err
	}

	// Store said tag so the artifact knows which to use.
	f, err := os.Create("out" + internal.ImageTagFile)
	if err != nil {
		return err
	}
	_, err = f.WriteString(imageTag)
	if err != nil {
		return err
	}
	f.Close()

	// Actually build those images.
	log.Println("Building test images.")
	buildimages := exec.Command(
		"make",
		"dockerx.pushx",
		"HUB="+dsr.URL,
		"TAG="+imageTag,
		"DOCKER_TARGETS=docker.pilot docker.proxyv2 docker.app docker.install-cni docker.mdp",
	)
	buildimages.Dir = repoRoot
	// Note, cannot be = os.Stdout() because of a check in common/run.sh for whether FD is a terminal;
	// docker run will fail with "the input device is not a TTY" if you do.
	buildimages.Stdout = logWriter(os.Stdout)
	buildimages.Stderr = logWriter(os.Stderr)
	err = buildimages.Run()
	if err != nil {
		return err
	}

	// Done building and pushing images. Archive it.
	log.Println("Shutting down local registry server and archiving images.")
	dsr.Shutdown()
	outputDir, err := registry.Archive(dir)
	if err != nil {
		return fmt.Errorf("cannot archive %q: %v", dir, err)
	}
	outRegistryDir := "out" + internal.RegistryDestinationDirectory
	configYml := outRegistryDir + "/config.yml"
	err = sh.Run("rsync", outputDir+"/config.yml", configYml)
	if err != nil {
		return err
	}
	err = sh.Run("rsync", outputDir+"/registry", outRegistryDir+"/registry")
	if err != nil {
		return err
	}
	varLibRegistryDir := outPath + "/var/lib/registry"
	if err := os.MkdirAll(varLibRegistryDir, 0775); err != nil {
		return err
	}
	err = sh.Run("rsync", "--delete", "-r", outputDir+"/varlibregistry/", varLibRegistryDir) // Trailing / is significant.
	if err != nil {
		return err
	}

	log.Println("Copying istioctl.")
	if err := magetools.CopyFile(repoRoot+"/out/linux_amd64/istioctl", outBinPath+"/istioctl"); err != nil {
		return err
	}
	log.Println("Copying all other binaries to same position in the code repo copy.")
	if err := sh.Run("rsync", "-r", "--delete", repoRoot+"/out/", repoRootOutPath+"/out"); err != nil {
		return err
	}

	// Copying this file doesn't update its timestamp, so do this so we could only rebuild images when necessary.
	return sh.Run("touch", configYml)
}

// Clean removes the whole out directory.
func Clean() error {
	return sh.Run("rm", "-fR", "out")
}

func logWriter(out io.Writer) io.WriteCloser {
	l := log.New(out, "", log.LstdFlags)
	r, w := io.Pipe()
	go func() {
		scanner := bufio.NewScanner(r)
		for scanner.Scan() {
			l.Print(scanner.Text())
		}
	}()
	return w
}

// Prerequisites targets that rely on the compiler image.
func (Build) compilerImage() error {
	log.Println("Building compiler image.")
	if err := os.MkdirAll(outBinPath, 0775); err != nil {
		return err
	}
	return sh.RunV("docker", "pull", compilerImgPath)
}

// Compiles a test package using a container with the same base OS as the
// TaaA integration test image.
// It is necessary to build on the same OS since otherwise descrepencies
// between the libraries of the image and the host machine cause errors.
func compileGo(isTest bool, outputBinaryName string, pathToPackage string, otherGoTestArgs ...string) error {
	cmd := getDockerCmd(pathToPackage)
	cmd.AddArgs("go")
	if isTest {
		cmd.AddArgs("test",
			"-c")
	} else {
		cmd.AddArgs("build")
	}
	cmd.AddArgs("-o", path.Join(internalOutDir, outputBinaryName))
	cmd.AddArgs(otherGoTestArgs...)
	log.Println("Running compiler image: ", compilerImgPath)
	return cmd.Run()
}

// runCompile Runs the compiler image with minimal set up at the specified directory in the repo.
func runCompile(pathToPackage string, command ...string) error {
	cmd := getDockerCmd(pathToPackage)
	cmd.AddArgs(command...)
	log.Println("Running compiler image: ", compilerImgPath)
	return cmd.Run()
}

// getDockerCmd returns an Docker command preset to run the compiler image.
// It binds in the current
func getDockerCmd(relativeWorkingDir string) *interactive.Docker {
	cmd := &interactive.Docker{
		Command: interactive.NewCommand("docker", "run", "--rm"),
	}
	const internalRepoDir = "/usr/lib/go/src/gke-internal/istio/istio"
	absRepoRoot, _ := filepath.Abs(repoRoot)

	homedir, _ := os.UserHomeDir()
	cmd.AddMount("bind", path.Join(homedir, ".gitconfig"), "/root/.gitconfig")
	cmd.AddMount("bind", absRepoRoot, internalRepoDir)
	absOutBinPath, _ := filepath.Abs(outBinPath)
	cmd.AddMount("bind", absOutBinPath, internalOutDir)
	cmd.AddArgs("-w", path.Join(internalRepoDir, relativeWorkingDir))

	cmd.AddArgs(compilerImgPath)
	return cmd
}
