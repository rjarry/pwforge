# SPDX-License-Identifier: Apache-2.0
# Copyright (c) 2026 Robin Jarry
#
# Makefile for deploying pwforge + patchwork on a single host.
# Run as root or with sudo.

DOMAIN ?= patches.example.com
DB_NAME ?= patchwork
DB_USER ?= patchwork
DB_PASSWORD ?= $(shell openssl rand -base64 24)
DJANGO_SECRET ?= $(shell openssl rand -base64 48)

.PHONY: help
help:
	@echo "usage: make <target> [VAR=value ...]"
	@echo ""
	@echo "targets:"
	@echo "  deps       install system packages"
	@echo "  database   create postgresql database"
	@echo "  patchwork  install patchwork in /opt/patchwork"
	@echo "  uwsgi      install uwsgi config and service"
	@echo "  postfix    install postfix config"
	@echo "  pwforge    build and install pwforge"
	@echo "  nginx      install nginx config and obtain TLS cert"
	@echo "  install    install everything (idempotent)"
	@echo ""
	@echo "variables:"
	@echo "  DOMAIN            $(DOMAIN)"
	@echo "  DB_NAME           $(DB_NAME)"
	@echo "  DB_USER           $(DB_USER)"
	@echo "  DB_PASSWORD       (auto-generated if unset)"
	@echo "  DJANGO_SECRET     (auto-generated if unset)"

DEB := git nginx certbot python3-certbot-nginx postfix opendkim opendkim-tools
DEB += uwsgi uwsgi-plugin-python3
DEB += postgresql python3-psycopg2
DEB += python3-django python3-djangorestframework python3-django-filters

.PHONY: deps
deps:
	apt-get update -q
	apt-get install -qy --no-install-recommends $(DEB)
	usermod -a -G opendkim postfix

/etc/default/patchwork:
	@echo "DJANGO_SETTINGS_MODULE=patchwork.settings.production" > $@
	@echo "DJANGO_SECRET_KEY=$(DJANGO_SECRET)" >> $@
	@echo "DATABASE_NAME=$(DB_NAME)" >> $@
	@echo "DATABASE_USER=$(DB_USER)" >> $@
	@echo "DATABASE_PASSWORD=$(DB_PASSWORD)" >> $@
	@echo "DATABASE_HOST=localhost" >> $@
	@echo "DATABASE_PORT=" >> $@
	@echo "STATIC_ROOT=/var/www/patchwork" >> $@
	@echo "ALLOWED_HOSTS=$(DOMAIN)" >> $@
	@echo "FROM_EMAIL='Patchwork <no-reply@$(DOMAIN)>'" >> $@
	chmod 640 $@
	chgrp www-data $@

.PHONY: database
database: /etc/default/patchwork
	@set -e; . $<; \
	user=`sudo -u postgres psql -tXAc "SELECT 1 FROM pg_roles WHERE rolname='$$DATABASE_USER';"`; \
	if [ -z "$$user" ]; then \
		sudo -u postgres psql -c "CREATE USER $$DATABASE_USER PASSWORD '$$DATABASE_PASSWORD';"; \
		sudo -u postgres createdb -O $$DATABASE_USER $$DATABASE_NAME; \
	fi

