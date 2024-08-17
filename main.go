// waked executes programs when macOS resumes from sleep.
package main

import (
	"context"
	"errors"
	"flag"
	"log"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"syscall"
	"time"

	"github.com/progrium/darwinkit/macos"
	"github.com/progrium/darwinkit/macos/appkit"
	"github.com/progrium/darwinkit/macos/foundation"
)

const (
	appName = "waked"

	usage = appName + `

SYNOPSIS
  ` + appName + ` [options] [directory-path]

DESCRIPTION
  waked executes programs when macOS resumes from sleep. By default,
  it executes all programs found in directory-path. If directory-path
  is not specified, then '` + defaultExesDirPath + `' is used.

OPTIONS
`

	helpArg = "h"

	defaultExesDirPath = "/usr/local/" + appName
)

func main() {
	err := mainWtihError()
	if err != nil {
		log.Fatalln("fatal:", err)
	}
}

func mainWtihError() error {
	// If we do not runtime.LockOSThread, then we never get events.
	runtime.LockOSThread()

	help := flag.Bool(helpArg, false, "Display this information")

	// TODO: Use syslog - unfortunately, syslog library is broken
	// - thanks, Apple: https://github.com/golang/go/issues/59229
	flag.Parse()

	if *help {
		os.Stderr.WriteString(usage)
		flag.PrintDefaults()

		os.Exit(1)
	}

	ctx, cancelFn := signal.NotifyContext(
		context.Background(),
		syscall.SIGINT, syscall.SIGTERM, syscall.SIGQUIT)
	defer cancelFn()

	go func() {
		<-ctx.Done()

		log.Fatalf("recieved signal - %s", ctx.Err())
	}()

	exesDir := flag.Arg(0)
	if exesDir == "" {
		exesDir = defaultExesDirPath
	}

	ctl := execCtl{
		ctx:     ctx,
		exesDir: exesDir,
	}

	err := ctl.validate()
	if err != nil {
		return err
	}

	// Examples:
	// https://forums.developer.apple.com/forums/thread/26430
	// https://developer.apple.com/documentation/foundation/nsnotificationcenter/1411723-addobserverforname?language=objc
	//
	// "A console mode app for example, that won't work,
	// CFRunLoopRun() is crucial to allow the OS to make callbacks."
	//
	// - https://stackoverflow.com/questions/64009042/not-receiving-nsworkspacewillsleepnotification-from-notificationcenter-using-c-s#comment113219829_64009042
	//
	macos.RunApp(func(appkit.Application, *appkit.ApplicationDelegate) {
		nc := appkit.Workspace_SharedWorkspace().NotificationCenter()

		queue := foundation.OperationQueue_MainQueue()

		nc.AddObserverForNameObjectQueueUsingBlock(
			foundation.NotificationName("NSWorkspaceDidWakeNotification"),
			nil,
			queue,
			ctl.onEvent,
		)
	})

	return nil
}

// TODO: Cancel existing execs if called again.
type execCtl struct {
	ctx     context.Context
	exesDir string
}

func (o *execCtl) validate() error {
	if o.exesDir == "" {
		return errors.New("please specify a directory containing executables to execute")
	}

	o.exesDir = filepath.Clean(o.exesDir)

	if o.ctx == nil {
		return errors.New("context is nil")
	}

	return nil
}

func (o *execCtl) onEvent(foundation.Notification) {
	infos, err := os.ReadDir(o.exesDir)
	if err != nil {
		log.Printf("failed to read executables directory %q - %s",
			o.exesDir, err)

		return
	}

	for _, info := range infos {
		if info.IsDir() {
			continue
		}

		exePath := filepath.Join(o.exesDir, info.Name())

		err := execRetry(o.ctx, exePath)
		if err != nil {
			log.Printf("[%s] failed to execute - %s", exePath, err)
		}
	}
}

func execRetry(ctx context.Context, exePath string) error {
	ctx, cancelFn := context.WithTimeoutCause(
		ctx,
		10*time.Minute,
		errors.New("timed out waiting for child process to finish"))
	defer cancelFn()

	for {
		exe := exec.CommandContext(ctx, exePath)

		exe.Stderr = os.Stderr
		exe.Stdout = os.Stdout

		err := exe.Run()
		if err != nil {
			log.Printf("[%s] retrying... (reason: %s)", exePath, err)

			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(time.Minute):
				continue
			}
		}

		return nil
	}
}
