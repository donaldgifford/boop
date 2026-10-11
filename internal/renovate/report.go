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

package renovate

import (
	"encoding/json"
	"slices"
)

// report is the "Printing report" line's report field:
// {problems, repositories: {"<slug>": {problems, branches, packageFiles}}}.
type report struct {
	Problems     []reportProblem              `json:"problems"`
	Repositories map[string]*repositoryReport `json:"repositories"`
}

type repositoryReport struct {
	Problems     []reportProblem                `json:"problems"`
	Branches     []reportBranch                 `json:"branches"`
	PackageFiles map[string][]reportPackageFile `json:"packageFiles"`
}

type reportProblem struct {
	Level   int    `json:"level"`
	Msg     string `json:"msg"`
	Message string `json:"message"`
}

type reportBranch struct {
	BranchName string          `json:"branchName"`
	PRNo       json.RawMessage `json:"prNo"`
	Result     string          `json:"result"`
	Upgrades   []reportUpgrade `json:"upgrades"`
}

type reportUpgrade struct {
	Datasource     string `json:"datasource"`
	DepName        string `json:"depName"`
	PackageName    string `json:"packageName"`
	PackageFile    string `json:"packageFile"`
	UpdateType     string `json:"updateType"`
	CurrentVersion string `json:"currentVersion"`
	CurrentValue   string `json:"currentValue"`
	NewVersion     string `json:"newVersion"`
	NewValue       string `json:"newValue"`
}

type reportPackageFile struct {
	PackageFile string `json:"packageFile"`
}

// repository returns the slug's entry, or the only entry when the report
// names one repository under another key (a rename mid-run).
func (r *report) repository(slug string) *repositoryReport {
	if r == nil {
		return nil
	}
	if rep, ok := r.Repositories[slug]; ok && rep != nil {
		return rep
	}
	if len(r.Repositories) == 1 {
		for _, rep := range r.Repositories {
			return rep
		}
	}
	return nil
}

func (rep *repositoryReport) tuples() []UpdateTuple {
	byFile := make(map[string]string)
	for _, m := range rep.managers() {
		for _, pf := range rep.PackageFiles[m] {
			if _, seen := byFile[pf.PackageFile]; !seen {
				byFile[pf.PackageFile] = m
			}
		}
	}
	var out []UpdateTuple
	for i := range rep.Branches {
		b := &rep.Branches[i]
		pr := prNumber(b.PRNo)
		for j := range b.Upgrades {
			u := &b.Upgrades[j]
			out = append(out, UpdateTuple{
				BranchName:     b.BranchName,
				BranchResult:   b.Result,
				PRNumber:       pr,
				Manager:        byFile[u.PackageFile],
				Datasource:     u.Datasource,
				DepName:        u.DepName,
				PackageName:    u.PackageName,
				PackageFile:    u.PackageFile,
				UpdateType:     u.UpdateType,
				CurrentVersion: firstNonEmpty(u.CurrentVersion, u.CurrentValue),
				NewVersion:     firstNonEmpty(u.NewVersion, u.NewValue),
			})
		}
	}
	return out
}

// managers returns the packageFiles keys, sorted.
func (rep *repositoryReport) managers() []string {
	out := make([]string, 0, len(rep.PackageFiles))
	for m := range rep.PackageFiles {
		out = append(out, m)
	}
	slices.Sort(out)
	return out
}

func (rep *repositoryReport) problemList() []Problem {
	out := make([]Problem, 0, len(rep.Problems))
	for _, p := range rep.Problems {
		out = append(out, Problem{Level: p.Level, Message: firstNonEmpty(p.Msg, p.Message)})
	}
	return out
}