/opt/patchwork/.git/config:
	git clone --depth=1 https://github.com/getpatchwork/patchwork /opt/patchwork
	git -C /opt/patchwork am -3 $$PWD/../local/patchwork/*.patch

/opt/patchwork/patchwork/settings/production.py: production.py
	install -Dm 640 -g www-data $< $@

.PHONY: patchwork
patchwork: /etc/default/patchwork /opt/patchwork/.git/config /opt/patchwork/patchwork/settings/production.py
	mkdir -p /var/www/patchwork
	set -a && . /etc/default/patchwork && set +a -xe && \
		cd /opt/patchwork && \
		python3 manage.py migrate --noinput && \
		python3 manage.py collectstatic --noinput && \
		python3 manage.py loaddata default_tags default_states

/etc/uwsgi/apps-available/patchwork.ini: uwsgi.ini
	install -m 0644 $< $@

/etc/uwsgi/apps-enabled/patchwork.ini: /etc/uwsgi/apps-available/patchwork.ini
	ln -sfr $< $@

/etc/systemd/system/%.service: %.service
	install -m 0644 $< $@

.PHONY: uwsgi
uwsgi: /etc/uwsgi/apps-enabled/patchwork.ini /etc/systemd/system/patchwork.service

/etc/postfix/%: postfix/%
	install -D -m 0644 $< $@
	sed -i 's/patches.example.com/$(DOMAIN)/g' $@

/opt/patchwork/parsemail.sh: parsemail.sh
	install -D -m 0755 $< $@

/etc/dkimkeys/$(DOMAIN)/mail.private:
	mkdir -p $(@D)
	opendkim-genkey -d $(DOMAIN) -s mail -D $(@D)
	chown -R opendkim:opendkim $(@D)
	sed -Ei '/^(Domain|Selector|KeyFile)/d' /etc/opendkim.conf
	echo 'Domain     $(DOMAIN)' >> /etc/opendkim.conf
	echo 'Selector   mail' >> /etc/opendkim.conf
	echo 'KeyFile    $@' >> /etc/opendkim.conf

.PHONY: postfix
postfix: $(addprefix /etc/,$(wildcard postfix/*)) /opt/patchwork/parsemail.sh /etc/dkimkeys/$(DOMAIN)/mail.private
	postmap /etc/postfix/transport
	postmap /etc/postfix/recipient_bcc

/etc/pwforge/config.ini: config.ini
	install -d -m 770 -g www-data /etc/pwforge
	install -m 660 -g www-data $< $@
	sed -i 's/patches.example.com/$(DOMAIN)/g' $@

.PHONY: pwforge
pwforge: /etc/pwforge/config.ini /etc/systemd/system/pwforge.service
	$(MAKE) -C .. install

/etc/nginx/sites-enabled/$(DOMAIN): nginx.conf
	@if ! [ -r "/etc/letsencrypt/live/$(DOMAIN)/cert.pem" ]; then \
		set -xe; \
		rm -f $@ /etc/nginx/sites-enabled/pwforge_; \
		awk '/^server / {count++} count<2' $< | \
			sed 's/patches.example.com/$(DOMAIN)/g' > \
			/etc/nginx/sites-enabled/pwforge_; \
		systemctl reload nginx; \
		mkdir -p /var/www/acme; \
		certbot certonly --webroot -w /var/www/acme -d $(DOMAIN); \
		rm -f /etc/nginx/sites-enabled/pwforge_; \
	fi
	install -Dm 644 $< /etc/nginx/sites-available/$(DOMAIN)
	sed -i 's/patches.example.com/$(DOMAIN)/g' /etc/nginx/sites-available/$(DOMAIN)
	ln -sfr /etc/nginx/sites-available/$(DOMAIN) $@
	nginx -t

.PHONY: nginx
nginx: /etc/nginx/sites-enabled/$(DOMAIN)

.PHONY: install
install: database patchwork uwsgi postfix pwforge nginx
	systemctl daemon-reload
	systemctl enable --now patchwork patchwork pwforge postfix nginx opendkim
	systemctl restart patchwork patchwork pwforge postfix nginx opendkim
	@echo
	@echo "Update your DNS zone with the following entries:"
	@echo
	@ip -j addr | jq -r '.[].addr_info[] | select(.scope == "global" and .family == "inet") | .local' | \
		sed 's/^/$(DOMAIN). 10800 IN A /'
	@ip -j addr | jq -r '.[].addr_info[] | select(.scope == "global" and .family == "inet6") | .local' | \
		sed 's/^/$(DOMAIN). 10800 IN AAAA /'
	@echo "$(DOMAIN). 10800 IN MX 10 $(DOMAIN)."
	@echo '$(DOMAIN). 10800 IN TXT "v=spf1 a -all"'
	@sed -E 's/^(mail._domainkey)/\1.$(DOMAIN)./' /etc/dkimkeys/$(DOMAIN)/mail.txt
