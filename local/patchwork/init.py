#!/usr/bin/env python3
# SPDX-License-Identifier: Apache-2.0
# Copyright (C) 2026 Robin Jarry <robin@jarry.cc>

import os
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
        "listemail": "test-project@pwforge.test",
    },
)

user.profile.maintainer_projects.add(project)

webhook, _ = Webhook.objects.get_or_create(
    project=project,
    url="http://localhost:9090/patchwork",
    secret="",
    events="*",
    creator=user,
)

print(f"user: {user.username}")
print(f"token: {token.key}")
print(f"project: {project.linkname}")
print(f"webhook: {webhook.url}")
