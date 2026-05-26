# SPDX-License-Identifier: Apache-2.0
# Copyright (C) 2026 Robin Jarry <robin@jarry.cc>

from patchwork.settings.dev import *  # noqa: F401,F403

DATABASES["default"].setdefault("OPTIONS", {})
DATABASES["default"]["OPTIONS"]["timeout"] = 30
DATABASES["default"]["OPTIONS"]["init_command"] = (
    "PRAGMA journal_mode=WAL; PRAGMA busy_timeout=30000;"
)

LOGGING["handlers"]["console"]["filters"] = []
LOGGING["loggers"]["django"]["level"] = "INFO"
LOGGING["loggers"]["patchwork.parser"]["level"] = "DEBUG"
LOGGING["loggers"]["patchwork.management.commands.parsemail"]["level"] = "DEBUG"
LOGGING["loggers"]["patchwork.webhooks"] = {
    "handlers": ["console"],
    "level": "DEBUG",
    "propagate": False,
}
LOGGING["loggers"]["patchwork.signals"] = {
    "handlers": ["console"],
    "level": "DEBUG",
    "propagate": False,
}
