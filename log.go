// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Robin Jarry

package main

import (
	"fmt"
	"log"
	"log/syslog"
	"os"
)

var (
	dbg  *log.Logger
	info *log.Logger
	warn *log.Logger
	err  *log.Logger
)

func LogInit(toSyslog bool) (e error) {
	flags := log.Lshortfile
	if toSyslog {
		if dbg, e = syslog.NewLogger(syslog.LOG_DEBUG, flags); e != nil {
			return e
		}
		if info, e = syslog.NewLogger(syslog.LOG_INFO, flags); e != nil {
			return e
		}
		if warn, e = syslog.NewLogger(syslog.LOG_WARNING, flags); e != nil {
			return e
		}
		if err, e = syslog.NewLogger(syslog.LOG_ERR, flags); e != nil {
			return e
		}
	} else {
		flags |= log.Ldate | log.Ltime
		dbg = log.New(os.Stdout, "DEBUG ", flags)
		info = log.New(os.Stdout, "INFO ", flags)
		warn = log.New(os.Stdout, "WARN ", flags)
		err = log.New(os.Stdout, "ERROR ", flags)
	}
	return nil
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
