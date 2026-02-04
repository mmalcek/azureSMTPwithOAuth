#!/usr/bin/env python3
"""
Test emails for azureSMTPwithOAuth.
Sends various email formats including edge cases to verify relay functionality.

Usage:
    python3 test_emails.py <sender@domain.com> <recipient@domain.com> [host] [port]

Examples:
    python3 test_emails.py sender@example.com recipient@example.com
    python3 test_emails.py sender@example.com recipient@example.com 127.0.0.1 2526

Requires a running azureSMTPwithOAuth instance with valid OAuth2 credentials
and fallback_smtp_user/pass configured in config.yaml.
"""

import smtplib
import base64
import sys
import time

# Defaults (overridden by CLI args)
SMTP_HOST = "127.0.0.1"
SMTP_PORT = 2526


def send(label, msg_bytes, sender, recipient, host, port):
    """Send a raw message and print result."""
    try:
        server = smtplib.SMTP(host, port, timeout=30)
        server.ehlo()
        server.login("", "")  # Uses fallback credentials from config
        server.sendmail(sender, [recipient], msg_bytes)
        server.quit()
        print(f"  [OK] {label}")
    except Exception as e:
        print(f"  [FAIL] {label}: {e}")


def test1_plain_text(sender, recipient, host, port):
    """Simple plain text email."""
    msg = (
        f"From: {sender}\r\n"
        f"To: {recipient}\r\n"
        f"Subject: [Test 1] Plain text email\r\n"
        f"\r\n"
        f"This is a simple plain text test email from azureSMTPwithOAuth.\r\n"
        f"If you received this, basic email sending works correctly.\r\n"
    )
    send("Test 1 - Plain text", msg, sender, recipient, host, port)


def test2_html(sender, recipient, host, port):
    """HTML email."""
    msg = (
        f"From: {sender}\r\n"
        f"To: {recipient}\r\n"
        f"Subject: [Test 2] HTML email\r\n"
        f"Content-Type: text/html; charset=utf-8\r\n"
        f"\r\n"
        f"<html><body>"
        f"<h2 style='color: #2e6da4;'>azureSMTPwithOAuth Test</h2>"
        f"<p>This is an <strong>HTML</strong> email with:</p>"
        f"<ul>"
        f"<li>Bold text</li>"
        f"<li>A list</li>"
        f"<li>Special chars: &amp; &lt; &gt; &quot;</li>"
        f"</ul>"
        f"<p style='color: green;'>If you see this formatted, HTML rendering works.</p>"
        f"</body></html>\r\n"
    )
    send("Test 2 - HTML", msg, sender, recipient, host, port)


def test3_utf8_subject(sender, recipient, host, port):
    """Email with RFC 2047 encoded subject (UTF-8)."""
    subject_text = "Test 3 - Unicode subject test"
    encoded_subject = "=?UTF-8?B?" + base64.b64encode(subject_text.encode("utf-8")).decode() + "?="
    msg = (
        f"From: {sender}\r\n"
        f"To: {recipient}\r\n"
        f"Subject: {encoded_subject}\r\n"
        f"Content-Type: text/plain; charset=utf-8\r\n"
        f"Content-Transfer-Encoding: base64\r\n"
        f"\r\n"
        + base64.b64encode("Body with UTF-8 content encoded in base64.".encode("utf-8")).decode()
        + "\r\n"
    )
    send("Test 3 - UTF-8 encoded subject + base64 body", msg, sender, recipient, host, port)


def test4_multipart_alternative(sender, recipient, host, port):
    """Multipart/alternative with both text and HTML parts."""
    boundary = "----=_Part_TEST4_BOUNDARY"
    msg = (
        f"From: {sender}\r\n"
        f"To: {recipient}\r\n"
        f"Subject: [Test 4] Multipart alternative\r\n"
        f"MIME-Version: 1.0\r\n"
        f"Content-Type: multipart/alternative; boundary=\"{boundary}\"\r\n"
        f"\r\n"
        f"--{boundary}\r\n"
        f"Content-Type: text/plain; charset=utf-8\r\n"
        f"Content-Transfer-Encoding: 7bit\r\n"
        f"\r\n"
        f"This is the PLAIN TEXT version.\r\n"
        f"You should NOT see this if your client supports HTML.\r\n"
        f"\r\n"
        f"--{boundary}\r\n"
        f"Content-Type: text/html; charset=utf-8\r\n"
        f"Content-Transfer-Encoding: 7bit\r\n"
        f"\r\n"
        f"<html><body><h3>Multipart Alternative Test</h3>"
        f"<p>This is the <em>HTML version</em>. The relay should prefer this over plain text.</p>"
        f"</body></html>\r\n"
        f"\r\n"
        f"--{boundary}--\r\n"
    )
    send("Test 4 - Multipart alternative (text+HTML)", msg, sender, recipient, host, port)


