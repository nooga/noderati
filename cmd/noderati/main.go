package main

import (
	"bufio"
	"fmt"
	"io"
	"net/http"
	_ "net/http/pprof"
	"os"
	"path/filepath"
	"strings"

	"github.com/nooga/noderati/internal/host"
	"github.com/nooga/paserati/pkg/driver"
	"github.com/nooga/paserati/pkg/errors"
	"github.com/nooga/paserati/pkg/vm"
)

func main() {
	if addr := os.Getenv("NODERATI_PPROF"); addr != "" {
		go func() {
			_ = http.ListenAndServe(addr, nil)
		}()
	}

	cli := parseNodeArgs(os.Args[1:])
	host.ExecArgv = cli.execArgv
	host.UserConditions = cli.conditions

	execPath, err := os.Executable()
	if err != nil {
		execPath = os.Args[0]
	}

	switch {
	case cli.hasEval && cli.print:
		os.Exit(runEval(execPath, cli.eval, true, cli.rest))
	case cli.hasEval:
		os.Exit(runEval(execPath, cli.eval, false, cli.rest))
	case cli.script != "":
		os.Exit(runFile(execPath, cli.script, cli.rest))
	default:
		os.Exit(runREPL(execPath))
	}
}

type nodeArgs struct {
	execArgv   []string
	conditions []string
	eval       string
	hasEval    bool
	print      bool
	script     string
	rest       []string
}

// nodeValueOptions are Node CLI options whose value may come as the next
// argument ("--conditions dev") rather than after "=".
var nodeValueOptions = map[string]bool{
	"-C": true, "--conditions": true, "-r": true, "--require": true, "--import": true,
	"--loader": true, "--experimental-loader": true, "--input-type": true, "--title": true,
	"--env-file": true, "--inspect-port": true, "--redirect-warnings": true,
	"--unhandled-rejections": true, "--diagnostic-dir": true, "--watch-path": true,
	"--stack-trace-limit": true, "--max-http-header-size": true, "--dns-result-order": true,
}

// parseNodeArgs follows Node's own command line shape: options first
// (collected as process.execArgv), then the script, then the script's
// own arguments, which are never interpreted.
func parseNodeArgs(args []string) nodeArgs {
	var out nodeArgs
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "--" {
			if i+1 < len(args) {
				out.script = args[i+1]
				out.rest = args[i+2:]
			}
			return out
		}
		if !strings.HasPrefix(a, "-") || a == "-" {
			out.script = a
			out.rest = args[i+1:]
			return out
		}
		name, value, hasValue := strings.Cut(a, "=")
		switch name {
		case "-e", "--eval", "-p", "--print":
			out.print = out.print || name == "-p" || name == "--print"
			if !hasValue && i+1 < len(args) {
				i++
				value = args[i]
			}
			out.eval, out.hasEval = value, true
			out.rest = args[i+1:]
			return out
		}
		out.execArgv = append(out.execArgv, a)
		if !hasValue && nodeValueOptions[name] && i+1 < len(args) {
			i++
			value = args[i]
			out.execArgv = append(out.execArgv, value)
		}
		if name == "-C" || name == "--conditions" {
			out.conditions = append(out.conditions, value)
		}
	}
	return out
}

func newHost(argv []string) *driver.Paserati {
	return host.New(argv)
}

func runEval(execPath, source string, print bool, rest []string) int {
	// Node's process.argv for -e/-p is [execPath, ...args]: the flag and
	// the source aren't part of it.
	argv := append([]string{execPath}, rest...)
	p := newHost(argv)
	p.SetSkipTypeCheck(true)
	val, errs := p.RunCode(source, driver.RunOptions{Filename: "[eval]", Script: false, ModuleName: "[eval]"})
	if len(errs) > 0 {
		errors.DisplayErrors(errs, source)
		return 1
	}
	drainAsync(p)
	if print && val != vm.Undefined {
		fmt.Println(val.Inspect())
	}
	return host.ProcessExitCode(p)
}

func runFile(execPath, filename string, extra []string) int {
	abs, err := filepath.Abs(filename)
	if err != nil {
		abs = filename
	}
	srcBytes, err := os.ReadFile(filename)
	if err != nil {
		fmt.Fprintf(os.Stderr, "noderati: %s\n", err)
		return 1
	}
	source := host.StripShebang(string(srcBytes))

	argv := append([]string{execPath, abs}, extra...)
	p := newHost(argv)
	// Native builtins (variadic Go funcs) and npm JS are not fully typed yet.
	p.SetSkipTypeCheck(true)
	ext := strings.ToLower(filepath.Ext(filename))

	var val vm.Value
	var errs []errors.PaseratiError
	if ext == ".ts" || ext == ".mts" || ext == ".mjs" || looksLikeESM(source) {
		val, errs = p.RunCode(source, driver.RunOptions{ModuleName: abs})
	} else {
		val, errs = host.RunCJS(p, source, abs)
	}
	_ = val
	if len(errs) > 0 {
		errors.DisplayErrors(errs, source)
		return 1
	}
	drainAsync(p)
	return host.ProcessExitCode(p)
}

func drainAsync(p *driver.Paserati) {
	if vmInst := p.GetVM(); vmInst != nil {
		vmInst.DrainUntilIdle()
	}
}

func looksLikeESM(source string) bool {
	for _, line := range strings.Split(source, "\n") {
		trim := strings.TrimSpace(line)
		if strings.HasPrefix(trim, "import ") || strings.HasPrefix(trim, "export ") {
			return true
		}
	}
	return false
}

func runREPL(execPath string) int {
	p := newHost([]string{execPath})
	p.SetSkipTypeCheck(true)
	reader := bufio.NewReader(os.Stdin)
	fmt.Println("noderati (Ctrl+D to exit)")
	for {
		fmt.Print("> ")
		line, err := reader.ReadString('\n')
		if err != nil {
			if err == io.EOF {
				fmt.Println()
				return 0
			}
			fmt.Fprintf(os.Stderr, "noderati: %s\n", err)
			return 1
		}
		if strings.TrimSpace(line) == "" {
			continue
		}
		val, errs := p.RunCode(line, driver.RunOptions{})
		if len(errs) > 0 {
			errors.DisplayErrors(errs, line)
			continue
		}
		if val != vm.Undefined {
			fmt.Println(val.Inspect())
		}
	}
}
