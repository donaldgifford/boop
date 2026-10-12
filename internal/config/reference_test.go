package config

import (
	"os"
	"reflect"
	"slices"
	"strings"
	"testing"
)

const referencePath = "../../docs/config.md"

// TestReferenceCoversSchema fails when a setting the file accepts is not
// named in docs/config.md, so the reference cannot fall behind the
// schema the way config_path did.
func TestReferenceCoversSchema(t *testing.T) {
	t.Parallel()
	doc, err := os.ReadFile(referencePath)
	if err != nil {
		t.Fatal(err)
	}
	var missing []string
	for _, name := range schemaNames(reflect.TypeFor[fileSchema](), nil) {
		if !strings.Contains(string(doc), "`"+name+"`") {
			missing = append(missing, name)
		}
	}
	if len(missing) > 0 {
		slices.Sort(missing)
		t.Errorf("%s does not mention %s", referencePath, strings.Join(slices.Compact(missing), ", "))
	}
}

// schemaNames returns every attribute and block name in t and the blocks
// it nests, skipping labels, ranges and remain bodies.
func schemaNames(t reflect.Type, out []string) []string {
	for f := range t.Fields() {
		tag, ok := f.Tag.Lookup("hcl")
		if !ok {
			continue
		}
		name, kind, _ := strings.Cut(tag, ",")
		switch kind {
		case "label", "attr_value_range", "def_range", "remain":
			continue
		}
		out = append(out, name)
		if kind == "block" {
			bt := f.Type
			for bt.Kind() == reflect.Pointer || bt.Kind() == reflect.Slice {
				bt = bt.Elem()
			}
			out = schemaNames(bt, out)
		}
	}
	return out
}
