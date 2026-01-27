package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"math/rand"
	"net"
	"net/http"
	"net/mail"
	"net/url"
	"runtime/debug"
	"strings"
	"sync"
	"time"

	"mime"
	"mime/multipart"
	"mime/quotedprintable"

	"golang.org/x/sync/singleflight"
)

// TokenCache holds cached OAuth2 tokens per user (thread-safe)
var TokenCache sync.Map

// tokenFetchGroup prevents duplicate concurrent token fetches for same user
var tokenFetchGroup singleflight.Group

type cachedToken struct {
	token     string
	expiresAt time.Time
}

// Shared HTTP clients with connection pooling for better performance
var (
	// graphHTTPClient is used for Microsoft Graph API calls
	graphHTTPClient = &http.Client{
		Timeout: 60 * time.Second, // Increased for large attachments
		Transport: &http.Transport{
			MaxIdleConns:        100,
			MaxIdleConnsPerHost: 10,
			IdleConnTimeout:     90 * time.Second,
			DisableCompression:  false,
		},
	}

	// authHTTPClient is used for OAuth2 token requests
	authHTTPClient = &http.Client{
		Timeout: 30 * time.Second,
		Transport: &http.Transport{
			MaxIdleConns:        20,
			MaxIdleConnsPerHost: 5,
			IdleConnTimeout:     90 * time.Second,
		},
	}
)

// RetryConfig holds configuration for HTTP retry logic
type RetryConfig struct {
	MaxAttempts     int
	InitialBackoff  time.Duration
	MaxBackoff      time.Duration
	RetryableStatus []int
}

// getRetryConfig returns retry configuration based on config settings
func getRetryConfig() RetryConfig {
	return RetryConfig{
		MaxAttempts:     config.RetryAttempts,
		InitialBackoff:  time.Duration(config.RetryInitialDelay) * time.Millisecond,
		MaxBackoff:      10 * time.Second,
		RetryableStatus: []int{429, 500, 502, 503, 504},
	}
}

// isRetryableStatus checks if HTTP status code should trigger a retry
func isRetryableStatus(status int, retryable []int) bool {
	for _, s := range retryable {
		if status == s {
			return true
		}
	}
	return false
}

// doWithRetry executes HTTP request with exponential backoff retry
func doWithRetry(ctx context.Context, client *http.Client, req *http.Request, jsonBody []byte, cfg RetryConfig) (*http.Response, error) {
	var lastErr error
	var resp *http.Response

	for attempt := 0; attempt < cfg.MaxAttempts; attempt++ {
		if attempt > 0 {
			backoff := cfg.InitialBackoff * time.Duration(1<<uint(attempt-1))
			if backoff > cfg.MaxBackoff {
				backoff = cfg.MaxBackoff
			}
			// Add jitter (0-25% of backoff)
			jitter := time.Duration(rand.Int63n(int64(backoff / 4)))
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(backoff + jitter):
			}
			logger.Debug("Retrying Graph API call", "attempt", attempt+1, "backoff_ms", (backoff+jitter).Milliseconds())
		}

		// Create new request for each attempt (body needs fresh reader)
		reqCopy, err := http.NewRequestWithContext(ctx, req.Method, req.URL.String(), bytes.NewReader(jsonBody))
		if err != nil {
			return nil, fmt.Errorf("failed to create request: %w", err)
		}
		for key, values := range req.Header {
			for _, value := range values {
				reqCopy.Header.Add(key, value)
			}
		}

		resp, lastErr = client.Do(reqCopy)
		if lastErr != nil {
			logger.Debug("HTTP request failed", "attempt", attempt+1, "error", lastErr)
			continue // Network error, retry
		}

		if !isRetryableStatus(resp.StatusCode, cfg.RetryableStatus) {
			return resp, nil // Success or non-retryable error
		}

		logger.Debug("Retryable status received", "attempt", attempt+1, "status", resp.StatusCode)
		resp.Body.Close() // Close before retry
		lastErr = fmt.Errorf("retryable status: %d", resp.StatusCode)
	}

	return resp, lastErr
}

