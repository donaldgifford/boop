package ingest

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/donaldgifford/boop/internal/config"
)

const (
	testSecret = "it's a secret"
	testBot    = "boop-bot[bot]"
)

var testPaths = []string{"renovate.json5", ".github/renovate.json5"}

// testConfig is one webhook App, boop-bot (app id 1), and an App with no
// webhook (app id 2).
func testConfig() *config.Config {
	return &config.Config{
		Ingest:   config.Ingest{Path: config.DefaultIngestPath},
		Renovate: config.Renovate{ConfigPaths: testPaths},
		Apps: []*config.App{
			{
				Name: "boop-bot", AppID: 1,
				Discovery: config.Discovery{Every: 6 * time.Hour},
				Webhook:   &config.Webhook{Bot: testBot, Secret: config.NewSecret([]byte(testSecret))},
			},
			{Name: "quiet", AppID: 2},
		},
	}
}

// payload reads testdata/<name>.json.
func payload(tb testing.TB, name string) []byte {
	tb.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name+".json"))
	if err != nil {
		tb.Fatal(err)
	}
	return b
}
