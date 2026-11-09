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

// Command renovate-unsafe-gen writes internal/renovate/unsafe's
// zz_generated.go from a Renovate dist directory (IMPL-0006 task 2.2).
// `just generate-unsafe` extracts the dist from the chart's pinned image
// and runs it:
//
//	renovate-unsafe-gen -dist <dir> -image <ref> -o internal/renovate/unsafe/zz_generated.go
package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/donaldgifford/boop/internal/renovate/unsafe/distscan"
)

func main() {
	dist := flag.String("dist", "", "Renovate dist directory (required)")
	image := flag.String("image", "", "image reference the dist came from (required)")
	out := flag.String("o", "internal/renovate/unsafe/zz_generated.go", "output file")
	flag.Parse()

	if err := run(*dist, *image, *out); err != nil {
		fmt.Fprintln(os.Stderr, "renovate-unsafe-gen:", err)
		os.Exit(1)
	}
}

func run(dist, image, out string) error {
	if dist == "" || image == "" {
		return fmt.Errorf("-dist and -image are required")
	}
	d, err := distscan.Scan(dist, image)
	if err != nil {
		return err
	}
	src, err := distscan.Render(&d)
	if err != nil {
		return err
	}
	if err := os.WriteFile(out, src, 0o644); err != nil {
		return fmt.Errorf("writing %s: %w", out, err)
	}
	fmt.Printf("wrote %s: Renovate %s, %d managers, %d exec, %d gated\n",
		out, d.Version, len(d.Managers), len(d.ExecManagers), len(d.Gated))
	return nil
}
