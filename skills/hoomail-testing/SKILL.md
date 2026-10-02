---
name: hoomail-testing
description: Set up Hoomail as a local mail catcher, connect an application's SMTP settings, and verify captured messages, attachments, HTML, calendar invitations, or offline inspection through its UI and HTTP API. Use in consumer projects and integration tests.
---

# Test application email with Hoomail

Work in the user's application. Determine whether a Hoomail instance already
exists, whether the sender runs on the host or in a container, and whether inbox
data must be retained. Skill installation supplies instructions, not the server.
For automated assertions read the bundled [API guide](references/api-testing.md).

## Run a local instance

The release container runs as UID/GID 65532 and needs writable database storage.
For a new local named volume and container:

```bash
docker volume create hoomail-data
docker run --rm -v hoomail-data:/data alpine:3.22 chown 65532:65532 /data
docker run -d --name hoomail \
  -p 127.0.0.1:3000:3000 \
  -p 127.0.0.1:2525:2525 \
  -p 127.0.0.1:3110:3110 \
  -v hoomail-data:/app/data \
  ghcr.io/openhoo/hoomail:0.10.0
```

The tag is a concrete supported example, not a claim about the latest release.
Use the project's selected version/digest when one exists. Reuse an existing
instance/volume without deleting its data; use separate names/ports for isolated
tests. Verify `docker exec hoomail /hoomail healthcheck` and open
`http://localhost:3000`. Health checks HTTP, SMTP connection, and POP3 greeting;
it does not send mail.

## Connect the sender

| Setting | Host-based application |
| --- | --- |
| SMTP host | `127.0.0.1` |
| SMTP port | `2525` |
| TLS/STARTTLS | Disabled |
| Authentication | None |
| Web/API URL | `http://127.0.0.1:3000` |

For an application container on a shared Compose network, use the Hoomail
service name and its in-container SMTP port. Container `localhost` targets the
sender itself. Keep these settings scoped to development/tests. Hoomail captures
SMTP recipients locally; it does not deliver to real external inboxes.

Listeners have no authentication/TLS and bind all interfaces inside the
container. Host loopback mappings or a trusted isolated network are required.
Do not expose the unauthenticated HTTP/SMTP/POP3 services publicly.

## Prove a real application flow

1. Trigger the application's mail-producing action using a unique test
   recipient such as `run-123@example.test` and a correlation marker.
2. Poll the API with a finite timeout for that recipient and marker, then assert
   sender, recipients, subject, body, and the requested attachment/calendar
   details. Do not pick the latest message without correlating it to this run.
3. Open the message in the UI for HTML/layout work. Preview sanitizes active
   markup and blocks remote resources; compare against the intended safe sender
   content, not against supposed Outlook/Gmail pixel equivalence.
4. For inspection use the Inspect tab or `/api/messages/{id}/inspect`. Check
   analysis completeness and evidence-backed findings; inspection is local and
   cannot establish DNS authentication, reputation, deliverability, or working
   unsubscribe endpoints.

`POST /api/send-test` is useful to prove Hoomail's SMTP capture independently:

```bash
curl --fail-with-body -H 'Content-Type: application/json' \
  --data '{"to":"developer@example.test","kind":"plain"}' \
  http://127.0.0.1:3000/api/send-test
```

This does not prove the application's SMTP configuration. Calendar kinds
`invite`, `update`, and `cancellation` share a per-recipient UID; verify their
sequence through the mailbox events API. POP3 uses inbox address as username,
accepts any password, and commits deletions only on `QUIT`.

## Cleanup and diagnosis

Use unique recipients for shared instances and delete only this run's messages
or mailbox when cleanup is requested. `/api/reset` deletes all data and resets
IDs; reserve it for an explicitly disposable isolated instance.

For connection failures check host/container routing and actual listener ports.
For missing mail compare SMTP envelope recipients with visible MIME `To`/`Cc`.
SMTP rejects messages above 25 MiB with 552. For startup failures check volume
ownership and port conflicts. For persistence keep `/app/data` including SQLite
WAL sidecars; ephemeral storage intentionally loses captured data on replacement.
Report the actual captured message evidence and remaining limitations.
