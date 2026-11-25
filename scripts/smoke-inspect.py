#!/usr/bin/env python3
"""Compare a Hoomail binary with independently generated mail and known outcomes.

Run from the checkout: python3 scripts/smoke-inspect.py /path/to/hoomail
Starts and stops a fresh process with a temporary database. No account is needed.
"""

import collections
import contextlib
import hashlib
import json
import math
import os
from pathlib import Path
import smtplib
import socket
import subprocess
import sys
import tempfile
import time
import urllib.request
from email import policy
from email.message import EmailMessage
from email.parser import BytesParser


def message(name):
    m = EmailMessage(policy=policy.SMTP)
    m["Date"] = "Wed, 25 Nov 2026 11:30:00 +0100"
    m["From"] = "Author <author@example.test>"
    m["To"] = name + "@example.test"
    m["Message-ID"] = "<" + name + "@example.test>"
    m["Subject"] = name
    return m


def corpus():
    plain = message("delegated")
    plain["Sender"] = "assistant@example.test"
    plain["Authentication-Results"] = "mx.example.test; none"
    plain.set_content("Quarterly update: https://example.test/news")
    yield plain

    newsletter = message("newsletter")
    newsletter["List-Unsubscribe"] = "<mailto:leave@example.test>, <https://example.test/leave>"
    newsletter["List-Unsubscribe-Post"] = "List-Unsubscribe=One-Click"
    newsletter["DKIM-Signature"] = ("v=1; a=rsa-sha256; d=example.test; s=list; "
        "h=from:list-unsubscribe:list-unsubscribe-post; bh=YWJj; b=ZGVm")
    newsletter.set_content("Read the newsletter.")
    newsletter.add_alternative(
        '<html lang="en"><body><a id="top"></a><a href="#top" title="Back to top"></a>\n'
        '<a href="http://example.test/news">News</a>\n'
        '<img src="https://example.test/logo.svg?edition=3" alt="Logo" style="opacity:0.5">\n'
        '<img src="https://example.test/open" width="1" height="1" alt="">\n'
        '<div style="display: grid; grid-template-columns: 1fr 1fr">Columns</div></body></html>',
        subtype="html")
    yield newsletter

    invalid = message("bad-headers")
    invalid.replace_header("From", "a@example.test, b@example.test")
    invalid["List-Unsubscribe"] = "<mailto:leave@example.test>"
    invalid["List-Unsubscribe-Post"] = "List-Unsubscribe=One-Click"
    invalid.set_content("Missing required Sender and HTTPS unsubscribe URI.")
    yield invalid

    files = message("attachments")
    files.add_attachment(b"one\x00", maintype="application", subtype="octet-stream", filename="one.bin")
    files.add_attachment(b"two\xff", maintype="application", subtype="octet-stream", filename="two.bin")
    yield files

    charset = message("unknown-charset")
    charset.set_content("Some text")
    charset.set_param("charset", "x-unregistered-example", replace=True)
    yield charset


def ports():
    with contextlib.ExitStack() as stack:
        sockets = [stack.enter_context(socket.socket()) for _ in range(3)]
        for sock in sockets:
            sock.bind(("127.0.0.1", 0))
        return [sock.getsockname()[1] for sock in sockets]


