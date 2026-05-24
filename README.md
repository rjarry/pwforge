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

## License

Apache-2.0
