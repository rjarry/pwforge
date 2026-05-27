// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 Robin Jarry <robin@jarry.cc>

package main

import (
	"context"
	"errors"
	"flag"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	"github.com/rjarry/pwforge/config"

	_ "github.com/rjarry/pwforge/github"
)

func main() {
	var configPath string
	var syslog bool
	flag.StringVar(&configPath, "config", "", "path to config file")
	flag.BoolVar(&syslog, "syslog", false, "log to syslog")
	flag.Parse()

	if err := LogInit(syslog); err != nil {
		log.Fatalf("syslog: %v", err)
	}

	conf, err := config.LoadConfig(configPath)
	if err != nil {
		Fatalf("config: %v", err)
	}

	// Subscribe to signals for graceful shutdown.
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM, syscall.SIGQUIT)

	sock, srv, err := NewServer(conf)
	if err != nil {
		Fatalf("listen: %s", err)
	}

	go func() {
		Infof("listening for webhooks on http://%s", conf.Listen)
		err = srv.Serve(sock)
		// dummy signal here in case Serve() failed prematurely
		sig <- syscall.SIGCHLD
	}()

	Infof("received signal %v: shutting down...", <-sig)

	e := srv.Shutdown(context.Background())
	srv.StopQueues()

	if errors.Is(err, http.ErrServerClosed) {
		err = e
	}
	if err != nil {
		Fatalf("server: %v", err)
	}
}
