// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/omkhar/workcell/internal/host/hoststate"
	"golang.org/x/sys/unix"
)

// sessionCloneDirFD is the descriptor that holds the pinned session directory
// in the child that runs git.
const sessionCloneDirFD = 3

// cmdHelperCloneSessionWorkspace clones SOURCE into PARENT/SESSION/repo with
// GIT. hoststate.CloneSessionWorkspace pins PARENT and the session directory.
// Git runs in a child of this program that sets its working directory with
// fchdir on the pinned session directory and then runs git with the relative
// target repo, so no path walk through PARENT happens before git starts.
func cmdHelperCloneSessionWorkspace(args []string) error {
	parent, session, gitBin, source := args[0], args[1], args[2], args[3]
	if !filepath.IsAbs(gitBin) || !filepath.IsAbs(source) {
		return errors.New("clone-session-workspace needs an absolute git path and an absolute source path")
	}
	self, err := os.Executable()
	if err != nil {
		return err
	}
	return hoststate.CloneSessionWorkspace(parent, session, func(sessionDir *os.File) error {
		cmd := exec.Command(self, "helper", "run-in-directory-fd", gitBin,
			"clone", "--quiet", "--no-hardlinks", "--", source, hoststate.SessionCloneRepoName)
		cmd.ExtraFiles = []*os.File{sessionDir}
		// Git must not look for a repository above the session directory: the
		// directories above it are the source Git directory.
		// A clone does not look for one today; the ceiling keeps it so.
		cmd.Env = append(os.Environ(), "GIT_CEILING_DIRECTORIES="+filepath.Dir(sessionDir.Name()))
		cmd.Stdin = nil
		cmd.Stdout = os.Stderr
		cmd.Stderr = os.Stderr
		return cmd.Run()
	})
}

// cmdHelperRunInDirectoryFD runs PROGRAM with ARGS in the directory open on
// descriptor 3, which its parent passed. It changes directory with fchdir, so
// the directory is the one the parent pinned, whatever path names it now.
func cmdHelperRunInDirectoryFD(args []string) error {
	program := args[0]
	if !filepath.IsAbs(program) {
		return fmt.Errorf("run-in-directory-fd needs an absolute program path: %s", program)
	}
	var stat unix.Stat_t
	if err := unix.Fstat(sessionCloneDirFD, &stat); err != nil {
		return fmt.Errorf("run-in-directory-fd: descriptor %d: %w", sessionCloneDirFD, err)
	}
	if stat.Mode&unix.S_IFMT != unix.S_IFDIR {
		return fmt.Errorf("run-in-directory-fd: descriptor %d is not a directory", sessionCloneDirFD)
	}
	if err := unix.Fchdir(sessionCloneDirFD); err != nil {
		return err
	}
	if err := unix.Close(sessionCloneDirFD); err != nil {
		return err
	}
	return unix.Exec(program, args, os.Environ())
}
