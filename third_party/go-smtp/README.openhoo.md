# OpenHoo go-smtp fork

This directory is a source fork of
`github.com/emersion/go-smtp` tag `v0.24.0`, commit
`ab24fe7cbe995d404af3b1c093195f2f43b94688`.

The fork carries two narrow protocol-safety changes:

- DATA accepts a message exactly at `MaxMessageBytes` while still rejecting
  the next byte.
- BDAT restores command line limits after every consumed chunk and fully
  discards an over-limit chunk before parsing the next command.

Regression coverage lives in
`internal/smtpserver/smtpserver_test.go`, including exact DATA limits,
successful non-LAST BDAT chunks, and over-limit BDAT stream resynchronization.

When updating upstream:

1. Diff every source file against the recorded upstream commit.
2. Reapply or retire each documented patch deliberately.
3. Run this module's checks and the top-level SMTP server tests.
4. Update this commit reference in the same change.

The upstream and local code remain covered by the license in this directory.
