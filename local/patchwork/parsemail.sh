#!/bin/sh
# SPDX-License-Identifier: Apache-2.0
# Copyright (C) 2026 Robin Jarry <robin@jarry.cc>

cd /opt/patchwork

if ! /usr/local/bin/python3 manage.py parsemail; then
	# EX_TEMPFAIL: tell postfix to retry later
	exit 75
fi >>/tmp/parsemail.log 2>&1
