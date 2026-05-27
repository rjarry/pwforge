# Deploying pwforge

Single-host deployment with patchwork (uwsgi), pwforge, postfix, nginx and postgresql.

## Prerequisites

    apt install postgresql nginx certbot python3-certbot-nginx \
        uwsgi uwsgi-plugin-python3 python3-pip postfix git

Or use the Makefile:

    make -C deploy deps

## Quick install

    make -C deploy install DOMAIN=patches.example.com

Passwords and secrets are auto-generated on first run and stored in
`/etc/default/patchwork` and `/etc/pwforge/config.ini`. Subsequent
runs are idempotent and preserve existing secrets.

To force-regenerate the patchwork env file:

    make -C deploy envfile DB_PASSWORD=xxx DJANGO_SECRET=xxx

## DNS records

For `patches.example.com` (adjust to your subdomain):

    patches.example.com.          A     <server-ip>
    patches.example.com.          AAAA  <server-ipv6>
    patches.example.com.          MX    10 patches.example.com.
    patches.example.com.          TXT   "v=spf1 a -all"
    _dmarc.patches.example.com.   TXT   "v=DMARC1; p=none"

## What gets installed

- **Patchwork** in `/opt/patchwork` with pwforge patches applied,
  served by uwsgi behind nginx.
- **PostgreSQL** database `patchwork` with TCP password auth.
- **Postfix** accepting mail from configured peers, delivering to
  patchwork via pipe transport.
- **pwforge** bridge binary at `/usr/local/bin/pwforge`.
- **nginx** reverse proxy with TLS (certbot).

## Configuration files

| File | Purpose |
|------|---------|
| `/etc/default/patchwork` | Environment variables for Django |
| `/etc/pwforge/config.ini` | pwforge bridge configuration |
| `/etc/uwsgi/apps-enabled/patchwork.ini` | uwsgi app config |
| `/etc/postfix/main.cf` | Postfix main config |
| `/etc/postfix/transport` | Mail routing to patchwork |
| `/etc/nginx/sites-enabled/pwforge` | nginx site config |

## Patchwork admin setup

After install, create the admin user and configure projects:

    set -a && . /etc/default/patchwork && set +a
    cd /opt/patchwork
    python3 manage.py createsuperuser

Then access the admin console at `https://patches.example.com/admin/`.

## GitHub App setup

Visit `https://patches.example.com/pwforge/setup` and click "Register GitHub
App". This creates a GitHub App with the correct permissions and
webhook URL. After confirming on GitHub, install the app on your
repositories, then restart pwforge.

Alternatively, create a GitHub App manually and configure `app-id`,
`private-key-file`, and `webhook-secret` in `/etc/pwforge/config.ini`.

## Patchwork webhook

Create a webhook in patchwork (via admin UI or API):

- URL: `https://patches.example.com/pwforge/patchwork`
- Events: `*`

## Mailing list subscription

Subscribe the mail address for your domain to the upstream mailing
list. Postfix accepts and pipes it to patchwork via parsemail.
