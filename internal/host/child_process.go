package host

import (
	"bytes"
	"fmt"
	"os/exec"

	"github.com/nooga/paserati/pkg/driver"
)

type spawnSyncResult struct {
	Status int    `json:"status"`
	Stdout string `json:"stdout"`
	Stderr string `json:"stderr"`
}

func declareChildProcess(p *driver.Paserati) {
	p.DeclareModule("child_process", func(m *driver.ModuleBuilder) {
		m.Function("spawnSync", func(command string, args []string, _ ...interface{}) *spawnSyncResult {
			cmd := exec.Command(command, args...)
			var stdout, stderr bytes.Buffer
			cmd.Stdout = &stdout
			cmd.Stderr = &stderr
			err := cmd.Run()
			status := 0
			if err != nil {
				if ee, ok := err.(*exec.ExitError); ok {
					status = ee.ExitCode()
				} else {
					status = 1
				}
			}
			return &spawnSyncResult{
				Status: status,
				Stdout: stdout.String(),
				Stderr: stderr.String(),
			}
		})
		m.Function("spawn", func(command string, _ ...interface{}) (interface{}, error) {
			return nil, fmt.Errorf("child_process.spawn is not implemented")
		})
		m.Default(nil)
	})
	_ = p.DeclareModuleAlias("node:child_process", "child_process")
}
