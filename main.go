// waked executes programs when macOS resumes from sleep.
package main

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
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
  ` + appName + ` executes programs when macOS resumes from sleep. By default,
  it executes all programs found in directory-path. If directory-path
  is not specified, then '` + defaultExesDirPath + `' is used.

  Executables containing '` + needsUnlockStr + `' in their name will only be executed
  once the screen is unlocked.

  ` + appName + ` will continuously re-execute a program if it exits with a non-zero
  exit status.

OPTIONS
`

	helpArg = "h"

	defaultExesDirPath = "/usr/local/etc/" + appName
	needsUnlockStr     = "-on-unlock"
)

func main() {
	err := mainWtihError()
	if err != nil {
		log.Fatalln("fatal:", err)
	}
}

func mainWtihError() error {
	// If we do not runtime.LockOSThread, then we never get events.
	// This is related to starting a Go routine to monitor for
	// OS signals.
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

	// Here we use the NSNotificationCenter via the shared workspace
	// to receive NSWorkspaceDidWakeNotification events.
	//
	// In order to do this, we need to execute the macOS app entrypoint
	// code. If we do not do this, we never get events. Stackoverflow
	// user Hans Passant notes:
	//
	//   "A console mode app for example, that won't work,
	//   CFRunLoopRun() is crucial to allow the OS to make
	//   callbacks."
	//   - https://stackoverflow.com/questions/64009042/not-receiving-nsworkspacewillsleepnotification-from-notificationcenter-using-c-s#comment113219829_64009042
	//
	// Examples:
	// https://forums.developer.apple.com/forums/thread/26430
	// https://developer.apple.com/documentation/foundation/nsnotificationcenter/1411723-addobserverforname?language=objc
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

type execCtl struct {
	ctx            context.Context
	exesDir        string
	mu             sync.Mutex
	stopChildrenFn func(error)
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
	o.mu.Lock()
	defer o.mu.Unlock()

	infos, err := os.ReadDir(o.exesDir)
	if err != nil {
		log.Printf("failed to read executables directory %q - %s",
			o.exesDir, err)

		return
	}

	if o.stopChildrenFn != nil {
		o.stopChildrenFn(errors.New("recieved new wake event"))
	}

	ctx, cancelFn := context.WithCancelCause(o.ctx)
	o.stopChildrenFn = cancelFn

	for _, info := range infos {
		if info.IsDir() {
			continue
		}

		exePath := filepath.Join(o.exesDir, info.Name())

		go execRetry(ctx, exePath)
	}
}

func execRetry(ctx context.Context, exePath string) error {
	for {
		_, err := os.Stat(exePath)
		if err != nil {
			log.Printf("[%s] no longer stat'able - %s", exePath, err)

			return err
		}

		err = execOnce(ctx, exePath)
		if err == nil {
			return nil
		}

		select {
		case <-ctx.Done():
			log.Printf("[%s] giving up - %s", ctx.Err())

			return ctx.Err()
		default:
		}

		waitFor := 10 * time.Second

		if errors.Is(err, screenLockedErr) {
			waitFor = 5 * time.Second
		}

		log.Printf("[%s] exec failed, will retry in %s - %s",
			exePath, waitFor.String(), err)

		select {
		case <-ctx.Done():
			log.Printf("[%s] giving up - %s", ctx.Err())

			return ctx.Err()
		case <-time.After(waitFor):
			continue
		}
	}
}

var screenLockedErr = errors.New("screen is locked")

func execOnce(ctx context.Context, exePath string) error {
	if strings.Contains(filepath.Base(exePath), needsUnlockStr) {
		isLocked, err := checkIfLocked(ctx)
		switch {
		case isLocked:
			return screenLockedErr
		case err != nil:
			log.Printf("[warn] failed to determine if screen is locked - %s", err)
		}
	}

	ctx, cancelFn := context.WithTimeoutCause(
		ctx,
		10*time.Minute,
		errors.New("timed-out waiting for child process to exit"))
	defer cancelFn()

	exe := exec.CommandContext(ctx, exePath)

	stderr := newExeLogger(exePath)
	defer stderr.Close()

	stdout := newExeLogger(exePath)
	defer stdout.Close()

	exe.Stderr = stderr
	exe.Stdout = stdout

	err := exe.Run()
	if err != nil {
		return fmt.Errorf("exec failed - %w", err)
	}

	return nil
}

func newExeLogger(exePath string) *exeLogger {
	r, w := io.Pipe()

	l := &exeLogger{
		exePath: exePath,
		r:       r,
		w:       w,
	}

	go l.loop()

	return l
}

type exeLogger struct {
	exePath string
	r       io.ReadCloser
	w       io.WriteCloser
}

func (o *exeLogger) Write(b []byte) (int, error) {
	return o.w.Write(b)
}

func (o *exeLogger) Close() error {
	o.r.Close()
	o.w.Close()

	return nil
}

func (o *exeLogger) loop() {
	scanner := bufio.NewScanner(o.r)

	for scanner.Scan() {
		log.Printf("[%s] %s", o.exePath, scanner.Text())
	}
}

// Based on work by Joel Bruner:
// https://stackoverflow.com/a/66723000
//
// We could use Go's XML parser here, but I do not feel
// like dealing with Apple's plist format.
func checkIfLocked(ctx context.Context) (bool, error) {
	// /usr/sbin/ioreg -n Root -d1 -a
	ioreg := exec.CommandContext(
		ctx,
		"/usr/sbin/ioreg",
		"-n",
		"Root",
		"-d1",
		"-a")

	ioregOutput, err := ioreg.CombinedOutput()
	if err != nil {
		return false, fmt.Errorf("ioreg failed (%v) - %w - output: %q",
			ioreg.Args, err, ioregOutput)
	}

	const coreGraphicsParam = "CGSSessionScreenIsLocked"

	if !bytes.Contains(ioregOutput, []byte(coreGraphicsParam)) {
		return false, nil
	}

	// /usr/bin/plutil -extract 'IOConsoleUsers.0.CGSSessionScreenIsLocked' raw -
	plutil := exec.CommandContext(
		ctx,
		"/usr/bin/plutil",
		"-extract",
		"IOConsoleUsers.0."+coreGraphicsParam,
		"raw",
		"-")

	plutil.Stdin = bytes.NewReader(ioregOutput)

	plutilOutput, err := plutil.CombinedOutput()
	if err != nil {
		return false, fmt.Errorf("plutil (%v) failed - %w - output: %q",
			plutil.Args, err, plutilOutput)
	}

	plutilOutput = bytes.TrimSpace(plutilOutput)

	return bytes.Equal([]byte("true"), plutilOutput), nil
}
