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

package config

import "github.com/hashicorp/hcl/v2"

// The structs below mirror the file block for block; gohcl decodes into
// them and rejects unknown attributes and blocks. Values that need a
// positioned diagnostic after decode carry their range (attr_value_range,
// def_range), and durations, enums and lists checked per element stay
// expressions until build evaluates them.

type fileSchema struct {
	SecretsDir      *string            `hcl:"secrets_dir,optional"`
	SecretsDirRange hcl.Range          `hcl:"secrets_dir,attr_value_range"`
	Renovate        renovateBlock      `hcl:"renovate,block"`
	Runs            runsBlock          `hcl:"runs,block"`
	Profiles        []profileBlock     `hcl:"profile,block"`
	Order           hcl.Expression     `hcl:"order"`
	DefaultProfile  string             `hcl:"default_profile"`
	UnknownProfile  string             `hcl:"unknown_profile"`
	ProfileRules    []profileRuleBlock `hcl:"profile_rule,block"`
	Apps            []appBlock         `hcl:"app,block"`
}

type renovateBlock struct {
	Image             string          `hcl:"image"`
	ImageRange        hcl.Range       `hcl:"image,attr_value_range"`
	ConfigPath        *string         `hcl:"config_path,optional"`
	ConfigPathRange   hcl.Range       `hcl:"config_path,attr_value_range"`
	SharedPreset      *string         `hcl:"shared_preset,optional"`
	SharedPresetRange hcl.Range       `hcl:"shared_preset,attr_value_range"`
	LogLevel          hcl.Expression  `hcl:"log_level,optional"`
	DryRun            hcl.Expression  `hcl:"dry_run,optional"`
	GitAuthor         *string         `hcl:"git_author,optional"`
	Global            *freeBlock      `hcl:"global,block"`
	RedisSecretRef    *secretRefBlock `hcl:"redis_secret_ref,block"`
	DefRange          hcl.Range       `hcl:",def_range"`
}

// freeBlock is a block of Renovate options: any attribute names, values
// converted to JSON. Nested Renovate config is written as object or
// tuple expressions, not blocks.
type freeBlock struct {
	Body     hcl.Body  `hcl:",remain"`
	DefRange hcl.Range `hcl:",def_range"`
}

type secretRefBlock struct {
	Name      string    `hcl:"name"`
	NameRange hcl.Range `hcl:"name,attr_value_range"`
	Key       string    `hcl:"key"`
	KeyRange  hcl.Range `hcl:"key,attr_value_range"`
	DefRange  hcl.Range `hcl:",def_range"`
}

type runsBlock struct {
	Namespace          string         `hcl:"namespace"`
	NamespaceRange     hcl.Range      `hcl:"namespace,attr_value_range"`
	PendingTimeout     hcl.Expression `hcl:"pending_timeout,optional"`
	MaxConcurrent      *int           `hcl:"max_concurrent,optional"`
	MaxConcurrentRange hcl.Range      `hcl:"max_concurrent,attr_value_range"`
	DefRange           hcl.Range      `hcl:",def_range"`
}

type profileBlock struct {
	Name         string     `hcl:"name,label"`
	Inherit      *string    `hcl:"inherit,optional"`
	InheritRange hcl.Range  `hcl:"inherit,attr_value_range"`
	Pod          *podBlock  `hcl:"pod,block"`
	Renovate     *freeBlock `hcl:"renovate,block"`
	DefRange     hcl.Range  `hcl:",def_range"`
}

type podBlock struct {
	RuntimeClassName   *string           `hcl:"runtime_class_name,optional"`
	Labels             map[string]string `hcl:"labels,optional"`
	Annotations        map[string]string `hcl:"annotations,optional"`
	NodeSelector       map[string]string `hcl:"node_selector,optional"`
	TerminationGrace   *int64            `hcl:"termination_grace_period_seconds,optional"`
	TerminationRange   hcl.Range         `hcl:"termination_grace_period_seconds,attr_value_range"`
	WorkSizeLimit      *string           `hcl:"work_size_limit,optional"`
	WorkSizeLimitRange hcl.Range         `hcl:"work_size_limit,attr_value_range"`
	Resources          *resourcesBlock   `hcl:"resources,block"`
	Tolerations        []tolerationBlock `hcl:"toleration,block"`
}

type resourcesBlock struct {
	Requests      map[string]string `hcl:"requests,optional"`
	RequestsRange hcl.Range         `hcl:"requests,attr_value_range"`
	Limits        map[string]string `hcl:"limits,optional"`
	LimitsRange   hcl.Range         `hcl:"limits,attr_value_range"`
}

type tolerationBlock struct {
	Key               *string `hcl:"key,optional"`
	Operator          *string `hcl:"operator,optional"`
	Value             *string `hcl:"value,optional"`
	Effect            *string `hcl:"effect,optional"`
	TolerationSeconds *int64  `hcl:"toleration_seconds,optional"`
}

type profileRuleBlock struct {
	Extends  []string  `hcl:"extends,optional"`
	Managers []string  `hcl:"managers,optional"`
	Profile  string    `hcl:"profile"`
	DefRange hcl.Range `hcl:",def_range"`
}

type appBlock struct {
	Name                string          `hcl:"name,label"`
	Endpoint            *string         `hcl:"endpoint,optional"`
	EndpointRange       hcl.Range       `hcl:"endpoint,attr_value_range"`
	AppID               int64           `hcl:"app_id"`
	AppIDRange          hcl.Range       `hcl:"app_id,attr_value_range"`
	PrivateKeySecretRef secretRefBlock  `hcl:"private_key_secret_ref,block"`
	Installations       hcl.Expression  `hcl:"installations,optional"`
	Cadence             hcl.Expression  `hcl:"cadence,optional"`
	Discovery           *discoveryBlock `hcl:"discovery,block"`
	Budget              *budgetBlock    `hcl:"budget,block"`
	DefRange            hcl.Range       `hcl:",def_range"`
}

type discoveryBlock struct {
	Every        hcl.Expression `hcl:"every,optional"`
	Probe        hcl.Expression `hcl:"probe,optional"`
	SkipForks    *bool          `hcl:"skip_forks,optional"`
	SkipArchived *bool          `hcl:"skip_archived,optional"`
}

type budgetBlock struct {
	ReserveFraction        *float64       `hcl:"reserve_fraction,optional"`
	ReserveFractionRange   hcl.Range      `hcl:"reserve_fraction,attr_value_range"`
	MaxConcurrentRuns      *int           `hcl:"max_concurrent_runs,optional"`
	MaxConcurrentRunsRange hcl.Range      `hcl:"max_concurrent_runs,attr_value_range"`
	DefaultEstimate        map[string]int `hcl:"default_estimate,optional"`
	DefaultEstimateRange   hcl.Range      `hcl:"default_estimate,attr_value_range"`
	DefRange               hcl.Range      `hcl:",def_range"`
}
