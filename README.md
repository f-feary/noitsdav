# No, it's DAV

`noitsdav` is a thin proxy written in Golang that allows you to access (stream) files on from one or more FTP servers through a WebDAV interface. It supports full file downloads as well as byte-range requests, so can be used for streaming movies.

## Why?

Streaming movies. Your use case may differ.

## How?

First, create your config. See `config.json.sample` for a sample.

Then, run it:

`go run ./cmd/noitsdav --config myconfig.json`


## Supported operations

List files and get only. No write operations are supported. Feel free to whack this in a PR if this is a feature you want.

## Security Notice

This is really, really not secure. It's intended to sit on a LAN behind a firewall, that's it. It would be a mistake to expose an instance of this service to the internet. Really, don't do it.