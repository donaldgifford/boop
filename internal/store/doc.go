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

// Package store is boopd's Postgres product store (DESIGN-0004, ADR-0006):
// an append-only event log with projections applied in the same
// transaction, and the migrations that create both.
//
// Migrate applies the embedded schema as the owner role. Every other role
// calls RequireVersion at start and refuses to run below SchemaVersion.
//
// Writers record what happened with Store.Append. Each Event carries a
// Kind from the catalogue (Kinds) and that kind's typed, versioned Data.
// Append records the idempotency key first, so a retried event is a
// no-op, then inserts the event and runs the kind's apply on the
// projections, all in one transaction. Every apply compares the source
// timestamp with the row's guard column and ignores older data, so events
// arriving out of order converge on the same rows.
//
// Rebuild truncates the projections and replays the log through the same
// applies; it equals the incremental result. Two reads serve the worker:
// ReceivedDeliveries for the delivery audit's missed classification, and
// OpenPullRequests for the PR reconcile. Maintain applies one retention
// rule (MaintenanceRules) for the daily StoreMaintenanceWorkflow. The
// queries live in queries/ and are generated into the db package by sqlc.
package store
