package main

// Copyright Google
// not licensed under Apache License, Version 2

import (
	_ "embed"
	"errors"
	"fmt"
	"log"
	"os/exec"
	"strconv"
	"sync"

	"github.com/golang/protobuf/proto"
	"github.com/magefile/mage/sh"
	"github.com/spf13/cobra"
	"gke-internal.git.corp.google.com/taaa/lib.git/pkg/entrypoint"
	"gke-internal.git.corp.google.com/taaa/lib.git/pkg/magetools"
	asmpb "gke-internal.git.corp.google.com/taaa/protobufs.git/asm_installer"
)

const (
	// The file name of the helper script to download scriptaro
	getScriptaroScript = "get_scriptaro.sh"
)

var (
	// Only one thread must be installing scriptaro.
	getScriptaroMu sync.Mutex
)

func readProto(asmProto *asmpb.ASMInstaller) {
	if err := entrypoint.ReadProto(asmProto); err != nil {
		log.Fatal(err)
	}
}

// getScriptaro installs scriptaro for a particular ASM version if not available.
func getScriptaro(c *asmpb.ASMCluster) (string, error) {
	getScriptaroMu.Lock()
	defer getScriptaroMu.Unlock()
	major := strconv.Itoa(int(c.GetMajor()))
	minor := strconv.Itoa(int(c.GetMinor()))
	scriptaroCmd := fmt.Sprintf("install_asm_%s.%s", major, minor)
	if _, err := exec.LookPath(scriptaroCmd); err == nil {
		// Already have this version installed exit out.
		log.Printf("Already have desired scriptaro version %s.%s\n", major, minor)
		return scriptaroCmd, nil
	}
	log.Printf("Installing ASM installer for ASM version %s.%s\n", major, minor)
	_, _, err := magetools.BothOutputLogWith(nil, getScriptaroScript, major, minor)
	return scriptaroCmd, err
}

func main() {
	entrypoint.RunCmd.RunE = func(cobraCmd *cobra.Command, args []string) error {
		var asmProto asmpb.ASMInstaller
		readProto(&asmProto)
		wg := sync.WaitGroup{}
		errExist := false
		for i, c := range asmProto.GetClusters() {
			i := i
			c := c
			wg.Add(1)
			go func() {
				defer wg.Done()
				scriptaroCmd, err := getScriptaro(c)
				if err != nil {
					errExist = true
					return
				}
				err = sh.RunV("asmclusterinstall", "run",
					"--output-directory", entrypoint.OutputDirectory,
					"--proto", entrypoint.ProtoFile,
					"--index", strconv.Itoa(i),
					"--scriptaro", scriptaroCmd)
				if err != nil {
					errExist = true
				}
			}()
		}
		wg.Wait()
		if errExist {
			return errors.New("ASM installation failed on 1 or more clusters")
		}
		log.Print("ASM Install finished.")
		return nil
	}
	entrypoint.DumpProtoCmd.Run = func(cmd *cobra.Command, args []string) {
		log.Println("Reading and dumping provided proto.")
		var asmProto asmpb.ASMInstaller
		readProto(&asmProto)
		log.Println(proto.MarshalTextString(&asmProto))
	}
	entrypoint.Execute()
}
