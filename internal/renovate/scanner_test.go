package renovate_test

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/donaldgifford/boop/internal/renovate"
)

const slug = "boop-bot/scratch"

func scan(t *testing.T, name string) *renovate.Result {
	t.Helper()
	f, err := os.Open(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	s := renovate.NewScanner(slug)
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64<<10), 4<<20)
	for sc.Scan() {
		s.Feed(sc.Text())
	}
	if err := sc.Err(); err != nil {
		t.Fatal(err)
	}
	return s.Result()
}

// upgradesInReport counts branches[].upgrades[] in the fixture's report
// line, independently of the parser.
func upgradesInReport(t *testing.T, name string) int {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, raw := range splitLines(b) {
		var l struct {
			Msg    string `json:"msg"`
			Report struct {
				Repositories map[string]struct {
					Branches []struct {
						Upgrades []json.RawMessage `json:"upgrades"`
					} `json:"branches"`
				} `json:"repositories"`
			} `json:"report"`
		}
		if json.Unmarshal(raw, &l) != nil || l.Msg != "Printing report" {
			continue
		}
		for _, r := range l.Report.Repositories {
			for _, br := range r.Branches {
				n += len(br.Upgrades)
			}
		}
	}
	return n
}

func splitLines(b []byte) [][]byte {
	var out [][]byte
	start := 0
	for i, c := range b {
		if c == '\n' {
			out = append(out, b[start:i])
			start = i + 1
		}
	}
	return out
}

func TestScanner_Live(t *testing.T) {
	t.Parallel()
	got := scan(t, "synthetic-live.log")
	if got.ReportMissing {
		t.Fatal("Result().ReportMissing = true, want the report parsed")
	}
	if want := upgradesInReport(t, "synthetic-live.log"); len(got.Tuples) != want || want == 0 {
		t.Fatalf("Result() tuples = %d, want %d (branches[].upgrades[] total)", len(got.Tuples), want)
	}
	for _, tu := range got.Tuples {
		if tu.Manager == "" {
			t.Errorf("tuple %+v has no Manager", tu)
		}
	}
	if got.Progress != (renovate.Progress{BranchesChanged: 3, PRsChanged: 3}) {
		t.Errorf("Progress = %+v, want 3 branches, 3 PRs (another repository's line ignored)", got.Progress)
	}
	if got.Finished == nil || *got.Finished != (renovate.Finished{Result: "done", Status: "activated", DurationMs: 48211, Cloned: true}) {
		t.Errorf("Finished = %+v, want done/activated/48211/cloned", got.Finished)
	}
	if got.RenovateVersion != "44.132.5" {
		t.Errorf("RenovateVersion = %q, want 44.132.5", got.RenovateVersion)
	}
	if want := []string{"github-actions", "npm", "pip_requirements"}; !slices.Equal(got.Managers, want) {
		t.Errorf("Managers = %v, want %v", got.Managers, want)
	}
	if len(got.Problems) != 1 || got.Problems[0] != (renovate.Problem{Level: 40, Message: "Found renovate config warnings"}) {
		t.Errorf("Problems = %+v, want the one warning", got.Problems)
	}
	req := got.Tuples[1]
	want := renovate.UpdateTuple{
		BranchName: "renovate/requests-2.x", BranchResult: "pr-created", PRNumber: 13, Manager: "pip_requirements",
		Datasource: "pypi", DepName: "requests", PackageName: "requests", PackageFile: "requirements.txt",
		UpdateType: "minor", CurrentVersion: "2.31.0", NewVersion: "2.32.3",
	}
	if req != want {
		t.Errorf("tuple 1 = %+v, want %+v", req, want)
	}
	// currentVersion missing falls back to currentValue.
	if co := got.Tuples[3]; co.CurrentVersion != "v3" || co.NewVersion != "v4" || co.PRNumber != 0 {
		t.Errorf("tuple 3 = %+v, want v3 -> v4 from the values, no PR", co)
	}
}

func TestScanner_DryRun(t *testing.T) {
	t.Parallel()
	got := scan(t, "synthetic-dryrun.log")
	if got.Progress != (renovate.Progress{BranchesChanged: 2, PRsChanged: 2}) {
		t.Errorf("Progress = %+v, want 2 branches, 2 PRs from DRY-RUN lines", got.Progress)
	}
	if got.ReportMissing || len(got.Tuples) != upgradesInReport(t, "synthetic-dryrun.log") {
		t.Errorf("tuples = %d, missing %v; want the report's", len(got.Tuples), got.ReportMissing)
	}
}

func TestScanner_TruncatedReport(t *testing.T) {
	t.Parallel()
	got := scan(t, "synthetic-truncated-report.log")
	if !got.ReportMissing {
		t.Fatal("ReportMissing = false, want true for a truncated report line")
	}
	if len(got.Managers) != 0 {
		t.Errorf("Managers = %v, want none without a report", got.Managers)
	}
	want := []renovate.UpdateTuple{
		{BranchName: "renovate/lodash-4.x", BranchResult: "pr-updated", PRNumber: 12},
		{BranchName: "renovate/requests-2.x", BranchResult: "pr-created", PRNumber: 13},
	}
	if !slices.Equal(got.Tuples, want) {
		t.Errorf("rebuilt tuples = %+v, want %+v", got.Tuples, want)
	}
	if got.Finished == nil || got.Finished.Result != "done" {
		t.Errorf("Finished = %+v, want done", got.Finished)
	}
}

func TestScanner_NoFinishedLine(t *testing.T) {
	t.Parallel()
	s := renovate.NewScanner(slug)
	s.Feed(`{"msg":"Branch created","repository":"boop-bot/scratch","branch":"renovate/a"}`)
	got := s.Result()
	if got.Finished != nil || !got.ReportMissing || got.Progress.BranchesChanged != 1 {
		t.Errorf("Result() = %+v, want no Finished, ReportMissing, one branch", got)
	}
}

func TestScanner_ExitCodeField(t *testing.T) {
	t.Parallel()
	s := renovate.NewScanner(slug)
	s.Feed(`{"msg":"Repository finished","repository":"boop-bot/scratch","result":"external-host-error","exitCode":7}`)
	if f := s.Result().Finished; f == nil || f.ExitCode != 7 || f.Result != "external-host-error" {
		t.Errorf("Finished = %+v, want external-host-error / 7", f)
	}
}