// handleSMTPConnection handles a single SMTP connection
func handleSMTPConnection(conn net.Conn) {
	// Panic recovery to prevent service crash
	defer func() {
		if r := recover(); r != nil {
			logger.Error("Panic recovered in SMTP handler",
				"panic", r,
				"stack", string(debug.Stack()),
				"remote", conn.RemoteAddr())
		}
		conn.Close()
	}()

	// Set connection timeout
	timeout := time.Duration(config.ConnectionTimeout) * time.Second
	conn.SetDeadline(time.Now().Add(timeout))

	reader := bufio.NewReader(conn)
	writer := bufio.NewWriter(conn)
	fmt.Fprintf(writer, "220 SMTP Relay Ready\r\n")
	writer.Flush()

	var username, password string
	authenticated := false
	var mailFrom string
	var rcptTo []string

	for {
		// Reset read deadline for each command (60s per command)
		conn.SetReadDeadline(time.Now().Add(60 * time.Second))

		line, err := reader.ReadString('\n')
		if err != nil {
			if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
				logger.Debug("Connection timeout", "remote", conn.RemoteAddr())
				fmt.Fprintf(writer, "421 4.4.2 Connection timeout\r\n")
				writer.Flush()
			} else {
				logger.Error("Client read error", "error", err, "remote", conn.RemoteAddr())
				fmt.Fprintf(writer, "421 4.7.0 Service not available\r\n")
				writer.Flush()
			}
			return
		}

		// Input length validation (RFC 5321 recommends 512 for command lines)
		if len(line) > 512 {
			logger.Warn("Command line too long", "length", len(line), "remote", conn.RemoteAddr())
			fmt.Fprintf(writer, "500 5.5.1 Line too long\r\n")
			writer.Flush()
			continue
		}

		line = strings.TrimRight(line, "\r\n")
		if line == "" { // Ignore empty lines
			continue
		}

		// Log the received command
		logger.Debug("Received SMTP command", "command", line)

		// Handle EHLO/HELO commands
		if strings.HasPrefix(strings.ToUpper(line), "EHLO") || strings.HasPrefix(strings.ToUpper(line), "HELO") {
			// Note: STARTTLS removed as it's not implemented
			fmt.Fprintf(writer, "250-smtpRelay\r\n250 AUTH LOGIN\r\n")
			writer.Flush()
			continue
		}

		if strings.HasPrefix(strings.ToUpper(line), "AUTH LOGIN") {
			// Handle both: AUTH LOGIN (prompt for username) and AUTH LOGIN <base64-username>
			parts := strings.Fields(line)
			if len(parts) == 3 {
				// AUTH LOGIN <base64-username>
				userB64 := strings.TrimSpace(parts[2])
				var decodeErr error
				username, decodeErr = decodeBase64WithError(userB64)
				if decodeErr != nil {
					logger.Error("Invalid base64 in username", "error", decodeErr)
					fmt.Fprintf(writer, "501 5.5.4 Invalid base64 encoding\r\n")
					writer.Flush()
					continue
				}
				logger.Debug("AUTH LOGIN inline username", "username", username)
				fmt.Fprintf(writer, "334 UGFzc3dvcmQ6\r\n") // 'Password:' base64
				writer.Flush()
				passB64, err := reader.ReadString('\n')
				if err != nil {
					logger.Error("Failed to read password during AUTH", "error", err)
					fmt.Fprintf(writer, "421 4.7.0 Connection error during authentication\r\n")
					writer.Flush()
					return
				}
				passB64 = strings.TrimSpace(passB64)
				password, decodeErr = decodeBase64WithError(passB64)
				if decodeErr != nil {
					logger.Error("Invalid base64 in password", "error", decodeErr)
					fmt.Fprintf(writer, "501 5.5.4 Invalid base64 encoding\r\n")
					writer.Flush()
					continue
				}
			} else {
				// Standard flow: prompt for username
				fmt.Fprintf(writer, "334 VXNlcm5hbWU6\r\n") // 'Username:' base64
				writer.Flush()
				userB64, err := reader.ReadString('\n')
				if err != nil {
					logger.Error("Failed to read username during AUTH", "error", err)
					fmt.Fprintf(writer, "421 4.7.0 Connection error during authentication\r\n")
					writer.Flush()
					return
				}
				userB64 = strings.TrimSpace(userB64)
				var decodeErr error
				username, decodeErr = decodeBase64WithError(userB64)
				if decodeErr != nil {
					logger.Error("Invalid base64 in username", "error", decodeErr)
					fmt.Fprintf(writer, "501 5.5.4 Invalid base64 encoding\r\n")
					writer.Flush()
					continue
				}
				logger.Debug("AUTH LOGIN username", "username", username)
				fmt.Fprintf(writer, "334 UGFzc3dvcmQ6\r\n") // 'Password:' base64
				writer.Flush()
				passB64, err := reader.ReadString('\n')
				if err != nil {
					logger.Error("Failed to read password during AUTH", "error", err)
					fmt.Fprintf(writer, "421 4.7.0 Connection error during authentication\r\n")
					writer.Flush()
					return
				}
				passB64 = strings.TrimSpace(passB64)
				password, decodeErr = decodeBase64WithError(passB64)
				if decodeErr != nil {
					logger.Error("Invalid base64 in password", "error", decodeErr)
					fmt.Fprintf(writer, "501 5.5.4 Invalid base64 encoding\r\n")
					writer.Flush()
					continue
				}
			}

			if username == "" || password == "" {
				// Use fallback credentials from config if not provided by client
				if config.FallbackSMTPuser == "" || config.FallbackSMTPpass == "" {
					fmt.Fprintf(writer, "535 5.7.8 Authentication credentials invalid\r\n")
					writer.Flush()
					logger.Error("Authentication failed: no credentials provided")
					return
				}
				logger.Warn("Using fallback credentials - per-user auditing bypassed",
					"client_ip", conn.RemoteAddr())
				username = config.FallbackSMTPuser
				password = config.FallbackSMTPpass
			}

			// Validate username and password via OAuth2
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			_, err = getCachedOAuth2Token(ctx, username, password)
			cancel()
			if err != nil {
				logger.Error("OAuth2 token retrieval failed", "error", err)
				fmt.Fprintf(writer, "535 5.7.8 Authentication failed\r\n")
				writer.Flush()
				return
			}
			fmt.Fprintf(writer, "235 2.7.0 Authentication successful\r\n")
			writer.Flush()
			logger.Debug("User authenticated", "username", username)
			authenticated = true
			continue
		}

		// If not authenticated, any command other than AUTH should fail
		if !authenticated {
			logger.Error("Authentication required for command", "command", line)
			fmt.Fprintf(writer, "530 5.7.0 Authentication required\r\n")
			writer.Flush()
			continue
		}

		// Handle MAIL FROM, RCPT TO, DATA commands
		if strings.HasPrefix(strings.ToUpper(line), "MAIL FROM:") {
			mailFrom = extractAddress(line)
			if mailFrom == "" {
				fmt.Fprintf(writer, "501 5.1.7 Invalid sender address\r\n")
				writer.Flush()
				continue
			}
			fmt.Fprintf(writer, "250 2.1.0 Ok\r\n")
			writer.Flush()
			continue
		}

		if strings.HasPrefix(strings.ToUpper(line), "RCPT TO:") {
			addr := extractAddress(line)
			if addr == "" || !isValidEmail(addr) {
				fmt.Fprintf(writer, "553 5.1.3 Invalid recipient address\r\n")
				writer.Flush()
				continue
			}
			rcptTo = append(rcptTo, addr)
			fmt.Fprintf(writer, "250 2.1.5 Ok\r\n")
			writer.Flush()
			continue
		}

		if strings.HasPrefix(strings.ToUpper(line), "DATA") {
			// Validate we have recipients before accepting DATA
			if len(rcptTo) == 0 {
				fmt.Fprintf(writer, "503 5.5.1 No recipients specified\r\n")
				writer.Flush()
				continue
			}

			fmt.Fprintf(writer, "354 End data with <CR><LF>.<CR><LF>\r\n")
			writer.Flush()

			var messageSize int64
			var dataBuffer strings.Builder

			for {
				// Reset deadline for DATA reading
				conn.SetReadDeadline(time.Now().Add(60 * time.Second))

				dataLine, err := reader.ReadString('\n')
				if err != nil {
					logger.Error("Client read error during DATA", "error", err)
					return
				}
				if strings.TrimSpace(dataLine) == "." {
					break
				}

				messageSize += int64(len(dataLine))
				if messageSize > config.MaxMessageSize {
					fmt.Fprintf(writer, "552 5.3.4 Message too large (max %d bytes)\r\n", config.MaxMessageSize)
					writer.Flush()
					logger.Warn("Message rejected: size exceeded", "size", messageSize, "max", config.MaxMessageSize)
					// Drain remaining data to keep connection in sync
					for {
						drainLine, err := reader.ReadString('\n')
						if err != nil || strings.TrimSpace(drainLine) == "." {
							break
						}
					}
					// Reset for next message attempt
					mailFrom = ""
					rcptTo = nil
					goto continueLoop
				}
				dataBuffer.WriteString(dataLine)
			}

			// Reconstruct message and normalize line endings for MIME parsing
			msg := dataBuffer.String()
			msg = strings.ReplaceAll(msg, "\r\n", "\n")
			msg = strings.ReplaceAll(msg, "\r", "\n")
			msg = strings.ReplaceAll(msg, "\n", "\r\n")

			// Parse subject, body, and attachments
			subject, body, isHTML, attachments, parseErr := parseSubjectBodyAndAttachments(msg)
			if parseErr != nil {
				fmt.Fprintf(writer, "550 5.6.0 Message parsing failed: %v\r\n", parseErr)
				writer.Flush()
				logger.Error("MIME parsing failed", "error", parseErr)
				return
			}

			// Get OAuth2 token and send via Graph API
			ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
			token, err := getCachedOAuth2Token(ctx, username, password)
			if err != nil {
				cancel()
				fmt.Fprintf(writer, "451 4.7.0 Temporary authentication failure\r\n")
				writer.Flush()
				logger.Error("Failed to get OAuth2 token", "error", err, "username", username)
				return
			}

			if err := sendMailGraphAPI(ctx, token, username, mailFrom, rcptTo, subject, body, isHTML, attachments); err != nil {
				cancel()
				fmt.Fprintf(writer, "550 5.7.0 Delivery failed: %v\r\n", err)
				writer.Flush()
				logger.Error("Failed to send email via Graph API", "error", err, "username", username, "mailFrom", mailFrom, "rcptTo", rcptTo)
				return
			}
			cancel()

			fmt.Fprintf(writer, "250 2.0.0 Ok: queued as graphapi\r\n")
			writer.Flush()
			// Reset for next message
			logger.Info("E-mail sent successfully", "username", username, "mailFrom", mailFrom, "rcptTo", rcptTo, "subject", subject)
			mailFrom = ""
			rcptTo = nil
			continue
		}

	continueLoop:
		if strings.HasPrefix(strings.ToUpper(line), "QUIT") {
			fmt.Fprintf(writer, "221 2.0.0 Bye\r\n")
			writer.Flush()
			return
		}

		if strings.HasPrefix(strings.ToUpper(line), "RSET") {
			mailFrom = ""
			rcptTo = nil
			fmt.Fprintf(writer, "250 2.0.0 Ok\r\n")
			writer.Flush()
			continue
		}

		if strings.HasPrefix(strings.ToUpper(line), "NOOP") {
			fmt.Fprintf(writer, "250 2.0.0 Ok\r\n")
			writer.Flush()
			continue
		}

		// Default: 502 Command not implemented
		fmt.Fprintf(writer, "502 5.5.2 Command not implemented\r\n")
		writer.Flush()
	}
}