def test5_attachment(sender, recipient, host, port):
    """Email with a text file attachment."""
    boundary = "----=_Part_TEST5_BOUNDARY"
    file_content = base64.b64encode(b"This is the content of the attached file.\nLine 2.\nLine 3.").decode()
    msg = (
        f"From: {sender}\r\n"
        f"To: {recipient}\r\n"
        f"Subject: [Test 5] Email with attachment\r\n"
        f"MIME-Version: 1.0\r\n"
        f"Content-Type: multipart/mixed; boundary=\"{boundary}\"\r\n"
        f"\r\n"
        f"--{boundary}\r\n"
        f"Content-Type: text/plain; charset=utf-8\r\n"
        f"Content-Transfer-Encoding: 7bit\r\n"
        f"\r\n"
        f"This email has a text file attachment (test_file.txt).\r\n"
        f"\r\n"
        f"--{boundary}\r\n"
        f"Content-Type: text/plain; name=\"test_file.txt\"\r\n"
        f"Content-Disposition: attachment; filename=\"test_file.txt\"\r\n"
        f"Content-Transfer-Encoding: base64\r\n"
        f"\r\n"
        f"{file_content}\r\n"
        f"\r\n"
        f"--{boundary}--\r\n"
    )
    send("Test 5 - Attachment", msg, sender, recipient, host, port)


def test6_quoted_printable(sender, recipient, host, port):
    """Email with quoted-printable encoding (common in legacy systems)."""
    msg = (
        f"From: {sender}\r\n"
        f"To: {recipient}\r\n"
        f"Subject: [Test 6] Quoted-printable encoding\r\n"
        f"Content-Type: text/plain; charset=utf-8\r\n"
        f"Content-Transfer-Encoding: quoted-printable\r\n"
        f"\r\n"
        f"This email uses quoted-printable encoding.=0D=0A"
        f"Special chars: =C3=A9=C3=A8=C3=AA (accented e variants)=0D=0A"
        f"Long line that should be soft-wrapped with an equals sign at the end of =\r\n"
        f"the line to test proper QP decoding.\r\n"
    )
    send("Test 6 - Quoted-printable", msg, sender, recipient, host, port)


def test7_cc_bcc(sender, recipient, host, port):
    """Email with CC header (tests CC/BCC parsing and deduplication)."""
    msg = (
        f"From: {sender}\r\n"
        f"To: {recipient}\r\n"
        f"Cc: {recipient}\r\n"
        f"Subject: [Test 7] CC header test\r\n"
        f"\r\n"
        f"This email has a CC header set to the same recipient.\r\n"
        f"Tests that CC addresses are parsed and deduplicated from To recipients.\r\n"
    )
    send("Test 7 - CC header", msg, sender, recipient, host, port)


def test8_missing_content_type(sender, recipient, host, port):
    """Email with no Content-Type header (edge case - should default to text)."""
    msg = (
        f"From: {sender}\r\n"
        f"To: {recipient}\r\n"
        f"Subject: [Test 8] No Content-Type header\r\n"
        f"\r\n"
        f"This email has no Content-Type header at all.\r\n"
        f"The relay should handle this gracefully and treat it as plain text.\r\n"
    )
    send("Test 8 - Missing Content-Type", msg, sender, recipient, host, port)


def test9_long_subject(sender, recipient, host, port):
    """Email with a very long subject line."""
    long_part = "word " * 50  # 250 chars
    msg = (
        f"From: {sender}\r\n"
        f"To: {recipient}\r\n"
        f"Subject: [Test 9] Long subject: {long_part.strip()}\r\n"
        f"\r\n"
        f"This email has a very long subject line to test handling of oversized headers.\r\n"
    )
    send("Test 9 - Long subject", msg, sender, recipient, host, port)


def test10_empty_body(sender, recipient, host, port):
    """Email with an empty body."""
    msg = (
        f"From: {sender}\r\n"
        f"To: {recipient}\r\n"
        f"Subject: [Test 10] Empty body\r\n"
        f"\r\n"
    )
    send("Test 10 - Empty body", msg, sender, recipient, host, port)


if __name__ == "__main__":
    if len(sys.argv) < 3:
        print("Usage: python3 test_emails.py <sender@domain.com> <recipient@domain.com> [host] [port]")
        print("Example: python3 test_emails.py sender@example.com recipient@example.com 127.0.0.1 2526")
        sys.exit(1)

    sender = sys.argv[1]
    recipient = sys.argv[2]
    host = sys.argv[3] if len(sys.argv) > 3 else SMTP_HOST
    port = int(sys.argv[4]) if len(sys.argv) > 4 else SMTP_PORT

    print(f"Sending test emails via {host}:{port}")
    print(f"From: {sender} -> To: {recipient}\n")

    tests = [
        test1_plain_text,
        test2_html,
        test3_utf8_subject,
        test4_multipart_alternative,
        test5_attachment,
        test6_quoted_printable,
        test7_cc_bcc,
        test8_missing_content_type,
        test9_long_subject,
        test10_empty_body,
    ]

    for i, test_fn in enumerate(tests):
        test_fn(sender, recipient, host, port)
        if i < len(tests) - 1:
            time.sleep(1)  # Small delay between sends

    print(f"\nDone! Check {recipient} inbox.")
