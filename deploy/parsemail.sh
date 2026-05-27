#!/bin/sh
# SPDX-License-Identifier: Apache-2.0
# Copyright (C) 2026 Robin Jarry <robin@jarry.cc>

# Called by postfix pipe transport to feed incoming mail to patchwork.
# Exit 75 (EX_TEMPFAIL) on failure so postfix retries later.

set -a
. /etc/default/patchwork
set +a

cd /opt/patchwork
systemd-cat --identifier=parsemail --priority=debug --stderr-priority=err \
	python3 manage.py parsemail || exit 75
