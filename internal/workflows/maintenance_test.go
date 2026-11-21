/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package workflows

import (
	"context"
	"errors"
	"log/slog"
	"slices"
	"testing"
	"time"

	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/log"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/testsuite"
	"go.temporal.io/sdk/workflow"
)

// Every rule runs in order as of the workflow's clock; a failed rule is
// counted and the rest still run.
func TestStoreMaintenanceWorkflow(t *testing.T) {
	t.Parallel()
	var suite testsuite.WorkflowTestSuite
	suite.SetLogger(log.NewStructuredLogger(slog.New(slog.DiscardHandler)))
	env := suite.NewTestWorkflowEnvironment()
	env.RegisterWorkflowWithOptions(StoreMaintenanceWorkflow, workflow.RegisterOptions{Name: StoreMaintenanceWorkflowName})
	start := time.Date(2027, 1, 15, 3, 0, 0, 0, time.UTC)
	env.SetStartTime(start)
	var ran []string
	env.RegisterActivityWithOptions(func(_ context.Context, in *MaintainStoreInput) (*MaintainStoreResult, error) {
		ran = append(ran, in.Rule)
		if !in.Now.Equal(start) {
			return nil, temporal.NewNonRetryableApplicationError("wrong clock", "test", nil)
		}
		if in.Rule == "offboarded" {
			return nil, temporal.NewNonRetryableApplicationError("down", "test", errors.New("down"))
		}
		return &MaintainStoreResult{Rule: in.Rule, Removed: 1}, nil
	}, activity.RegisterOptions{Name: MaintainStoreActivity})
	env.ExecuteWorkflow(StoreMaintenanceWorkflowName)
	var res StoreMaintenanceResult
	if err := env.GetWorkflowResult(&res); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(ran, MaintenanceRules) {
		t.Errorf("ran %v, want %v in order", ran, MaintenanceRules)
	}
	if res.Failed != 1 || len(res.Rules) != 3 {
		t.Errorf("result = %+v, want 3 rules and 1 failure", res)
	}
}
