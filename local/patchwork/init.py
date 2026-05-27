#!/usr/bin/env python3
# SPDX-License-Identifier: Apache-2.0
# Copyright (C) 2026 Robin Jarry <robin@jarry.cc>

import django

django.setup()

from django.contrib.auth.models import User
from rest_framework.authtoken.models import Token
from patchwork.models import Project, Webhook

user, _ = User.objects.get_or_create(
    username="admin",
    defaults={
        "email": "admin@pwforge.test",
        "is_staff": True,
        "is_superuser": True,
    },
)
user.set_password("admin")
user.save()

token, _ = Token.objects.get_or_create(
    user=user,
    defaults={"key": "test-patchwork-token"},
)

project, _ = Project.objects.get_or_create(
    linkname="test-project",
    defaults={
        "name": "Test Project",
        "listid": "test-project.pwforge.test",
        "listemail": "test-project@lists.pwforge.test",
        "auto_supersede": True,
    },
)

user.profile.maintainer_projects.add(project)

# pwforge service account (for API access and check reporting)
bot, _ = User.objects.get_or_create(
    username="pwforge",
    defaults={
        "email": "bridge@pwforge.test",
    },
)
bot.save()

bot.profile.maintainer_projects.add(project)

bot_token, _ = Token.objects.get_or_create(
    user=bot,
    defaults={"key": "test-pwforge-token"},
)

webhook, _ = Webhook.objects.get_or_create(
    project=project,
    url="http://localhost:9090/patchwork",
    secret="",
    events="*",
    creator=bot,
)

print(f"admin user: {user.username}")
print(f"pwforge user: {bot.username}")
print(f"pwforge token: {bot_token.key}")
print(f"project: {project.linkname}")
print(f"webhook: {webhook.url}")
