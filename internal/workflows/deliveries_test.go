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
	"testing"
	"time"

	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/converter"
	"go.temporal.io/sdk/log"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/testsuite"
	"go.temporal.io/sdk/workflow"
)

// runAudit runs one DeliveryAuditWorkflow pass whose activity answers
// with res or err, and returns the ContinueAsNew input and when the
// activity ran.
func runAudit(t *testing.T, in *DeliveryAuditInput, res *DeliveryAudit, err error) (next *DeliveryAuditInput, after *ListHookDeliveriesInput) {
	t.Helper()
	var suite testsuite.WorkflowTestSuite
	suite.SetLogger(log.NewStructuredLogger(slog.New(slog.DiscardHandler)))
	env := suite.NewTestWorkflowEnvironment()
	env.RegisterWorkflowWithOptions(DeliveryAuditWorkflow, workflow.RegisterOptions{Name: DeliveryAuditWorkflowName})
	env.RegisterActivityWithOptions(func(_ context.Context, got *ListHookDeliveriesInput) (*DeliveryAudit, error) {
		after = got
		if err != nil {
			return nil, temporal.NewNonRetryableApplicationError("github down", "test", err)
		}
		return res, nil
	}, activity.RegisterOptions{Name: ListHookDeliveriesActivity})
	start := time.Date(2026, 10, 10, 0, 0, 0, 0, time.UTC)
	env.SetStartTime(start)
	env.ExecuteWorkflow(DeliveryAuditWorkflowName, in)
	werr := env.GetWorkflowError()
	if in.Every <= 0 {
		if werr != nil {
			t.Fatalf("one-shot audit: %v", werr)
		}
		return nil, after
	}
	var can *workflow.ContinueAsNewError
	if !errors.As(werr, &can) {
		t.Fatalf("workflow: %v, want ContinueAsNew", werr)
	}
	if elapsed := env.Now().Sub(start); elapsed < in.Every {
		t.Errorf("continued after %v, want a wait of Every (%v)", elapsed, in.Every)
	}
	next = &DeliveryAuditInput{}
	if err := converter.GetDefaultDataConverter().FromPayloads(can.Input, next); err != nil {
		t.Fatal(err)
	}
	return next, after
}

// A pass reads from the cursor, waits Every and continues with the
// newest delivery as the next cursor.
func TestDeliveryAuditWorkflow_AdvancesTheCursor(t *testing.T) {
	t.Parallel()
	res := &DeliveryAudit{Newest: 40, Read: 3, Pages: 1, Counts: []DeliveryCounts{{Event: "push", Failed: 1, OK: 2}}}
	next, after := runAudit(t, &DeliveryAuditInput{App: "main", Every: 15 * time.Minute, Cursor: 37}, res, nil)
	if after.App != "main" || after.After != 37 {
		t.Errorf("activity input = %+v, want main after 37", after)
	}
	if *next != (DeliveryAuditInput{App: "main", Every: 15 * time.Minute, Cursor: 40}) {
		t.Errorf("continued with %+v, want cursor 40", *next)
	}
}

// A failed pass keeps the cursor, so the next re-reads.
func TestDeliveryAuditWorkflow_FailedPassKeepsTheCursor(t *testing.T) {
	t.Parallel()
	next, _ := runAudit(t, &DeliveryAuditInput{App: "main", Every: time.Minute, Cursor: 37}, nil, errors.New("down"))
	if next.Cursor != 37 {
		t.Errorf("continued with cursor %d, want 37 kept", next.Cursor)
	}
}

// Without an interval the workflow makes one pass and ends.
func TestDeliveryAuditWorkflow_OneShot(t *testing.T) {
	t.Parallel()
	if _, after := runAudit(t, &DeliveryAuditInput{App: "main"}, &DeliveryAudit{}, nil); after == nil {
		t.Error("the one pass did not run")
	}
}