def run(binary):
    passed, failed = [], []
    def check(name, condition):
        (passed if condition else failed).append(name)

    dataset = json.loads((Path(__file__).resolve().parents[1] / "internal/inspect/caniemail-data.json").read_bytes())
    features = {f["slug"]: f for f in dataset["data"]}
    http, smtp, pop = ports()
    opener = urllib.request.build_opener(urllib.request.ProxyHandler({}))
    def get(path):
        with opener.open(f"http://127.0.0.1:{http}" + path, timeout=10) as response:
            return response.read()
    def api(path):
        return json.loads(get(path))

    with tempfile.TemporaryDirectory(prefix="hoomail-inspect-") as temp:
        with subprocess.Popen([binary], cwd=temp, stdout=subprocess.DEVNULL, stderr=subprocess.PIPE,
                env={**os.environ, "PORT": str(http), "HOOMAIL_SMTP_PORT": str(smtp),
                     "HOOMAIL_POP3_PORT": str(pop), "HOOMAIL_DB_PATH": str(Path(temp) / "inbox.db")}) as server:
            try:
                deadline = time.monotonic() + 10
                while server.poll() is None:
                    try:
                        api("/api/mailboxes")
                        break
                    except OSError:
                        if time.monotonic() >= deadline:
                            raise RuntimeError("temporary server did not become ready")
                        time.sleep(0.05)
                if server.poll() is not None:
                    raise RuntimeError(server.stderr.read().decode())
                for msg in corpus():
                    name = str(msg["Subject"])
                    raw = msg.as_bytes()
                    with smtplib.SMTP("127.0.0.1", smtp, local_hostname="smoke.example.test", timeout=10) as client:
                        client.sendmail("author@example.test", str(msg["To"]), raw)
                    box = next(b for b in api("/api/mailboxes")["mailboxes"] if b["address"] == str(msg["To"]))
                    mid = api(f"/api/mailboxes/{box['id']}/messages")["messages"][0]["id"]
                    prefix = f"/api/messages/{mid}"
                    report_bytes = get(prefix + "/inspect")
                    report = json.loads(report_bytes)
                    cases = lambda label, ok: check(f"{name}: {label}", ok)
                    cases("source identical", get(prefix + "/source") == raw)
                    cases("deterministic report bytes", report_bytes == get(prefix + "/inspect"))
                    boxes = api("/api/mailboxes")["mailboxes"]
                    cases("inspection preserves unread", next(b["unread_count"] for b in boxes if b["id"] == box["id"]) == 1)
                    findings = {f["id"]: f for f in report["findings"]}
                    def verdict(rule):
                        f = findings.get(rule, {})
                        return f.get("outcome"), f.get("severity")
                    buckets = collections.Counter()
                    for f in report["findings"]:
                        bucket = {"not-evaluated": "notEvaluated"}.get(f["outcome"], f["outcome"])
                        if bucket == "pass" and f["severity"] != "none":
                            bucket = f["severity"]
                        buckets[bucket] += 1
                    cases("summary matches findings", collections.Counter(report["summary"]) == buckets)
                    cases("unique finding IDs", len(findings) == len(report["findings"]))
                    if name == "delegated":
                        cases("different author and sender", verdict("message.sender") == ("pass", "none"))
                        cases("none authentication assertion", verdict("authentication.results.1") == ("observed", "none"))
                        cases("no HTML compatibility on plain mail", report["htmlCompatibility"] is None)
                        cases("plain text URL", [r["url"] for r in report["resources"]] == ["https://example.test/news"])
                    elif name == "newsletter":
                        cases("valid one-click pair", verdict("unsubscribe.one-click-pairing") == ("pass", "none"))
                        cases("DKIM coverage is observation", verdict("unsubscribe.dkim-coverage") == ("observed", "none"))
                        cases("HTML and text selected", verdict("content.alternative") == ("pass", "none"))
                        cases("all images have alt", verdict("content.image-alt") == ("pass", "none"))
                        cases("title names link; bookmark ignored", verdict("content.link-name") == ("pass", "none"))
                        cases("HTTP link advisory", verdict("privacy.insecure-links") == ("pass", "advisory"))
                        res = [(r["kind"], r["url"]) for r in report["resources"]]
                        cases("only the beacon is a tracking candidate", [u for k, u in res if k == "tracking-pixel"] == ["https://example.test/open"])
                        warnings = {w["slug"]: w for w in report["htmlCompatibility"]["warnings"]}
                        for slug in ("css-display-grid", "html-anchor-links", "css-grid-template", "image-svg"):
                            w = warnings.get(slug)
                            cases(slug + " detected once", bool(w and w["occurrences"] == 1))
                            oracle = {(family, platform, version): {"y": "yes", "a": "partial", "n": "no", "u": "unknown"}[value.split()[0]]
                                      for family, pp in features[slug]["stats"].items() for platform, vv in pp.items() for version, value in vv.items()}
                            actual = {(c["family"], c["platform"], c["version"]): c["support"] for c in w["clients"]} if w else {}
                            cases(slug + " every client matches snapshot", actual == oracle)
                            counts = collections.Counter(oracle.values())
                            n = len(oracle) - counts["unknown"]
                            cases(slug + " percentages exclude unknown", bool(w and all(math.isclose(w["score"][key], 100 * counts[k] / n, abs_tol=1e-8)
                                for k, key in (("yes", "supported"), ("no", "unsupported"), ("partial", "partial")))))
                    elif name == "bad-headers":
                        cases("two authors require Sender", verdict("message.sender") == ("fail", "error"))
                        cases("mailto alone cannot support one-click", verdict("unsubscribe.one-click-pairing") == ("fail", "error"))
                    elif name == "attachments":
                        parsed = BytesParser(policy=policy.default).parsebytes(raw)
                        parts = list(parsed.iter_attachments())
                        children = report["mimeTree"]["children"]
                        cases("MIME hashes and sizes agree with Python", len(parts) == len(children) and all(
                            c["filename"] == p.get_filename() and c["decodedSize"] == len(p.get_payload(decode=True)) and
                            c["checksums"]["sha256"] == hashlib.sha256(p.get_payload(decode=True)).hexdigest()
                            for c, p in zip(children, parts)))
                        cases("one occurrence per attachment", [(r["text"], r["occurrenceCount"]) for r in report["resources"]] == [("one.bin", 1), ("two.bin", 1)])
                    else:
                        cases("unknown charset does not mean empty", verdict("content.body") == ("not-evaluated", "none"))
                        cases("no checksum for undecoded body", report["mimeTree"]["decodedSize"] is None and "checksums" not in report["mimeTree"])
            finally:
                server.terminate()
    for name in failed:
        print("FAIL", name)
    print(f"{len(passed)}/{len(passed) + len(failed)} checks passed on five SMTP messages.")
    return bool(failed)


if __name__ == "__main__":
    if len(sys.argv) != 2:
        sys.exit(__doc__)
    sys.exit(run(str(Path(sys.argv[1]).resolve())))
