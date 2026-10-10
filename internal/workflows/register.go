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
	"go.temporal.io/sdk/worker"
	"go.temporal.io/sdk/workflow"
)

// registration is one workflow function under its registered name.
type registration struct {
	fn   any
	name string
}

// all is every boopd workflow, on the one task queue.
var all = []registration{
	{InstallationWorkflow, InstallationWorkflowName},
	{RepoWorkflow, RepoWorkflowName},
	{DiscoveryWorkflow, DiscoveryWorkflowName},
}

// Register registers the worker's workflows.
func Register(r worker.WorkflowRegistry) {
	for _, reg := range all {
		r.RegisterWorkflowWithOptions(reg.fn, workflow.RegisterOptions{Name: reg.name})
	}
}
