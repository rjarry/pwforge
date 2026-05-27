// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 Robin Jarry <robin@jarry.cc>

package main

import (
	"flag"
	"log"

	"github.com/rjarry/pwforge/config"

	_ "github.com/rjarry/pwforge/github"
)

func main() {
	var configPath string
	flag.StringVar(&configPath, "config", "", "path to config file")
	flag.Parse()

	conf, err := config.LoadConfig(configPath)
	if err != nil {
		log.Fatalf("config: %v", err)
	}

	srv := NewServer(conf)
	if err := srv.ListenAndServe(); err != nil {
		log.Fatalf("server: %v", err)
	}
}
