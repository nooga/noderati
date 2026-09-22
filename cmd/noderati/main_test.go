package main

import (
	"reflect"
	"testing"
)

// Expected splits match what real Node reports as process.execArgv /
// process.argv.slice(2) for the same command lines.
func TestParseNodeArgs(t *testing.T) {
	cases := []struct {
		args       []string
		execArgv   []string
		conditions []string
		script     string
		rest       []string
		eval       string
	}{
		{args: []string{"--conditions", "dev", "-C", "x", "s.mjs", "a", "--flag"}, execArgv: []string{"--conditions", "dev", "-C", "x"}, conditions: []string{"dev", "x"}, script: "s.mjs", rest: []string{"a", "--flag"}},
		{args: []string{"--trace-warnings", "s.mjs"}, execArgv: []string{"--trace-warnings"}, script: "s.mjs", rest: []string{}},
		{args: []string{"s.mjs", "--conditions", "y"}, script: "s.mjs", rest: []string{"--conditions", "y"}},
		{args: []string{"--conditions=dev", "s.mjs"}, execArgv: []string{"--conditions=dev"}, conditions: []string{"dev"}, script: "s.mjs", rest: []string{}},
		{args: []string{"-e", "1+2", "extra"}, eval: "1+2", rest: []string{"extra"}},
		{args: []string{"--", "s.mjs", "-x"}, script: "s.mjs", rest: []string{"-x"}},
	}
	for _, c := range cases {
		got := parseNodeArgs(c.args)
		if !reflect.DeepEqual(got.execArgv, c.execArgv) || !reflect.DeepEqual(got.conditions, c.conditions) ||
			got.script != c.script || got.eval != c.eval || len(got.rest) != len(c.rest) || (len(c.rest) > 0 && !reflect.DeepEqual(got.rest, c.rest)) {
			t.Errorf("parseNodeArgs(%q) = %+v", c.args, got)
		}
	}
}