// isValidEmail performs basic email validation
func isValidEmail(email string) bool {
	if len(email) > 254 || len(email) == 0 {
		return false
	}
	atIdx := strings.LastIndex(email, "@")
	if atIdx <= 0 || atIdx >= len(email)-1 {
		return false
	}
	local := email[:atIdx]
	domain := email[atIdx+1:]
	if len(local) > 64 || len(domain) > 253 {
		return false
	}
	// Check domain has at least one dot
	if !strings.Contains(domain, ".") {
		return false
	}
	return true
}

// extractAddress extracts the email address from SMTP command line
func extractAddress(line string) string {
	start := strings.Index(line, "<")
	end := strings.Index(line, ">")
	if start != -1 && end != -1 && end > start {
		return line[start+1 : end]
	}
	// fallback: try after colon
	parts := strings.SplitN(line, ":", 2)
	if len(parts) == 2 {
		return strings.TrimSpace(parts[1])
	}
	return ""
}

// Attachment represents a parsed email attachment
// filename, contentType, and base64-encoded content
type Attachment struct {
	Filename    string
	ContentType string
	Content     string // base64-encoded
}

// parseSubjectBodyAndAttachments parses the subject, body, and attachments from a raw SMTP message
func parseSubjectBodyAndAttachments(msg string) (subject, body string, isHTML bool, attachments []Attachment, err error) {
	// Ensure message ends with a newline for robust parsing
	if !strings.HasSuffix(msg, "\n") {
		msg += "\n"
	}
	r := strings.NewReader(msg)
	m, err := mail.ReadMessage(r)
	if err != nil {
		return "", "", false, nil, fmt.Errorf("mail.ReadMessage failed: %w", err)
	}
	wd := new(mime.WordDecoder)
	subjectRaw := m.Header.Get("Subject")
	subject, err = wd.DecodeHeader(subjectRaw)
	if err != nil {
		subject = subjectRaw // fallback to raw if decode fails
	}
	ct := m.Header.Get("Content-Type")
	cte := strings.ToLower(m.Header.Get("Content-Transfer-Encoding"))
	if strings.Contains(strings.ToLower(ct), "html") {
		isHTML = true
	}
	mediaType, params, err := mime.ParseMediaType(ct)
	dataContent := []byte{}
	if err == nil && strings.HasPrefix(mediaType, "multipart/") {
		mr := multipart.NewReader(m.Body, params["boundary"])
		const maxParts = 100 // Prevent infinite loops from malformed multipart
		partCount := 0
		for {
			p, err := mr.NextPart()
			if err == io.EOF {
				break
			}
			if err != nil {
				// On any multipart parsing error (including missing boundary),
				// log and break instead of hanging
				logger.Debug("Multipart parsing ended", "reason", err.Error(), "parts_parsed", partCount)
				break
			}
			partCount++
			if partCount > maxParts {
				logger.Warn("Multipart message exceeded max parts limit", "max", maxParts)
				break
			}
			// Decode the part's subject if available
			if strings.HasPrefix(p.Header.Get("Content-Disposition"), "attachment") {
				filename := p.FileName()
				// Try to extract filename from Content-Type if still empty
				if filename == "" {
					ct := p.Header.Get("Content-Type")
					_, params, err := mime.ParseMediaType(ct)
					if err == nil {
						if n, ok := params["name"]; ok && n != "" {
							filename = n
							logger.Debug("Attachment filename extracted from Content-Type name param", "filename", filename)
						}
					}
				}
				ctype := p.Header.Get("Content-Type")
				if ctype == "" {
					ctype = "application/octet-stream"
				}
				attCTE := strings.ToLower(p.Header.Get("Content-Transfer-Encoding"))
				if dataContent, err = decodeMessage(attCTE, p); err != nil {
					if config.StrictAttachments {
						return "", "", false, nil, fmt.Errorf("failed to decode attachment %q: %w", filename, err)
					}
					logger.Warn("Failed to decode attachment, skipping", "filename", filename, "error", err)
					continue // skip this attachment if decoding fails
				}
				if filename == "" || ctype == "" || len(dataContent) == 0 {
					logger.Warn("Invalid attachment detected, skipping", "filename", filename, "contentType", ctype, "dataLength", len(dataContent))
					continue // skip invalid attachments
				}
				attachments = append(attachments, Attachment{
					Filename:    filename,
					ContentType: ctype,
					Content:     base64.StdEncoding.EncodeToString(dataContent),
				})
			} else {
				// treat as body part
				cte := strings.ToLower(p.Header.Get("Content-Transfer-Encoding"))
				if dataContent, err = decodeMessage(cte, p); err != nil {
					logger.Warn("Failed to decode body part", "error", err)
					continue // skip this part if decoding fails
				}
				// If the part is HTML, set isHTML flag
				if strings.Contains(strings.ToLower(p.Header.Get("Content-Type")), "html") {
					isHTML = true
				}
				body = string(dataContent)
			}
		}
		return subject, body, isHTML, attachments, nil
	}
	// Not multipart: fallback to old logic
	if dataContent, err = decodeMessage(cte, m.Body); err != nil {
		return "", "", false, nil, fmt.Errorf("failed to decode message body: %w", err)
	}

	return subject, string(dataContent), isHTML, nil, nil
}

