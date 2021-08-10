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
	"istio.io/pkg/monitoring"
)

type jobState string

const (
	ControlplaneProvisionErrorState jobState = "CONTROLPLANE_PROVISION_ERROR"
	MigrationConfigErrorState       jobState = "MIGRATION_CONFIG_ERROR"
	SuccessState                    jobState = "SUCCESS"
	RollbackedState                 jobState = "ROLLBACKED"
	// PendingState means the addon cluster is pending for selection and migration.
	PendingState jobState = "PENDING"
	sec          float64  = 1000
)

var (
	migrationDuration = monitoring.NewDistribution(
		"migration_duration",
		"Migration job execution duration",
		[]float64{
			// most of the job should complete within 10 mins
			60 * sec, 180 * sec, 300 * sec, 420 * sec, 600 * sec, 900 * sec,
		},
	)
	migrationState = monitoring.NewGauge(
		"migration_state",
		"Migration job running state",
		monitoring.WithInt64Values(),
		monitoring.WithLabels(stateLabel),
	)
)

// ReportMigrationDuration reports migration duration metric
func ReportMigrationDuration(ut float64) {
	migrationDuration.RecordInt(int64(ut))
}

// ReportMigrationState reports migration state metric
func ReportMigrationState(state jobState) {
	for _, s := range []jobState{
		ControlplaneProvisionErrorState, PendingState, MigrationConfigErrorState, SuccessState, RollbackedState,
	} {
		v := 0
		if s == state {
			v = 1
		}
		migrationState.
			With(stateLabel.Value(string(s))).
			RecordInt(int64(v))
	}
}

func init() {
	monitoring.MustRegister(migrationDuration)
	monitoring.MustRegister(migrationState)
}
