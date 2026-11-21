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
	"testing"

	"go.temporal.io/sdk/testsuite"
	"go.temporal.io/sdk/workflow"
)

func TestStoreOptions(t *testing.T) {
	t.Parallel()
	var s testsuite.WorkflowTestSuite
	env := s.NewTestWorkflowEnvironment()
	var got workflow.ActivityOptions
	env.ExecuteWorkflow(func(ctx workflow.Context) error {
		got = workflow.GetActivityOptions(storeOptions(ctx, 7))
		return nil
	})
	if err := env.GetWorkflowError(); err != nil {
		t.Fatal(err)
	}
	if got.StartToCloseTimeout != StoreStartToClose || got.RetryPolicy == nil || got.RetryPolicy.MaximumAttempts != 0 {
		t.Errorf("storeOptions = %+v, want 10 s start-to-close and unlimited retry", got)
	}
	if got.Priority.PriorityKey != int(PriorityLow) || got.Priority.FairnessKey != "7" {
		t.Errorf("storeOptions priority = %+v, want low, fairness 7", got.Priority)
	}
}