func decodeMessage(c string, r io.Reader) (content []byte, err error) {
	switch c {
	case "base64":
		content, err = io.ReadAll(base64.NewDecoder(base64.StdEncoding, r))
	case "quoted-printable":
		content, err = io.ReadAll(quotedprintable.NewReader(r))
	default:
		content, err = io.ReadAll(r)
	}
	if err != nil {
		return nil, fmt.Errorf("failed to read part: %w", err)
	}
	return content, nil
}

// sendMailGraphAPI sends the email via Microsoft Graph API /sendMail with retry logic
func sendMailGraphAPI(ctx context.Context, token, sender, mailFrom string, rcptTo []string, subject, body string, isHTML bool, attachments []Attachment) error {
	graphURL := "https://graph.microsoft.com/v1.0/users/" + sender + "/sendMail"
	contentType := "text"
	if isHTML {
		contentType = "html"
	}
	var toRecipients []map[string]map[string]string
	for _, addr := range rcptTo {
		toRecipients = append(toRecipients, map[string]map[string]string{
			"emailAddress": {"address": addr},
		})
	}
	var graphAttachments []map[string]interface{}
	for _, att := range attachments {
		graphAttachments = append(graphAttachments, map[string]interface{}{
			"@odata.type":  "#microsoft.graph.fileAttachment",
			"name":         att.Filename,
			"contentType":  att.ContentType,
			"contentBytes": att.Content,
		})
	}
	if graphAttachments == nil {
		graphAttachments = make([]map[string]interface{}, 0)
	}
	msg := map[string]interface{}{
		"message": map[string]interface{}{
			"subject": subject,
			"body": map[string]string{
				"contentType": contentType,
				"content":     body,
			},
			"toRecipients": toRecipients,
			"from": map[string]map[string]string{
				"emailAddress": {"address": mailFrom},
			},
			"attachments": graphAttachments,
		},
		"saveToSentItems": config.SaveToSent,
	}

	jsonBody, err := json.Marshal(msg)
	if err != nil {
		return fmt.Errorf("failed to marshal email message: %w", err)
	}

	request, err := http.NewRequestWithContext(ctx, "POST", graphURL, bytes.NewReader(jsonBody))
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("Content-Type", "application/json")

	// Use retry logic for Graph API calls
	resp, err := doWithRetry(ctx, graphHTTPClient, request, jsonBody, getRetryConfig())
	if err != nil {
		return fmt.Errorf("Graph API call failed after retries: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 300 {
		b, readErr := io.ReadAll(resp.Body)
		if readErr != nil {
			return fmt.Errorf("Graph API error (status %d, failed to read body: %v)", resp.StatusCode, readErr)
		}
		return fmt.Errorf("Graph API error (status %d): %s", resp.StatusCode, string(b))
	}
	return nil
}

// decodeBase64WithError decodes base64 and returns error instead of empty string
func decodeBase64WithError(s string) (string, error) {
	b, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		return "", fmt.Errorf("base64 decode failed: %w", err)
	}
	return string(b), nil
}

