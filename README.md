# azureSMTPwithOAuth

SMTP relay service that receives E-mails from SMTP clients and sends them to Office 365 using OAuth2 authentication/Graph API.

**[DOWNLOAD](https://github.com/mmalcek/azureSMTPwithOAuth/releases/latest)** latest prebuilt release (Windows, Mac, Linux)

## Motivation

- From September 2025, Microsoft will require all SMTP clients to use OAuth2 authentication for sending emails to Office 365. This service provides a simple way to relay emails from SMTP clients to Office 365 using OAuth2 authentication.
- This is useful for applications that need to send emails but do not support OAuth2 authentication natively, such as legacy applications or custom SMTP clients.
- I created this application for our ([systems@work](https://systemsatwork.com)) internal use, but we decided to share it with the community as it may be useful for others as well.

**If you like this app, you can buy me a coffee ;)**

[![Ko-fi](https://ko-fi.com/img/githubbutton_sm.svg)](https://ko-fi.com/mmalcek)

## Features

- SMTP relay service
- OAuth2 authentication
- Graph API integration
- Token cache and renewal. Tokens are stored in memory and renewed automatically.
- Supports AUTH LOGIN and AUTH PLAIN authentication methods
- Supports multiple SMTP clients
- Also works with the "Exchange Online Kiosk" plan, which does not support SMTP OAuth authentication (thanks to Graph API)

### Stability Features (v1.1.0)

- **Retry logic** - Automatic retry with exponential backoff for transient Graph API failures (429, 500, 502, 503, 504)
- **Connection pooling** - HTTP connection reuse for better performance
- **Graceful shutdown** - Waits for in-flight messages to complete before stopping
- **Connection limits** - Configurable maximum concurrent connections to prevent resource exhaustion
- **Message size limits** - Configurable maximum message size (default 25MB per Graph API limit)
- **Timeouts** - Configurable connection and OAuth2 timeouts
- **Token cache cleanup** - Automatic cleanup of expired tokens to prevent memory leaks
- **Panic recovery** - Service continues running even if a handler encounters an unexpected error
- **Input validation** - Email address validation and command line length limits
- **Malformed email handling** - Gracefully handles non-standard MIME structures from legacy applications

## Important

- This is an SMTP relay ONLY! (No IMAP/POP3 support)
- This is not a full email server; it does not store emails, it only relays them to Office 365.
- SMTP Encryption (StartTLS, TLS) **is not supported!** It is highly recommended to run this service on the same machine as your SMTP client and set up `listen_addr:127.0.0.1:XXX`. Communication with Office 365 is of course encrypted using HTTPS.

## Quick Step By Step Summary

1. Register an application in Azure Entra ID (Azure AD) and configure it for OAuth2 authentication.
2. Update `config.yaml` with your Azure App Client ID, Client Secret, and Tenant ID.
3. Optionally encrypt the config file (Windows only).
4. Install the service using the command line.
5. Start the service.
6. Configure your SMTP client to use the service as a relay.

More detailed instructions are provided below.

## Setup Entra ID (Azure AD) Application

- See quick guide **azureSMTPwithOAuth_RegisterApp.docx**

## Config file

```yaml
log: ""
log_level: info
listen_addr: 127.0.0.1:2526
oauth2_config:
  client_id: AzureAppClientID
  client_secret: AzureAppClientSecret
  tenant_id: AzureTenantID
  scopes:
    - https://graph.microsoft.com/.default
fallback_smtp_user:
fallback_smtp_pass:
save_to_sent: false

# Stability configuration (optional - all have sensible defaults)
max_message_size: 26214400      # Max email size in bytes (default: 25MB)
max_connections: 100            # Max concurrent connections (default: 100)
connection_timeout: 300         # Connection timeout in seconds (default: 300)
strict_attachments: false       # Fail if attachment decode fails (default: false)
retry_attempts: 3               # Graph API retry attempts (default: 3)
retry_initial_delay: 500        # Initial retry delay in ms (default: 500)
```

### Basic Configuration

- `log`: Path to log file. If empty, logs will be printed to stdout.
- `log_level`: Log level. Can be `debug`, `info`, `warn`, or `error`.
- `listen_addr`: Address to listen on. Default is `127.0.0.1:2526`.
- `oauth2_config`: OAuth2 configuration.
  - `client_id`: Azure App Client ID.
  - `client_secret`: Azure App Client Secret.
  - `tenant_id`: Azure Tenant ID.
  - `scopes`: Scopes to request. Default is `https://graph.microsoft.com/.default`.
- `fallback_smtp_user`: Fallback SMTP user. If set, this user will be used if the SMTP client does not provide a user.
- `fallback_smtp_pass`: Fallback SMTP password. If set, this password will be used if the SMTP client does not provide a password.
- `save_to_sent`: If true, the service will save a copy of the sent email to the "Sent Items" folder in Office 365. Default is `false`.

### Stability Configuration (v1.1.0)

All stability options have sensible defaults and are optional. Existing config files will work without changes.

- `max_message_size`: Maximum email size in bytes. Default is `26214400` (25MB), which is the Graph API limit.
- `max_connections`: Maximum concurrent SMTP connections. Default is `100`. Connections beyond this limit receive a `421` temporary error.
- `connection_timeout`: Overall connection timeout in seconds. Default is `300` (5 minutes).
- `strict_attachments`: If `true`, the service will reject emails if any attachment fails to decode. If `false` (default), failed attachments are skipped with a warning.
- `retry_attempts`: Number of retry attempts for Graph API calls on transient failures. Default is `3`.
- `retry_initial_delay`: Initial delay in milliseconds before first retry. Uses exponential backoff with jitter. Default is `500`.

## Usage

### Run from command line

- If you start the application from the command line without any arguments, it will run as a console application. If config.yaml: `log: ""` is empty, you can watch logs in the console.

### Setup as service

- `.\azureSMTPwithOAuth.exe -service install`: Install the service.
- `.\azureSMTPwithOAuth.exe -service start`: Start the service.
- `.\azureSMTPwithOAuth.exe -service stop`: Stop the service.
- `.\azureSMTPwithOAuth.exe -service uninstall`: Uninstall the service.

### Other commands

- `.\azureSMTPwithOAuth.exe -encrypt`: Encrypt sensitive information in the config file using DPAPI. Windows only.

### Configure SMTP Client/your application

- Set the SMTP server to the address and port specified in `listen_addr` (default is `127.0.0.1:2526`).
- StartTLS is not supported, so ensure your SMTP client is configured to connect without encryption.
- If the client provides a username and password, they will be used for authentication. If not, the `fallback_smtp_user` and password will be used.

## Changelog

### v1.1.2

- **Bug fix:** Fixed double-close on HTTP response body when Graph API retries are exhausted on retryable status codes
- **Bug fix:** Added RFC 5321 §4.5.2 dot-destuffing in SMTP DATA phase — messages containing lines starting with `.` are no longer corrupted
- **Feature:** Added AUTH PLAIN support (in addition to AUTH LOGIN) for broader SMTP client compatibility

### v1.1.1

- **Security:** Credentials are no longer logged at DEBUG level during AUTH flow
- **Security:** Graph API URL now URL-encodes the sender to prevent path injection
- **Security:** Internal error details are no longer exposed to SMTP clients
- **Security:** OAuth2 token response body is no longer included in error messages
- **Bug fix:** Replaced `goto` control flow with proper `continue` after message-too-large rejection
- **Bug fix:** Guard against zero retry attempts causing nil pointer panic
- **Bug fix:** MAIL FROM address is now validated with the same rules as RCPT TO
- **Bug fix:** Token cache expiry now has a minimum of 30 seconds to prevent constant re-fetching
- **Bug fix:** Token cache cleanup goroutine now stops on service shutdown
- **Improvement:** Added recipient limit (max 500 per message, matching Graph API limit)
- **Improvement:** AUTH LOGIN now accepts base64 without padding for broader client compatibility
- **Improvement:** Replaced `log.Fatalf` in goroutine with graceful error logging
- **Improvement:** Client disconnections are now logged at DEBUG instead of ERROR level
- **Improvement:** `extractAddress` now strips SMTP parameters (e.g., `SIZE=12345`) in fallback path
- **Bug fix:** Fixed Graph API error when all recipients are in CC/BCC (nil `toRecipients`)
- **Cleanup:** Removed unused `health_addr` config field
- **Cleanup:** Updated `golang.org/x/sys` dependency from 2020 to v0.40.0
- Added new unit tests for base64 decoding, address extraction, and email validation

### v1.1.0

- Added retry logic with exponential backoff for Graph API transient failures
- Added HTTP connection pooling for improved performance
- Added graceful shutdown with connection draining
- Added configurable connection limits
- Added configurable message size limits
- Added configurable timeouts for connections and OAuth2 requests
- Added automatic token cache cleanup
- Added panic recovery to prevent service crashes
- Added input validation (email format, command line length)
- Improved handling of malformed MIME messages from legacy applications
- Fixed potential hang on malformed multipart boundaries
- Removed STARTTLS advertisement (was not implemented)
- Added RSET and NOOP command support

### v1.0.0

- Initial release
