# pwforge

Bidirectional synchronization bridge between mailing list patch workflows (via
[Patchwork](https://github.com/getpatchwork/patchwork)) and code forge pull
requests (GitHub, with more forges planned).

When a patch series is submitted on a mailing list and tracked by Patchwork,
`pwforge` automatically creates a pull request on the configured forge. Replies
to patches are mirrored as PR comments. Conversely, PR comments and reviews
posted on the forge are sent back to the mailing list as properly threaded
email replies.

## Building

    make

## Configuration

Copy `config.example.ini` and edit it:

    cp config.example.ini config.ini
    $EDITOR config.ini

## Running

    ./pwforge -config config.ini

The service exposes two webhook endpoints:

- `POST /patchwork` -- receives Patchwork event webhooks
- `POST /forge` -- receives forge (GitHub) event webhooks

Both endpoints verify HMAC-SHA256 signatures.

## Testing

Start a full local testbed in a tmux session:

    make local

This requires `gh` (GitHub CLI) to be authenticated and `tmux` to be installed.
On first run it creates a private GitHub repository (default: `pwforge-testbed`
under your account) and reuses it on subsequent runs. Data is persisted in
`./build/` so that successive runs pick up where everything was stopped.

The tmux session has the following windows:

| Window | Content |
|--------|---------|
| 0:testbed | The local testbed script output |
| 1:patchwork | patchwork running on with an sqlite database |
| 2:postfix | postfix wired to patchwork parsemail script |
| 3:pwforge | pwforge bridge (this project) |
| 4:webhooks | `gh webhook forward` (GitHub -> local pwforge) |
| 5:mail | mail client (aerc, neomutt or mutt) |
| 6:git | local clone for preparing patch series |

From the git window, create commits and send them to the testbed mailing list
(git send-email is pre-configured):

    git send-email origin/main

Patches will appear in patchwork and in the mail client. If pwforge receives
a `series-completed` webhook from patchwork, it creates a PR on the GitHub test
repo. Comments on the PR are forwarded back as emails via the webhook
forwarding.

Environment variables:

* `PWFORGE_TESTBED_REPO` -- Name of the private GitHub repo to create/reuse
  (default `pwforge-testbed`)
* `PWFORGE_WEBHOOK_SECRET` -- Shared secret for GitHub webhook signature
  verification (default `testbed-webhook-secret`)

## License

Apache-2.0
