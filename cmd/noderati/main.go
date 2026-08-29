package main

import (
	"bufio"
	"flag"
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

	eval := flag.String("e", "", "evaluate script")
	printEval := flag.String("p", "", "evaluate script and print the result")
	flag.Parse()

	execPath, err := os.Executable()
	if err != nil {
		execPath = os.Args[0]
	}

	switch {
	case *printEval != "":
		os.Exit(runEval(execPath, *printEval, true, flag.Args()))
	case *eval != "":
		os.Exit(runEval(execPath, *eval, false, flag.Args()))
	case flag.NArg() >= 1:
		os.Exit(runFile(execPath, flag.Arg(0), flag.Args()[1:]))
	default:
		os.Exit(runREPL(execPath))
	}
}

func newHost(argv []string) *driver.Paserati {
	return host.New(argv)
}

func runEval(execPath, source string, print bool, rest []string) int {
	argv := append([]string{execPath, "-e"}, rest...)
	if print {
		argv[1] = "-p"
	}
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
	if ext == ".ts" || ext == ".mts" || looksLikeESM(source) {
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
