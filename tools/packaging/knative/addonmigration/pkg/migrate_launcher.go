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

package migration

import (
	"fmt"
	"time"

	"k8s.io/client-go/kubernetes"

	"istio.io/istio/pkg/test/shell"
	"istio.io/istio/pkg/test/util/retry"
	"istio.io/pkg/log"
)

const (
	// defaultRevisionName is the revision name for regular channel
	defaultRevisionName = "asm-managed"
	regularChannel      = "regular"
	retryTimeOut        = time.Minute * 10
	retryDelay          = time.Minute * 1
)

// scriptArgs includes the args passed into the underlying migrate_addon.sh
type scriptArgs struct {
	command,
	channel string
}

type migrationWorker struct {
	kubeClient *kubernetes.Clientset
	scriptArgs scriptArgs
}

// NewMigrationWorker creates new worker for the migration job
// nolint: golint, revive
func NewMigrationWorker(kubeClient *kubernetes.Clientset, command, channel string) *migrationWorker {
	return &migrationWorker{
		kubeClient: kubeClient,
		scriptArgs: scriptArgs{
			command: command,
			channel: channel,
		},
	}
}

// ExecuteMigrationMode provisions MCP and migrates the configs.
func (m *migrationWorker) ExecuteMigrationMode() {
	log.Infof("Start addon migration")
	bt := time.Now()
	cprName := defaultRevisionName
	if m.scriptArgs.channel != regularChannel {
		cprName = fmt.Sprintf("asm-managed-%s", m.scriptArgs.channel)
	}
	template := `bash -c 'cat <<EOF | kubectl apply -f -
apiVersion: mesh.cloud.google.com/v1alpha1
kind: ControlPlaneRevision
metadata:
  name: %s
  namespace: istio-system
  labels:
    mesh.cloud.google.com/managed-cni-enabled: "false"
spec:
  type: managed_service
  channel: %s
EOF'`
	var jobstate jobState
	err := retry.UntilSuccess(func() error {
		if res, err := shell.Execute(true, fmt.Sprintf(template, cprName, m.scriptArgs.channel)); err != nil {
			jobstate = ControlplaneProvisionErrorState
			return fmt.Errorf("failed to apply ControlPlaneRevision CR, result: %s, err: %v", res, err)
		}
		waitCmd := fmt.Sprintf("kubectl wait --for=condition=ProvisioningFinished controlplanerevision %s -n istio-system --timeout=5m", cprName)
		if res, err := shell.Execute(true, waitCmd); err != nil {
			jobstate = ControlplaneProvisionErrorState
			return fmt.Errorf("failed to provision mcp, result: %s, err: %v", res, err)
		}
		// -y flag needed to be put at the beginning
		msCommand := fmt.Sprintf("bash -c 'migrate-addon -y -z  --mcp_channel %s --command %s'", m.scriptArgs.channel, m.scriptArgs.command)
		var res string
		var err error
		if res, err = shell.Execute(true, msCommand); err != nil {
			jobstate = MigrationConfigErrorState
			return fmt.Errorf("failed to migrate incluster configs, result: %s, err: %v", res, err)
		}
		log.Infof("Output of migrate-addon: %s\n", res)
		return nil
	}, retry.Timeout(retryTimeOut), retry.Delay(retryDelay))
	if err != nil {
		log.Errorf("Failed to migrate from addon to MCP: %v\n", err)
		writeConfigMapAndReportMetrics(jobstate)
	} else {
		log.Info("Successfully migrated from addon to MCP\n")
		writeConfigMapAndReportMetrics(SuccessState)
		ReportMigrationDuration(time.Since(bt).Seconds())
	}
}

// ExecuteRollbackMode rollbacks cluster to Istio addon.
func (m *migrationWorker) ExecuteRollbackMode() {
	log.Infof("Start rollback to addon")
	msCommand := "bash -c 'migrate-addon -y --command rollback_all'"
	err := retry.UntilSuccess(func() error {
		if res, err := shell.Execute(true, msCommand); err != nil {
			return fmt.Errorf("failed to run migrate-addon.sh for rollback, result: %s, err: %v", res, err)
		}
		return nil
	}, retry.Timeout(retryTimeOut), retry.Delay(retryDelay))
	if err != nil {
		log.Errorf("Failed to rollback to addon: %v\n", err)
		writeConfigMapAndReportMetrics(MigrationConfigErrorState)
	} else {
		writeConfigMapAndReportMetrics(RollbackedState)
		log.Info("Successfully rolled back to addon\n")
	}
}

// writeConfigMapAndReportMetrics is a helper function to update the in-cluster configmap and report metrics related to migration status.
func writeConfigMapAndReportMetrics(state jobState) {
	ReportMigrationState(state)
	template := `bash -c 'cat <<EOF | kubectl apply -f -
apiVersion: v1
kind: ConfigMap
metadata:
  name: asm-addon-migration-state
  namespace: istio-system
data:
  migrationStatus: %s
EOF'`
	if res, err := shell.Execute(true, fmt.Sprintf(template, string(state))); err != nil {
		log.Errorf("Failed to update migration state configmap, result: %s, err: %v", res, err)
	}
}