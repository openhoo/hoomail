#!/usr/bin/env python3
"""Smoke-test a disposable Hoomail container, including a real SQLite restart.

Uses only Python's standard library. The named container must already be running
with persistent disposable storage and loopback HTTP/SMTP/POP3 port mappings.
"""

import argparse
import base64
import concurrent.futures
import email.policy
import email.utils
import json
import poplib
import re
import smtplib
import socket
import subprocess
import sys
import time
import urllib.error
import urllib.parse
import urllib.request
from email.message import EmailMessage
from pathlib import Path


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--container", required=True)
    parser.add_argument("--http-port", type=int, default=3000)
    parser.add_argument("--smtp-port", type=int, default=2525)
    parser.add_argument("--pop3-port", type=int, default=3110)
    parser.add_argument("--output", type=Path)
    parser.add_argument("--disposable", action="store_true", required=True,
                        help="Confirm that resetting this test container is intended")
    args = parser.parse_args()
    base = f"http://127.0.0.1:{args.http_port}"
    checks = []

    def check(name, condition, **evidence):
        if not condition:
            raise AssertionError(f"{name}: {evidence}")
        checks.append({"check": name, "status": "pass", **evidence})
        print(f"PASS {name}", file=sys.stderr, flush=True)

    def request(path, body=None):
        data = None if body is None else json.dumps(body).encode()
        req = urllib.request.Request(base + path, data=data,
                                     headers={"Content-Type": "application/json"})
        with urllib.request.urlopen(req, timeout=10) as response:
            return response.read(), response.headers

    def api(path, body=None):
        return json.loads(request(path, body)[0])

    def ready():
        deadline = time.monotonic() + 20
        while time.monotonic() < deadline:
            try:
                api("/api/mailboxes")
                return
            except (OSError, urllib.error.URLError):
                time.sleep(0.1)
        raise AssertionError("HTTP server did not become ready")

    def mailboxes():
        return {box["address"]: box for box in api("/api/mailboxes")["mailboxes"]}

    def messages(box, query=""):
        return api(f"/api/mailboxes/{box['id']}/messages?q="
                   + urllib.parse.quote(query))["messages"]

    def send(raw, recipients):
        # Supplying EHLO identity avoids an unbounded local reverse-DNS lookup
        # in smtplib before the SMTP connection's timeout can take effect.
        with smtplib.SMTP("127.0.0.1", args.smtp_port,
                          local_hostname="smoke.example.test", timeout=10) as smtp:
            refused = smtp.sendmail("sender@example.test", recipients, raw)
            if refused:
                raise AssertionError(f"SMTP refused recipients: {refused}")

    def pop(address):
        client = poplib.POP3("127.0.0.1", args.pop3_port, timeout=10)
        client.user(address)
        client.pass_("test")
        return client

    ready()
    subprocess.run(["docker", "exec", args.container, "/hoomail", "healthcheck"], check=True)
    api("/api/reset", {})
    check("isolated-reset", mailboxes() == {})
    index, headers = request("/")
    assets = re.findall(rb'(?:src|href)="(/assets/[^\"]+)"', index)
    check("embedded-ui-and-assets", b"text/html" in headers["Content-Type"].encode()
          and bool(assets) and all(request(asset.decode())[0] for asset in assets),
          assets=len(assets))
    check("openapi", "/api/messages/{messageId}/inspect" in api("/openapi.json")["paths"])

    marker = "Hoomail smoke %_\\ " + str(time.time_ns())
    visible, copied, hidden = ("smoke-to@example.test", "smoke-cc@example.test", "smoke-bcc@example.test")
    message = EmailMessage(policy=email.policy.SMTP)
    message["From"] = "Smoke Sender <sender@example.test>"
    message["To"] = visible
    message["Cc"] = copied
    message["Subject"] = marker
    message["Date"] = email.utils.formatdate(usegmt=True)
    message["Message-ID"] = "<hoomail-smoke@example.test>"
    message.set_content("Plain body " + marker + "\n.leading dot\n")
    message.add_alternative('<html><body><h1>Safe preview</h1><script>bad()</script>'
                            '<img src="https://example.invalid/tracker.png" onerror="bad()">'
                            '<img src="cid:smoke-logo"><a href="https://example.com">Link</a>'
                            '</body></html>', subtype="html")
    png = base64.b64decode("iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO+jVioAAAAASUVORK5CYII=")
    message.get_payload()[1].add_related(png, maintype="image", subtype="png", cid="<smoke-logo>")
    text_attachment = b"attachment smoke bytes\n"
    message.add_attachment(text_attachment, maintype="text", subtype="plain", filename="note.txt")
    message.add_attachment(b"<script>bad()</script>", maintype="text", subtype="html", filename="active.html")
    raw = message.as_bytes()
    stream = urllib.request.urlopen(base + "/api/events", timeout=10)
    try:
        send(raw, [visible.upper(), visible, copied, hidden])
        deadline = time.monotonic() + 10
        event_lines = []
        while time.monotonic() < deadline:
            line = stream.readline().decode()
            if not line:
                break
            event_lines.append(line.strip())
            if "message:new" in line:
                break
        check("sse-real-delivery-invalidation", any("message:new" in line for line in event_lines))
    finally:
        stream.close()
    boxes = mailboxes()
    check("smtp-envelope-bcc-and-normalization", set(boxes) == {visible, copied, hidden}
          and all(box["total_count"] == 1 and box["unread_count"] == 1 for box in boxes.values()),
          recipients=len(boxes))
    box = boxes[visible]
    row = messages(box)[0]
    mid = row["id"]
    source, source_headers = request(f"/api/messages/{mid}/source")
    report = api(f"/api/messages/{mid}/inspect")
    check("source-exact-and-inspect-deterministic", source == raw
          and report == api(f"/api/messages/{mid}/inspect")
          and source_headers["X-Content-Type-Options"] == "nosniff")
    check("source-and-inspect-preserve-unread", mailboxes()[visible]["unread_count"] == 1)
    check("literal-wildcard-search", len(messages(box, marker)) == 1
          and len(messages(box, "missing-marker")) == 0)
    detail = api(f"/api/messages/{mid}")
    html = detail["message"]["html"]
    check("safe-html-and-cid", "<script" not in html.lower() and "onerror" not in html.lower()
          and "example.invalid" not in html and 'target="_blank"' in html
          and "noopener" in html and "/api/attachments/" in html)
    cid_path = re.search(r'/api/attachments/\d+(?:\?inline=cid)?', html).group(0)
    check("cid-bytes", request(cid_path)[0] == png)
    attachments = {item["filename"]: item for item in detail["attachments"]}
    text_bytes, text_headers = request(f"/api/attachments/{attachments['note.txt']['id']}")
    _, active_headers = request(f"/api/attachments/{attachments['active.html']['id']}")
    check("attachment-content-and-execution-policy", text_bytes == text_attachment
          and text_headers["Content-Disposition"].startswith("inline")
          and active_headers["Content-Disposition"].startswith("attachment")
          and active_headers["X-Content-Type-Options"] == "nosniff")
    check("detail-marks-read", mailboxes()[visible]["unread_count"] == 0)
    api("/api/messages/actions", {"action": "unread", "ids": [mid]})
    check("read-unread-actions", mailboxes()[visible]["unread_count"] == 1)

    client = pop(visible)
    check("pop3-retrieval-and-dot-stuffing", client.stat()[0] == 1
          and b"\r\n".join(client.retr(1)[1]) + b"\r\n" == raw)
    client.dele(1)
    client.close()
    check("pop3-aborted-delete-retains-mail", len(messages(box)) == 1)
    client = pop(hidden)
    client.dele(1)
    client.quit()
    check("pop3-quit-commits-delete", mailboxes()[hidden]["total_count"] == 0)

    calendar_recipient = "smoke-calendar@example.test"
    uid = None
    for sequence, kind in enumerate(("invite", "update", "cancellation")):
        api("/api/send-test", {"to": calendar_recipient, "kind": kind, "subject": marker})
        calendar_box = mailboxes()[calendar_recipient]
        events = api(f"/api/mailboxes/{calendar_box['id']}/events")["events"]
        if uid is None:
            uid = events[0]["uid"]
        check(f"calendar-{kind}", len(events) == 1 and events[0]["uid"] == uid
              and events[0]["sequence"] == sequence
              and events[0]["status"] == ("CANCELLED" if kind == "cancellation" else "CONFIRMED"))

    def deliver(index):
        payload = (f"From: sender@example.test\r\nTo: smoke-parallel@example.test\r\n"
                   f"Subject: Parallel {index}\r\n\r\nMessage {index}\r\n").encode()
        send(payload, ["smoke-parallel@example.test"])

    with concurrent.futures.ThreadPoolExecutor(max_workers=8) as pool:
        list(pool.map(deliver, range(20)))
    check("concurrent-smtp-delivery", mailboxes()["smoke-parallel@example.test"]["total_count"] == 20)

    # Coalesce a BDAT command and long binary body in one socket write. A line
    # limit must constrain the command, without scanning or dropping chunk bytes.
    chunk = b"From: sender@example.test\r\nSubject: BDAT smoke\r\n\r\n" + b"x" * 8192
    with socket.create_connection(("127.0.0.1", args.smtp_port), timeout=10) as connection:
        reader = connection.makefile("rb")

        def reply():
            while True:
                line = reader.readline()
                if not line:
                    raise AssertionError("SMTP closed before reply")
                if len(line) < 4 or line[3:4] != b"-":
                    return line

        reply()
        for command in (b"EHLO smoke", b"MAIL FROM:<sender@example.test>", b"RCPT TO:<smoke-bdat@example.test>"):
            connection.sendall(command + b"\r\n")
            if not reply().startswith(b"250"):
                raise AssertionError("SMTP command rejected")
        connection.sendall(f"BDAT {len(chunk)} LAST\r\n".encode() + chunk)
        check("coalesced-bdat-long-body", reply().startswith(b"250"))
        connection.sendall(b"QUIT\r\n")
        reply()
    bdat_box = mailboxes()["smoke-bdat@example.test"]
    check("bdat-preserves-exact-bytes", request(f"/api/messages/{messages(bdat_box)[0]['id']}/source")[0] == chunk)

    with socket.create_connection(("127.0.0.1", args.smtp_port), timeout=10) as connection:
        reader = connection.makefile("rb")
        reply()
        for command in (b"EHLO smoke", b"MAIL FROM:<sender@example.test>", b"RCPT TO:<smoke-short@example.test>"):
            connection.sendall(command + b"\r\n")
            if not reply().startswith(b"250"):
                raise AssertionError("SMTP command rejected")
        connection.sendall(f"BDAT {len(chunk) + 100} LAST\r\n".encode() + chunk)
        connection.shutdown(socket.SHUT_WR)
        check("short-bdat-rejected", not reply().startswith(b"250"))
    check("short-bdat-not-stored", "smoke-short@example.test" not in mailboxes())

    with smtplib.SMTP("127.0.0.1", args.smtp_port, local_hostname="smoke.example.test", timeout=10) as smtp:
        smtp.ehlo()
        status, _ = smtp.mail("sender@example.test", options=["SIZE=26214401"])
        check("declared-smtp-size-limit", status == 552)

    before_restart = mailboxes()
    start = time.monotonic()
    subprocess.run(["docker", "restart", "--timeout", "15", args.container], check=True, stdout=subprocess.DEVNULL)
    ready()
    check("container-restart-persistence", mailboxes() == before_restart
          and request(f"/api/messages/{mid}/source")[0] == raw
          and request(f"/api/attachments/{attachments['note.txt']['id']}")[0] == text_attachment
          and api(f"/api/mailboxes/{calendar_box['id']}/events")["events"][0]["status"] == "CANCELLED",
          elapsed_seconds=round(time.monotonic() - start, 3))
    subprocess.run(["docker", "exec", args.container, "/hoomail", "healthcheck"], check=True)
    api("/api/messages/actions", {"action": "delete", "ids": [mid]})
    try:
        request(f"/api/attachments/{attachments['note.txt']['id']}")
    except urllib.error.HTTPError as error:
        check("message-delete-cascades-attachment", error.code == 404)
    else:
        raise AssertionError("Attachment survived message deletion")
    api("/api/reset", {})
    check("final-reset", mailboxes() == {})
    result = {"status": "pass", "container": args.container, "checks": checks}
    encoded = json.dumps(result, indent=2) + "\n"
    if args.output:
        args.output.write_text(encoded)
    print(encoded, end="")


if __name__ == "__main__":
    main()
