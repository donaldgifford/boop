package config_test

import (
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/utils/ptr"

	"github.com/donaldgifford/boop/internal/config"
	"github.com/donaldgifford/boop/internal/jobspec"
)

const goldenPath = "testdata/golden.hcl"

func loadGolden(t *testing.T) *config.Config {
	t.Helper()
	cfg, diags := config.Load(goldenPath)
	if diags.HasErrors() {
		t.Fatalf("Load(%s): %v", goldenPath, diags.Error())
	}
	cfg.SecretsDir = t.TempDir()
	for _, f := range []struct{ rel, value string }{
		{"boop-bot-app/private-key.pem", "pem"},
		{"boopd-redis/url", "redis://:pw@boopd-redis:6379/0\n"},
	} {
		p := filepath.Join(cfg.SecretsDir, f.rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(f.value), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := cfg.ReadSecrets(); err != nil {
		t.Fatalf("ReadSecrets: %v", err)
	}
	return cfg
}

// TestGolden_BaselineBuildInput: the design's example, as HCL, renders the
// BuildInput the jobspec tests use (env_test.go baseInput), and the same
// environment.
func TestGolden_BaselineBuildInput(t *testing.T) {
	t.Parallel()
	cfg := loadGolden(t)
	repo := jobspec.Repo{ID: 101, Slug: "donaldgifford/boop", DefaultBranch: "main", InstallationID: 55}

	got, err := cfg.BuildInput(cfg.App("boop-bot"), "baseline", repo)
	if err != nil {
		t.Fatalf("BuildInput() = %v", err)
	}
	// The per-run fields the activity sets.
	got.TokenSecret = "run-101-0"
	got.WorkflowID = "repo/github/101"
	got.RunID = "0199c0de-0000-7000-8000-000000000000"

	want := &jobspec.BuildInput{
		Namespace: "boopd",
		Image:     "ghcr.io/renovatebot/renovate:44-full@sha256:0000000000000000000000000000000000000000000000000000000000000000",
		App: jobspec.App{
			Endpoint:     "https://api.github.com",
			SharedPreset: "github>boop-bot/renovate-config:default.json",
			Global:       map[string]any{"prHourlyLimit": float64(2), "automerge": false},
			LogLevel:     "info",
			RedisURL:     "redis://:pw@boopd-redis:6379/0",
			GitAuthor:    "boop-bot <boop-bot[bot]@users.noreply.github.com>",
		},
		Repo:        repo,
		TokenSecret: "run-101-0",
		WorkflowID:  "repo/github/101",
		RunID:       "0199c0de-0000-7000-8000-000000000000",
	}
	want.Profile = got.Profile // the overlay is checked below
	if !reflect.DeepEqual(got, want) {
		t.Errorf("BuildInput() =\n%+v\nwant\n%+v", got, want)
	}
	if got.Profile.Name != "baseline" || got.Profile.Pod.Labels["boopd.dev/egress"] != "baseline" {
		t.Errorf("baseline profile = %+v", got.Profile)
	}

	gotEnv, err := jobspec.BuildEnv(got)
	if err != nil {
		t.Fatalf("BuildEnv(config input) = %v", err)
	}
	want.Profile = jobspec.Profile{Name: "baseline"}
	wantEnv, err := jobspec.BuildEnv(want)
	if err != nil {
		t.Fatalf("BuildEnv(jobspec fixture) = %v", err)
	}
	if !reflect.DeepEqual(gotEnv, wantEnv) {
		t.Errorf("BuildEnv differs:\n got %v\nwant %v", gotEnv, wantEnv)
	}
}

// TestGolden_InheritedProfiles checks the python and strict overlays
// after inheritance: baseline's requests and cpu limit stay, python's
// memory limit, runtime class, label and Renovate options win.
func TestGolden_InheritedProfiles(t *testing.T) {
	t.Parallel()
	cfg := loadGolden(t)
	wantPod := jobspec.PodOverlay{
		Labels:           map[string]string{"boopd.dev/egress": "python"},
		RuntimeClassName: ptr.To("gvisor"),
		Resources: &corev1.ResourceRequirements{
			Requests: corev1.ResourceList{"cpu": resource.MustParse("1"), "memory": resource.MustParse("2Gi")},
			Limits:   corev1.ResourceList{"cpu": resource.MustParse("2"), "memory": resource.MustParse("6Gi")},
		},
	}
	wantRenovate := map[string]any{
		"customEnvVariables": map[string]any{"PIP_ONLY_BINARY": ":all:", "UV_NO_BUILD": "1"},
	}
	for _, name := range []string{"python", "strict"} {
		in, err := cfg.BuildInput(cfg.App("boop-bot"), name, jobspec.Repo{ID: 1, Slug: "o/r"})
		if err != nil {
			t.Fatalf("BuildInput(%s) = %v", name, err)
		}
		if !podEqual(&in.Profile.Pod, &wantPod) {
			t.Errorf("profile %s pod = %+v, want %+v", name, in.Profile.Pod, wantPod)
		}
		if !reflect.DeepEqual(in.Profile.Renovate, wantRenovate) {
			t.Errorf("profile %s renovate = %v, want %v", name, in.Profile.Renovate, wantRenovate)
		}
	}

	// BuildInput hands out copies: mutating one leaves the config alone.
	in, err := cfg.BuildInput(cfg.App("boop-bot"), "python", jobspec.Repo{ID: 1, Slug: "o/r"})
	if err != nil {
		t.Fatal(err)
	}
	in.Profile.Renovate["customEnvVariables"].(map[string]any)["PIP_ONLY_BINARY"] = "changed"
	in.Profile.Pod.Labels["boopd.dev/egress"] = "changed"
	again, err := cfg.BuildInput(cfg.App("boop-bot"), "python", jobspec.Repo{ID: 1, Slug: "o/r"})
	if err != nil {
		t.Fatal(err)
	}
	if again.Profile.Renovate["customEnvVariables"].(map[string]any)["PIP_ONLY_BINARY"] != ":all:" ||
		again.Profile.Pod.Labels["boopd.dev/egress"] != "python" {
		t.Error("BuildInput() shares maps with the config")
	}
}

// TestGolden_Sandbox checks the sandbox block's defaults and what
// BuildInput hands jobspec for a sandboxed and an ordinary profile.
func TestGolden_Sandbox(t *testing.T) {
	t.Parallel()
	cfg := loadGolden(t)
	want := &config.Sandbox{
		RuntimeClass:   "kata",
		ServiceAccount: config.DefaultSandboxServiceAccount,
		NodeSelector:   map[string]string{"boopd.dev/sandbox": "true"},
		Tolerations: []corev1.Toleration{
			{Key: "boopd.dev/sandbox", Value: "true", Effect: corev1.TaintEffectNoSchedule},
		},
		EgressAllow:      []string{"github.com", "api.github.com"},
		FloorMode:        config.FloorReport,
		FloorAdd:         []string{"gomod"},
		TokenPermissions: config.DefaultTokenPermissions(),
		PendingTimeout:   11 * time.Minute,
	}
	if !reflect.DeepEqual(cfg.Sandbox, want) {
		t.Errorf("Sandbox =\n%+v\nwant\n%+v", cfg.Sandbox, want)
	}
	if !slices.Contains(cfg.Floor(), "gomod") || !slices.Contains(cfg.Floor(), "poetry") {
		t.Errorf("Floor() = %v, want the built-in floor plus gomod", cfg.Floor())
	}

	jvm, err := cfg.BuildInput(cfg.App("boop-bot"), "jvm", jobspec.Repo{ID: 1, Slug: "o/r"})
	if err != nil {
		t.Fatal(err)
	}
	wantSandbox := &jobspec.Sandbox{
		RuntimeClassName:   "kata",
		ServiceAccountName: "boopd-sandbox",
		NodeSelector:       want.NodeSelector,
		Tolerations:        want.Tolerations,
	}
	if !reflect.DeepEqual(jvm.Sandbox, wantSandbox) || !jvm.Profile.Sandboxed ||
		!slices.Equal(jvm.Profile.UnsafeExecutions, []string{"gradleWrapper"}) {
		t.Errorf("jvm BuildInput sandbox = %+v, profile = %+v", jvm.Sandbox, jvm.Profile)
	}
	if jvm.ForceDisable != nil || jvm.HostUsers != nil {
		t.Errorf("jvm ForceDisable = %v, HostUsers = %v; want neither on a sandboxed run", jvm.ForceDisable, jvm.HostUsers)
	}
	jvm.Sandbox.NodeSelector["changed"] = "x"
	if _, shared := cfg.Sandbox.NodeSelector["changed"]; shared {
		t.Error("BuildInput() shares the sandbox node selector with the config")
	}

	base, err := cfg.BuildInput(cfg.App("boop-bot"), "baseline", jobspec.Repo{ID: 1, Slug: "o/r"})
	if err != nil {
		t.Fatal(err)
	}
	if base.Sandbox != nil || base.ForceDisable != nil || base.HostUsers != nil {
		t.Errorf("baseline in report mode = sandbox %+v, force %v, hostUsers %v; want none", base.Sandbox, base.ForceDisable, base.HostUsers)
	}
}

// TestBuildInput_EnforceAndHostUsers: in enforce mode an ordinary run
// forces the floor off, and runs.host_users = false reaches it; a
// sandboxed profile with no sandbox block gets no Sandbox.
func TestBuildInput_EnforceAndHostUsers(t *testing.T) {
	t.Parallel()
	src, err := os.ReadFile(goldenPath)
	if err != nil {
		t.Fatal(err)
	}
	text := strings.NewReplacer(
		`floor_add    = ["gomod"]`, `floor_add = ["gomod"]`+"\n  floor_mode = \"enforce\"",
		`max_concurrent  = 20`, `max_concurrent = 20`+"\n  host_users = false",
	).Replace(string(src))
	cfg, diags := config.Parse("golden.hcl", []byte(text))
	if diags.HasErrors() {
		t.Fatal(diags.Error())
	}
	in, err := cfg.BuildInput(cfg.App("boop-bot"), "baseline", jobspec.Repo{ID: 1, Slug: "o/r"})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(in.ForceDisable, cfg.Floor()) || in.HostUsers == nil || *in.HostUsers {
		t.Errorf("baseline in enforce mode: ForceDisable = %v, HostUsers = %v; want the floor and false", in.ForceDisable, in.HostUsers)
	}
	jvm, err := cfg.BuildInput(cfg.App("boop-bot"), "jvm", jobspec.Repo{ID: 1, Slug: "o/r"})
	if err != nil {
		t.Fatal(err)
	}
	if jvm.HostUsers != nil || jvm.ForceDisable != nil {
		t.Errorf("jvm: HostUsers = %v, ForceDisable = %v; want neither", jvm.HostUsers, jvm.ForceDisable)
	}

	before, rest, ok := strings.Cut(text, "sandbox {")
	_, after, ok2 := strings.Cut(rest, "\n}\n")
	if !ok || !ok2 {
		t.Fatal("golden.hcl has no sandbox block")
	}
	noBlock, diags := config.Parse("golden.hcl", []byte(before+after))
	if diags.HasErrors() {
		t.Fatal(diags.Error())
	}
	jvm, err = noBlock.BuildInput(noBlock.App("boop-bot"), "jvm", jobspec.Repo{ID: 1, Slug: "o/r"})
	if err != nil {
		t.Fatal(err)
	}
	if jvm.Sandbox != nil || !jvm.Profile.Sandboxed {
		t.Errorf("jvm without a sandbox block: Sandbox = %+v, Sandboxed = %v; want nil and true", jvm.Sandbox, jvm.Profile.Sandboxed)
	}
}

func TestBuildInput_Errors(t *testing.T) {
	t.Parallel()
	cfg := loadGolden(t)
	if _, err := cfg.BuildInput(nil, "baseline", jobspec.Repo{ID: 1}); err == nil {
		t.Error("BuildInput(nil app) err = nil, want error")
	}
	if _, err := cfg.BuildInput(cfg.App("boop-bot"), "nope", jobspec.Repo{ID: 1}); err == nil {
		t.Error("BuildInput(unknown profile) err = nil, want error")
	}
	if cfg.App("nope") != nil {
		t.Error(`App("nope") != nil`)
	}
}

func podEqual(a, b *jobspec.PodOverlay) bool {
	if !reflect.DeepEqual(a.Labels, b.Labels) || !reflect.DeepEqual(a.RuntimeClassName, b.RuntimeClassName) {
		return false
	}
	return quantitiesEqual(a.Resources.Requests, b.Resources.Requests) &&
		quantitiesEqual(a.Resources.Limits, b.Resources.Limits)
}

func quantitiesEqual(a, b corev1.ResourceList) bool {
	if len(a) != len(b) {
		return false
	}
	for k, q := range a {
		w, ok := b[k]
		if !ok || q.Cmp(w) != 0 {
			return false
		}
	}
	return true
}
