#!/bin/bash
# SPDX-License-Identifier: Apache-2.0
# Copyright (C) 2026 Robin Jarry <robin@jarry.cc>

set -eu

# initialize database from pristine copy if not present
if [ ! -f /data/patchwork.db.sqlite3 ]; then
	cp /opt/patchwork/patchwork.db.pristine /data/patchwork.db.sqlite3
fi

# pipe delivery needs write access to the database
chmod 0777 /data
chmod 0666 /data/patchwork.db.sqlite3

exec "$@"
