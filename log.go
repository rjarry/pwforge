// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Robin Jarry

package main

import (
	"fmt"
	"io"
	"log"
	"log/syslog"
	"os"
)

var (
	dbg  *log.Logger
	info *log.Logger
	warn *log.Logger
	err  *log.Logger

	Stdout io.Writer
	Stderr io.Writer
)

func newSyslog(prio syslog.Priority) *log.Logger {
	w, e := syslog.New(prio, "pwforge")
	if e != nil {
		log.Fatalf("error: syslog.New: %v", e)
	}
	switch prio {
	case syslog.LOG_DEBUG:
		Stdout = w
	case syslog.LOG_WARNING:
		Stderr = w
	}
	return log.New(w, "", log.Lshortfile)
}

func LogInit(toSyslog bool) {
	if toSyslog {
		dbg = newSyslog(syslog.LOG_DEBUG)
		info = newSyslog(syslog.LOG_INFO)
		warn = newSyslog(syslog.LOG_WARNING)
		err = newSyslog(syslog.LOG_ERR)
	} else {
		const flags = log.Lshortfile | log.Ldate | log.Ltime
		dbg = log.New(os.Stdout, "DEBUG ", flags)
		info = log.New(os.Stdout, "INFO ", flags)
		warn = log.New(os.Stdout, "WARN ", flags)
		err = log.New(os.Stderr, "ERROR ", flags)
		Stdout = os.Stdout
		Stderr = os.Stderr
	}
}

func logFormat(message string, args ...any) string {
	if len(args) > 0 {
		message = fmt.Sprintf(message, args...)
	}
	return message
}

func Debugf(message string, args ...any) {
	dbg.Output(2, logFormat(message, args...)) //nolint:errcheck // oh well
}

func Infof(message string, args ...any) {
	info.Output(2, logFormat(message, args...)) //nolint:errcheck // oh well
}

func Warnf(message string, args ...any) {
	warn.Output(2, logFormat(message, args...)) //nolint:errcheck // oh well
}

func Errorf(message string, args ...any) {
	err.Output(2, logFormat(message, args...)) //nolint:errcheck // oh well
}

func Fatalf(message string, args ...any) {
	err.Output(2, logFormat(message, args...)) //nolint:errcheck // oh well
	os.Exit(1)
}
