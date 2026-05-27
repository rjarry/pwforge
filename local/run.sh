#!/bin/bash
# SPDX-License-Identifier: Apache-2.0
# Copyright (C) 2026 Robin Jarry <robin@jarry.cc>
#
# Start a local testbed with patchwork, postfix, pwforge and a mail
# client in a tmux session. Data is persisted in ./testdata/ so that
# successive runs pick up where everything was stopped.
#
# Usage: ./local/run.sh

if [ -z "$_TMUX_SOCK" ]; then
	unset TMUX
	export _TMUX_SOCK="pwforge-$$"
	exec tmux -L "$_TMUX_SOCK" new-session -n testbed "$0" "$@"
fi

set -euo pipefail

SCRIPT_DIR=$(dirname "$0")
ROOT_DIR=$(git rev-parse --show-toplevel)
GITHUB_REPO=${PWFORGE_TESTBED_REPO:-pwforge-testbed}
GH_WEBHOOK_SECRET=${PWFORGE_WEBHOOK_SECRET:-testbed-webhook-secret}
GH_UPSTREAM_OWNER=${PWFORGE_UPSTREAM_OWNER:-}
GH_UPSTREAM_REPO=${PWFORGE_UPSTREAM_REPO:-}
BUILDDIR="$ROOT_DIR/build"

die() {
	echo "error: $*" >&2
	false
}


cleanup() {
	local rc=$?
	set +e
	if [ "$rc" -ne 0 ]; then
		echo
		read -rp 'press enter to close'
	fi
	# stop containers first
	podman rm -f pwforge-patchwork pwforge-postfix 2>/dev/null
	# kill all pane process trees
	local pids=$(tmux list-panes -a -F '#{pane_pid}' 2>/dev/null)
	for pid in $pids; do
		if ! [ "$pid" = "$$" ]; then
			kill -- -"$pid" 2>/dev/null || kill "$pid" 2>/dev/null
		fi
	done
	# this kills our own shell last
	tmux kill-server 2>/dev/null
}

trap cleanup EXIT

command -v tmux >/dev/null || die "tmux is required"
command -v podman >/dev/null || die "podman is required"
command -v gh >/dev/null || die "gh (GitHub CLI) is required"
command -v curl >/dev/null || die "curl is required"

# find a mail client
MAIL_CMD=""
for cmd in aerc neomutt mutt; do
	if command -v "$cmd" >/dev/null; then
		MAIL_CMD=$cmd
		break
	fi
done
[ -n "$MAIL_CMD" ] || die "no mail client found (tried aerc, neomutt, mutt)"

# -- persistent data directory ------------------------------------------------

mkdir -p $BUILDDIR/patchwork-db $BUILDDIR/maildir/INBOX/{new,cur,tmp}

# -- github repo setup --------------------------------------------------------

GH_USER=$(gh api user -q .login)
GH_FULL_REPO="$GH_USER/$GITHUB_REPO"
GH_WEBHOOK_REPO="${GH_UPSTREAM_OWNER:+$GH_UPSTREAM_OWNER/$GH_UPSTREAM_REPO}"
GH_WEBHOOK_REPO="${GH_WEBHOOK_REPO:-$GH_FULL_REPO}"
if ! gh repo view "$GH_FULL_REPO" >/dev/null 2>&1; then
	if [ -n "$GH_UPSTREAM_OWNER" ]; then
		echo "forking $GH_UPSTREAM_OWNER/$GH_UPSTREAM_REPO as $GH_FULL_REPO..."
		gh repo fork "$GH_UPSTREAM_OWNER/$GH_UPSTREAM_REPO" \
			--fork-name "$GITHUB_REPO" --clone=false
	else
		echo "creating github repo $GH_FULL_REPO..."
		gh repo create "$GITHUB_REPO" --add-readme --public
	fi
	sleep 2
fi
GH_TOKEN=$(gh auth token)

# clone the github repo if not already present
if [ ! -d "$BUILDDIR/workdir/.git" ]; then
	rm -rf "$BUILDDIR/workdir"
	GIT_CONFIG_GLOBAL=/dev/null GIT_CONFIG_SYSTEM=/dev/null \
		git clone "https://github.com/$GH_FULL_REPO.git" "$BUILDDIR/workdir"
	git -C "$BUILDDIR/workdir" config user.email developer@pwforge.test
	git -C "$BUILDDIR/workdir" config user.name Developer
fi

# always update git send-email config (ports may change)
git -C "$BUILDDIR/workdir" config sendemail.smtpServer localhost
git -C "$BUILDDIR/workdir" config sendemail.smtpServerPort 1587
git -C "$BUILDDIR/workdir" config sendemail.smtpEncryption none
git -C "$BUILDDIR/workdir" config sendemail.confirm never
git -C "$BUILDDIR/workdir" config sendemail.to test-project@lists.pwforge.test

# -- mail client config -------------------------------------------------------

MAIL_CONF_DIR="$BUILDDIR/mail-config"
mkdir -p "$MAIL_CONF_DIR"

case "$MAIL_CMD" in
aerc)
	cat > "$MAIL_CONF_DIR/accounts.conf" <<-EOF
	[testbed]
	source = maildir://$BUILDDIR/maildir
	outgoing = smtp+insecure://localhost:1587
	default = INBOX
	from = Developer <developer@pwforge.test>
	copy-to = INBOX
	check-mail = 5s
	check-mail-cmd = true
	EOF
	MAIL_LAUNCH="aerc -A $MAIL_CONF_DIR/accounts.conf"
	;;