// getCachedOAuth2Token returns a cached token or fetches a new one if expired
// Uses singleflight to prevent duplicate concurrent fetches for the same user
func getCachedOAuth2Token(ctx context.Context, username, password string) (string, error) {
	// Check cache first
	if val, ok := TokenCache.Load(username); ok {
		tok := val.(cachedToken)
		if time.Now().Before(tok.expiresAt) {
			logger.Debug("Using cached OAuth2 token", "username", username, "expires_at", tok.expiresAt)
			return tok.token, nil
		}
	}

	// Use singleflight to deduplicate concurrent fetches for same user
	result, err, _ := tokenFetchGroup.Do(username, func() (interface{}, error) {
		// Double-check cache (another goroutine may have populated it)
		if val, ok := TokenCache.Load(username); ok {
			tok := val.(cachedToken)
			if time.Now().Before(tok.expiresAt) {
				return tok.token, nil
			}
		}

		token, expiresIn, err := getOAuth2TokenWithExpiry(ctx, username, password)
		if err != nil {
			return "", err
		}

		TokenCache.Store(username, cachedToken{
			token:     token,
			expiresAt: time.Now().Add(time.Duration(expiresIn-60) * time.Second), // refresh 1 min before expiry
		})
		logger.Debug("New OAuth2 token cached", "username", username, "expires_in", expiresIn)
		return token, nil
	})

	if err != nil {
		return "", err
	}
	return result.(string), nil
}

