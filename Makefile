# SPDX-License-Identifier: Apache-2.0
# Copyright (c) 2026 Robin Jarry

GO ?= go
PREFIX ?= /usr/local
BINDIR ?= $(PREFIX)/bin

V ?= 0
ifeq ($V,1)
Q =
else
Q = @
endif

.PHONY: all
all: pwforge

.PHONY: clean
clean:
	rm -f pwforge

.PHONY: install
install: $(DESTDIR)$(BINDIR)/pwforge

go_src = $(shell git ls-files '*.go') go.mod go.sum

pwforge: $(go_src)
	$(GO) build -trimpath -o $@

$(DESTDIR)$(BINDIR)/pwforge: pwforge
	install -D -m 0755 $< $@

.PHONY: local
local:
	./local/run.sh

.PHONY: format
format:
	gofmt -w .

license_exclude = LICENSE *.md *.asc .* config.example.ini go.mod go.sum *.patch *.cf local

.PHONY: lint
lint:
	@echo '[golangci-lint]'
	$Q $(GO) run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.11.4 run
	@echo '[license-check]'
	$Q ! git ls-files $(addprefix :!:,$(license_exclude)) | while read -r f; do \
		if ! grep -qF 'SPDX-License-Identifier: Apache-2.0' $$f; then \
			echo $$f; \
		fi; \
		if ! grep -q 'Copyright .* [0-9]\{4\} .*' $$f; then \
			echo $$f; \
		fi; \
	done | LC_ALL=C sort -u | grep --color . || { \
		echo 'error: files are missing license and/or copyright notice'; \
		exit 1; \
	}
	@echo '[white-space]'
	$Q git ls-files ':!:*.patch' | xargs devtools/check-whitespace
	@echo '[comments]'
	$Q devtools/check-comments $(go_src) Makefile
	@echo '[codespell]'
	$Q codespell *

REVISION_RANGE ?= origin/main..

.PHONY: check-commits
check-commits:
	$Q ./check-commits $(REVISION_RANGE)

.PHONY: tag-release
tag-release:
	@cur_version=`sed -En 's/.* \|\| echo v([0-9\.]+)\>.*$$/\1/p' Makefile` && \
	next_version=`echo $$cur_version | awk -F. -v OFS=. '{$$(NF) += 1; print}'` && \
	read -rp "next version ($$next_version)? " n && \
	if [ -n "$$n" ]; then next_version="$$n"; fi && \
	set -xe && \
	sed -i "s/\<v$$cur_version\>/v$$next_version/" Makefile && \
	git commit -sm "pwforge: release v$$next_version" -m "`git shortlog -sn v$$cur_version..`" Makefile && \
	git tag -sm "v$$next_version" "v$$next_version"
