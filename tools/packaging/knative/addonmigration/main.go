// Copyright Istio Authors
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package main

import (
	"context"
	"flag"
	"fmt"

	"go.opencensus.io/stats/view"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"

	"istio.io/istio/pkg/bootstrap/platform"
	migration "istio.io/istio/tools/packaging/knative/addonmigration/pkg"
	"istio.io/pkg/log"
)

var (
	projectNumber       string
	clusterName         string
	location            string
	mcpEnvConfigMapName = "env-asm-managed"
)

const (
	runallFlag     = "run_all"
	rapidChannel   = "rapid"
	regularChannel = "regular"
	stableChannel  = "stable"
	istioSystemNS  = "istio-system"
	// these consts need to align with migrate-addon.sh
	migrationConfigMapName = "asm-addon-migration-state"
	migrationStatusField   = "migrationStatus"
	migrationSuccessStatus = "SUCCESS"
	// used in previous migration guide
	migrationCompleteStatus = "COMPLETE"
	migrateMode             = "migrate"
	rollbackMode            = "rollback"
)

func main() {
	// command is the --command flag passed in to the underlying migrate-addon.sh
	command := flag.String("command", runallFlag, "command to be run for migrate_addon.sh, support value: run_all, rollback_all")
	// channel is the channel value for MCP
	channel := flag.String("mcp_channel", "", "channel for MCP, support value: rapid, regular, stable")
	// mode is the execution mode, can be either migrate or rollback
	mode := flag.String("mode", migrateMode, "execution mode of the migration script, support value: migrate, rollback")
	flag.Parse()
	if err := validateFlag(*channel, *mode); err != nil {
		log.Fatalf("Failed to verify flag: %v", err)
	}
	if *channel != regularChannel {
		mcpEnvConfigMapName = fmt.Sprintf("env-asm-managed-%s", *channel)
	}
	loggingOptions := log.DefaultOptions()
	if err := log.Configure(loggingOptions); err != nil {
		log.Errorf("Unable to configure logging: %v", err)
	}
	clientSet, err := migration.NewClientSet("", "")
	if err != nil {
		log.Fatalf("Failed to create kube client: %v", err)
	}
	if checkMigrationComplete(clientSet) {
		log.Info("Skip because migration is already complete in the cluster")
	} else {
		setProjectMetadata()
		mw := migration.NewMigrationWorker(clientSet, *command, *channel)
		exporter, err := mw.InitializeMonitoring(clusterName, location)
		if err != nil {
			log.Fatalf("Failed to setup monitoring: %v", err)
		}
		view.RegisterExporter(exporter)
		migration.ReportMigrationState(migration.PendingState)
		if *mode == migrateMode {
			mw.ExecuteMigrationMode()
		} else {
			mw.ExecuteRollbackMode()
		}
	}
}

func validateFlag(channel, mode string) error {
	switch channel {
	case rapidChannel, regularChannel, stableChannel:
		break
	default:
		return fmt.Errorf("invalid mcp channel: %s", channel)
	}
	switch mode {
	case migrateMode, rollbackMode:
		return nil
	default:
		return fmt.Errorf("invalid execution mode: %s", mode)
	}
}

// setProjectMetadata set target cluster metadata.
// projectNumber, clusterName and location are required since they are used to check against target cluster list.
func setProjectMetadata() {
	gcpMetadata := platform.NewGCP().Metadata()
	if pi, ok := gcpMetadata[platform.GCPProjectNumber]; ok {
		projectNumber = pi
	} else {
		log.Fatalf("No project number found from gcp metadata")
	}
	if cn, ok := gcpMetadata[platform.GCPCluster]; ok {
		clusterName = cn
	} else {
		log.Fatalf("No cluster name found from gcp metadata")
	}
	if lc, ok := gcpMetadata[platform.GCPLocation]; ok {
		location = lc
	} else {
		log.Fatalf("No location found from gcp metadata")
	}
}

// checkMigrationComplete check whether migration is complete by verifying the configMap status.
// it checks the migrationStatus of the asm-addon-migration-state configMap first, if it is success then return true
// then it checks the existence of CLOUDRUN_ADDR field of the env-asm-managed configMap
func checkMigrationComplete(clientSet *kubernetes.Clientset) bool {
	ctx := context.Background()
	if cm, err := clientSet.CoreV1().ConfigMaps(istioSystemNS).
		Get(ctx, migrationConfigMapName, v1.GetOptions{}); err == nil {
		if cm.Data != nil {
			if val, ok := cm.Data[migrationStatusField]; ok && (val == migrationSuccessStatus || val == migrationCompleteStatus) {
				log.Infof("Found existing migration state configMap with success state")
				return true
			}
		}
	}
	return false
}