// getOAuth2TokenWithExpiry returns token and expiry (in seconds)
func getOAuth2TokenWithExpiry(ctx context.Context, username, password string) (string, int, error) {
	// Add timeout to context if not already present
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	tokenURL := fmt.Sprintf("https://login.microsoftonline.com/%s/oauth2/v2.0/token", config.OAuth2Config.TenantID)

	params := url.Values{}
	params.Set("client_id", config.OAuth2Config.ClientID)
	params.Set("scope", strings.Join(config.OAuth2Config.Scopes, " "))
	params.Set("username", username)
	params.Set("password", password)
	params.Set("grant_type", "password")
	params.Set("client_secret", config.OAuth2Config.ClientSecret)

	req, err := http.NewRequestWithContext(ctx, "POST", tokenURL, strings.NewReader(params.Encode()))
	if err != nil {
		return "", 0, fmt.Errorf("failed to create token request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := authHTTPClient.Do(req)
	if err != nil {
		return "", 0, fmt.Errorf("token request failed: %w", err)
	}
	defer resp.Body.Close()

	var result struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int    `json:"expires_in"`
		Error       string `json:"error"`
		ErrorDesc   string `json:"error_description"`
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", 0, fmt.Errorf("failed to read token response: %w", err)
	}

	if err := json.Unmarshal(body, &result); err != nil {
		return "", 0, fmt.Errorf("failed to parse token response: %w, body: %s", err, string(body))
	}

	// Check for OAuth error
	if result.Error != "" {
		return "", 0, fmt.Errorf("OAuth2 error: %s - %s", result.Error, result.ErrorDesc)
	}

	// Check if access token is present
	if result.AccessToken == "" {
		return "", 0, fmt.Errorf("no access token in response, body: %s", string(body))
	}

	logger.Debug("OAuth2 token retrieved", "username", username, "expires_in", result.ExpiresIn)
	return result.AccessToken, result.ExpiresIn, nil
}

// StartTokenCacheCleanup starts a background goroutine to clean expired tokens
func StartTokenCacheCleanup(interval time.Duration) {
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()

		for range ticker.C {
			now := time.Now()
			var deleted int

			TokenCache.Range(func(key, value interface{}) bool {
				tok := value.(cachedToken)
				if now.After(tok.expiresAt) {
					TokenCache.Delete(key)
					deleted++
				}
				return true
			})

			if deleted > 0 {
				logger.Debug("Token cache cleanup completed", "deleted", deleted)
			}
		}
	}()
}
