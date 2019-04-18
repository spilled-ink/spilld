# spilled.ink

[Spilled.ink](https://spilled.ink/) is a new take on open source email infrastructure.

This repository contains the spilld SMTP and IMAP server,
built around a new SQLite-based storage format, spillbox.

The project is very new.

## The spilld server

The main binary is called **spilld**. The development version can be
installed with go get:

```go get -u spilled.ink/cmd/spilld```

## The spillbox storage format

**NOTE: this is a pre-release**, and the format is in flux.
It may change in incompatible ways and no tool will be published
to automatically migrate spillbox data until the project uses
version numbers. Store precious data in spillbox at your own risk.

A lot of documentation will be coming in the next few weeks.

## The spillbox tool

`spillbox` is a command-line tool for managing a spilldb database.
The development version can be installed with go get:

```go get -u spilled.ink/cmd/spillbox```

TODO: document

## License

The code is licensed under the GPL.
If the GPL does not suit your needs, alternate licenses are for sale
at reasonable rates. The goal is not to make big $$$ out of licensing
(ha!), but to make sure the project maintains focus on indie email
servers, while not locking out anyone else who has a reasonable use
for the project.

To that end, contributors need to sign a contributor license agreement.
