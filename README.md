# FastGD

FastGD mounts the "My Drive" of a Google account as an ordinary folder on Linux, over FUSE. Folders list at once from a local database, what you read stays in a local cache, and what you write goes to Drive in the background.

Unofficial, not affiliated with Google.

## Install

Packages for Debian and its kin and archives for amd64 and arm64 are at <https://fastgd.ruslandenisovich.com/>, and the guide to set FastGD up at <https://fastgd.ruslandenisovich.com/setup>.

With Go 1.26 or later, and a key of your own (below):

```bash
go install github.com/Ruslan-Linuxoid/FastGD/cmd/fastgd@latest
```

## Your own key

The packages and archives of the site carry the key of the app FastGD and sign in through it. A build of your own, by `go install` or from these sources, carries none, and needs an OAuth client of your own:

1. In the [Google Cloud console](https://console.cloud.google.com/), make a project and enable the Google Drive API in it.
2. Set up its OAuth consent screen, and publish the app: Audience, Publish app, to the status In production. An app left in Testing gets sign-ins that Google ends after 7 days. Google need not verify the app: the sign-in warns that it is not verified, and goes on under Advanced.
3. Under Credentials, make an OAuth client ID of the type Desktop app, and download its JSON.
4. Sign in with it, `fastgd auth -client-id client_id.json -token token.json`, and name it in the config of the service: `client_id_file = "/etc/fastgd/client_id.json"`.

## Build and test

The build and the tests run in Docker; nothing is installed on the host.

```bash
make build   # bin/fastgd
make test    # the tests of every package, with -race
make e2e     # the daemon with a real FUSE mount against an emulator of Drive
```

## License

Copyright (C) 2026 Ruslan

FastGD is free software under the GNU General Public License, version 3 or later; see [LICENSE](LICENSE).
