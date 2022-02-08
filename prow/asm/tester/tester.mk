# Copyright Istio Authors
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
#    http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.

TESTER_PATH := prow/asm/tester
LINT_SCRIPT_PATH := ./cmd/lint/lint.go
SKIP_CONFIG_PATH := ./configs/tests/skip.yaml

.PHONY: lint-skip-config
lint-skip-config:
	cd $(TESTER_PATH) && go run $(LINT_SCRIPT_PATH) $(SKIP_CONFIG_PATH)

.PHONY: tester-unit-tests
tester-unit-tests:
	cd $(TESTER_PATH) && go test -v -race ./... 2>&1
