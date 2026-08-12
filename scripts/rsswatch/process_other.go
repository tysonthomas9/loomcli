//go:build !darwin && !linux

package main

import (
	"errors"
	"os/exec"
)

func configureProcessGroup(_ *exec.Cmd) error {
	return errors.New("process-tree RSS sampling is supported only on Darwin and Linux")
}

func killProcessGroup(_ int) error {
	return nil
}

func readProcessTable() (map[int]processSample, error) {
	return nil, errors.New("process-tree RSS sampling is supported only on Darwin and Linux")
}
