# CLAUDE.md

## Build & Test

```bash
go build -o azureSMTPwithOAuth .
go test -v ./...
go vet ./...
```

## Run locally

```bash
./azureSMTPwithOAuth
# Listens on address from config.yaml (default 127.0.0.1:2526)
```

## CLI flags

```bash
-service install|start|stop|uninstall  # Manage as OS service (kardianos/service)
-encrypt                               # Encrypt config.yaml sensitive fields (Windows DPAPI only)
```

## Send test email

Requires valid config.yaml with OAuth2 credentials and fallback_smtp_user/pass.

```python
import smtplib

server = smtplib.SMTP('127.0.0.1', 2526, timeout=30)
server.ehlo()
server.login('', '')  # Empty = uses fallback credentials from config
server.sendmail('sender@domain.com', ['recipient@example.com'], """From: sender@domain.com
To: recipient@example.com
Subject: Test email

Hello from azureSMTPwithOAuth!
""")
server.quit()
```

### Anonymous send (allow_anonymous: true)

When `allow_anonymous: true` and fallback credentials are configured, clients can send without AUTH:

```python
import smtplib

server = smtplib.SMTP('127.0.0.1', 2526, timeout=30)
server.ehlo()
# No login needed - uses fallback credentials automatically
server.sendmail('sender@domain.com', ['recipient@example.com'], """From: sender@domain.com
To: recipient@example.com
Subject: Anonymous test

Hello from anonymous device!
""")
server.quit()
```

Or with PowerShell (no `-Credential` needed):

```powershell
Send-MailMessage -From 'sender@domain.com' -To 'recipient@example.com' -Subject 'Test' -Body 'Hello' -SmtpServer '127.0.0.1' -Port 2526
```

### HTML email

Set `Content-Type: text/html` header in the message body.

### Email with attachment

Use `Content-Type: multipart/mixed` with proper MIME boundaries and base64-encoded attachment parts.

## Project structure

- `main.go` - Service lifecycle, TCP listener, connection semaphore, graceful shutdown (30s timeout)
- `smtp.go` - Core SMTP protocol handler, AUTH LOGIN flow, MIME parsing (`parseSubjectBodyAndAttachments`), Graph API sender (`sendMailGraphAPI`), OAuth2 token management with singleflight dedup and sync.Map cache, retry with exponential backoff
- `config.go` - YAML config loading, default value initialization, slog-based logging setup
- `smtp_test.go` - Unit tests for MIME parsing and content decoding (base64, quoted-printable, multipart, encoded subjects)
- `flags.go` - `-encrypt` and `-service` flag processing
- `cryptWindows.go` - Windows DPAPI encryption/decryption of sensitive config fields (prefix: `__SYSTEMENCRYPTED__`)
- `cryptNonWindows.go` - Stubs that fatal on encrypt, no-op on decrypt

## Architecture

```
SMTP Client → [AUTH LOGIN] → SMTP Relay → [OAuth2 ROPC token] → Microsoft Graph API /sendMail
```

- Connections gated by buffered channel semaphore (`max_connections`, default 100)
- Each connection runs in a goroutine tracked by sync.WaitGroup
- Token caching per user in sync.Map with singleflight deduplication
- Token cache cleanup goroutine runs every 5 minutes
- Retry on Graph API 429/5xx with exponential backoff + jitter (max 10s)
- Panic recovery per connection with stack trace logging

## Configuration (config.yaml)

### Required

```yaml
oauth2_config:
  client_id: ""
  client_secret: ""
  tenant_id: ""
  scopes: ["https://graph.microsoft.com/.default"]
```

### Optional (with defaults)

```yaml
listen_addr: "127.0.0.1:2526"
log: ""                        # Empty = stdout
log_level: "info"              # debug, info, warn, error
fallback_smtp_user: ""         # Used when client sends empty credentials
fallback_smtp_pass: ""
allow_anonymous: false         # Allow unauthenticated clients (uses fallback credentials)
save_to_sent: false            # Save to Office 365 Sent Items
max_message_size: 26214400     # 25MB
max_connections: 100
connection_timeout: 300        # Seconds per connection
strict_attachments: false      # true = reject email on attachment decode error
retry_attempts: 3
retry_initial_delay: 500       # Milliseconds
```

## Key SMTP response codes used

- `220` Service ready, `221` Closing, `235` Auth success
- `250` OK, `334` Auth challenge, `354` Start data
- `421` Service unavailable (capacity/timeout/error)
- `501`/`502`/`503` Protocol errors, `530` Auth required
- `535` Auth failed, `550` Message rejected, `552` Too large, `553` Invalid recipient

## Dependencies

- `github.com/kardianos/service` - Cross-platform service management
- `github.com/pkg/errors` - Error wrapping
- `golang.org/x/sync` - Singleflight for token dedup
- `golang.org/x/sys` - Windows DPAPI syscalls
- `gopkg.in/yaml.v3` - Config parsing

## Test coverage

Tests cover MIME parsing, content decoding, and anonymous access:
- Base64, quoted-printable, and plain text decoding
- Simple text and HTML email parsing
- Multipart messages with/without attachments
- RFC 2047 encoded subjects (e.g., UTF-8/base64)
- Anonymous SMTP access (allowed, denied, denied without fallback credentials)

Not covered: OAuth2 flow, Graph API integration, service lifecycle.