neomutt|mutt)
	cat > "$MAIL_CONF_DIR/muttrc" <<-EOF
	set mbox_type = Maildir
	set folder = "$BUILDDIR/maildir"
	set spoolfile = "$BUILDDIR/maildir"
	set smtp_url = "smtp://localhost:1587"
	set from = "Developer <developer@pwforge.test>"
	set ssl_starttls = no
	set ssl_force_tls = no
	set realname = "Developer"
	EOF
	MAIL_LAUNCH="$MAIL_CMD -F $MAIL_CONF_DIR/muttrc"
	;;
esac

# -- build --------------------------------------------------------------------

echo "building pwforge..."
make -C "$ROOT_DIR" pwforge

echo "building patchwork image..."
podman build -t localhost/pwforge-patchwork "$SCRIPT_DIR"

# -- pwforge config -----------------------------------------------------------

cat > "$BUILDDIR/config.ini" <<EOF
listen = :9090
forge = github

[patchwork]
url = http://localhost:8888
token = test-pwforge-token
project = test-project

[github]
token = $GH_TOKEN
webhook-secret = $GH_WEBHOOK_SECRET
owner = ${GH_UPSTREAM_OWNER:-$GH_USER}
repo = ${GH_UPSTREAM_REPO:-$GITHUB_REPO}
$([ -n "$GH_UPSTREAM_OWNER" ] && echo "fork-owner = $GH_USER")
$([ -n "$GH_UPSTREAM_REPO" ] && echo "fork-repo = $GITHUB_REPO")

[smtp]
host = localhost
port = 1587
encryption = none
from = Pwforge Bridge <bridge@pwforge.test>
to = Test Project <test-project@lists.pwforge.test>

[git]
mirror-path = $BUILDDIR/mirror
subject-prefix = PATCH test
EOF

# -- ensure gh-webhook extension ----------------------------------------------

if ! gh extension list 2>/dev/null | grep -q cli/gh-webhook; then
	echo "installing gh webhook extension..."
	gh extension install cli/gh-webhook
fi

# clean up stale webhooks from previous runs
for hook_id in $(gh api "repos/$GH_WEBHOOK_REPO/hooks" -q '.[].id' 2>/dev/null); do
	echo "deleting stale webhook $hook_id..."
	gh api -X DELETE "repos/$GH_WEBHOOK_REPO/hooks/$hook_id" 2>/dev/null || true
done

# -- add tmux windows ---------------------------------------------------------

# window 1: patchwork container
tmux new-window -d -n patchwork \
	"podman rm -f pwforge-patchwork 2>/dev/null; \
	 podman run --rm -it \
		--name pwforge-patchwork \
		--network=host \
		-v $BUILDDIR/patchwork-db:/data:Z \
		localhost/pwforge-patchwork; \
	 read -rp 'patchwork exited, press enter to close'"

# wait for patchwork
echo "waiting for patchwork..."
for _ in $(seq 1 30); do
	if curl -fIL http://localhost:8888/api/1.5/; then
		break
	fi
	sleep 1
done

# window 2: postfix container
tmux new-window -d -n postfix \
	"podman rm -f pwforge-postfix 2>/dev/null; \
	 podman run --rm -it \
		--name pwforge-postfix \
		--network=host \
		-v $BUILDDIR/patchwork-db:/data:Z \
		-v $BUILDDIR/maildir:/root/Maildir:Z \
		localhost/pwforge-patchwork postfix -v start-fg; \
	 read -rp 'postfix exited, press enter to close'"

cat >$BUILDDIR/watch_pwforge.sh <<EOF
#!/bin/sh

set -x

trap 'kill \$(jobs -p) 2>/dev/null' EXIT

while true; do
	$ROOT_DIR/pwforge -config $BUILDDIR/config.ini &
	pid=\$!
	inotifywait -e attrib $ROOT_DIR/pwforge
	kill \$pid
	wait \$pid
	sleep 1
done
EOF
chmod 755 $BUILDDIR/watch_pwforge.sh

# window 3: pwforge (auto-restart on binary change)
tmux new-window -d -n pwforge $BUILDDIR/watch_pwforge.sh

# window 4: webhook forwarding
tmux new-window -d -n webhooks \
	"gh webhook forward \
		--repo=$GH_WEBHOOK_REPO \
		--events='*' \
		--url=http://localhost:9090/forge \
		--secret=$GH_WEBHOOK_SECRET; \
	 read -rp 'webhook forward exited, press enter to close'"

# window 5: mail client
tmux new-window -d -n mail \
	"$MAIL_LAUNCH; \
	 read -rp 'mail client exited, press enter to close'"

# window 6: local git clone
tmux new-window -d -n git -c "$BUILDDIR/workdir"

echo
echo "============================================"
echo " pwforge testbed ready"
echo "============================================"
echo
echo "  patchwork:       http://localhost:8888"
echo "  api:             http://localhost:8888/api/1.5/"
echo "  admin:           admin / admin"
echo "  pwforge token:   test-pwforge-token"
echo "  smtp:            localhost:1587"
echo "  list address:    test-project@lists.pwforge.test"
echo "  github repo:     https://github.com/$GH_FULL_REPO (private)"
echo "  pwforge:         http://localhost:9090"
echo "  build dir:       $BUILDDIR"
echo
echo "Available tmux windows:"
echo
tmux list-windows
echo
echo "Press Ctrl-C to terminate all services."

tail -f /dev/null
