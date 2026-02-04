package main

import (
	"bytes"
	"encoding/base64"
	"mime/multipart"
	"strings"
	"testing"
)

func TestDecodeMessage_Base64(t *testing.T) {
	input := base64.StdEncoding.EncodeToString([]byte("hello world"))
	decoded, err := decodeMessage("base64", strings.NewReader(input))
	if err != nil {
		t.Fatalf("decodeMessage base64 failed: %v", err)
	}
	if string(decoded) != "hello world" {
		t.Errorf("expected 'hello world', got '%s'", string(decoded))
	}
}

func TestDecodeMessage_QuotedPrintable(t *testing.T) {
	input := "hello=20world=21"
	decoded, err := decodeMessage("quoted-printable", strings.NewReader(input))
	if err != nil {
		t.Fatalf("decodeMessage quoted-printable failed: %v", err)
	}
	if string(decoded) != "hello world!" {
		t.Errorf("expected 'hello world!', got '%s'", string(decoded))
	}
}

func TestDecodeMessage_Default(t *testing.T) {
	input := "plain text"
	decoded, err := decodeMessage("", strings.NewReader(input))
	if err != nil {
		t.Fatalf("decodeMessage default failed: %v", err)
	}
	if string(decoded) != input {
		t.Errorf("expected '%s', got '%s'", input, string(decoded))
	}
}

func TestParseSubjectBodyAndAttachments_Simple(t *testing.T) {
	raw := "From: test@example.com\r\nTo: you@example.com\r\nSubject: Hello\r\n\r\nThis is the body."
	subject, body, isHTML, attachments, _, _, err := parseSubjectBodyAndAttachments(raw)
	if err != nil {
		t.Fatalf("parseSubjectBodyAndAttachments failed: %v", err)
	}
	if subject != "Hello" {
		t.Errorf("expected subject 'Hello', got '%s'", subject)
	}
	// Accept both with and without trailing newline
	expectedBody := "This is the body."
	if strings.TrimRight(body, "\r\n") != expectedBody {
		t.Errorf("expected body '%s', got '%s'", expectedBody, body)
	}
	if isHTML {
		t.Errorf("expected isHTML false, got true")
	}
	if len(attachments) != 0 {
		t.Errorf("expected 0 attachments, got %d", len(attachments))
	}
}

func TestParseSubjectBodyAndAttachments_SimpleHTML(t *testing.T) {
	raw := "From: test@example.com\r\nTo: you@example.com\r\nSubject: Hello\r\nContent-Type: text/html\r\n\r\n<html><body>Hi!</body></html>"
	subject, body, isHTML, attachments, _, _, err := parseSubjectBodyAndAttachments(raw)
	if err != nil {
		t.Fatalf("parseSubjectBodyAndAttachments failed: %v", err)
	}
	if subject != "Hello" {
		t.Errorf("expected subject 'Hello', got '%s'", subject)
	}
	expectedBody := "<html><body>Hi!</body></html>"
	if strings.TrimRight(body, "\r\n") != expectedBody {
		t.Errorf("expected body '%s', got '%s'", expectedBody, body)
	}
	if !isHTML {
		t.Errorf("expected isHTML true, got false")
	}
	if len(attachments) != 0 {
		t.Errorf("expected 0 attachments, got %d", len(attachments))
	}
}

func TestParseSubjectBodyAndAttachments_MultipartWithAttachment(t *testing.T) {
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	boundary := w.Boundary()
	// Body part
	bodyPart, _ := w.CreatePart(map[string][]string{
		"Content-Type":              {"text/plain"},
		"Content-Transfer-Encoding": {"7bit"},
	})
	bodyPart.Write([]byte("This is the body."))
	// Attachment part
	attPart, _ := w.CreatePart(map[string][]string{
		"Content-Type":              {"text/plain; name=\"file.txt\""},
		"Content-Disposition":       {"attachment; filename=\"file.txt\""},
		"Content-Transfer-Encoding": {"base64"},
	})
	attContent := base64.StdEncoding.EncodeToString([]byte("file content"))
	attPart.Write([]byte(attContent))
	w.Close()
	msg := "From: test@example.com\r\nTo: you@example.com\r\nSubject: Multipart\r\nMIME-Version: 1.0\r\nContent-Type: multipart/mixed; boundary=\"" + boundary + "\"\r\n\r\n" + buf.String()
	subject, body, isHTML, attachments, _, _, err := parseSubjectBodyAndAttachments(msg)
	if err != nil {
		t.Fatalf("parseSubjectBodyAndAttachments failed: %v", err)
	}
	if subject != "Multipart" {
		t.Errorf("expected subject 'Multipart', got '%s'", subject)
	}
	if strings.TrimRight(body, "\r\n") != "This is the body." {
		t.Errorf("expected body 'This is the body.', got '%s'", body)
	}
	if isHTML {
		t.Errorf("expected isHTML false, got true")
	}
	if len(attachments) != 1 {
		t.Errorf("expected 1 attachment, got %d", len(attachments))
	}
	if attachments[0].Filename != "file.txt" {
		t.Errorf("expected attachment filename 'file.txt', got '%s'", attachments[0].Filename)
	}
	decoded, _ := base64.StdEncoding.DecodeString(attachments[0].Content)
	if string(decoded) != "file content" {
		t.Errorf("expected attachment content 'file content', got '%s'", string(decoded))
	}
}

func TestParseSubjectBodyAndAttachments_MultipartHTMLBody(t *testing.T) {
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	boundary := w.Boundary()
	// HTML body part
	bodyPart, _ := w.CreatePart(map[string][]string{
		"Content-Type":              {"text/html"},
		"Content-Transfer-Encoding": {"7bit"},
	})
	bodyPart.Write([]byte("<b>HTML Body</b>"))
	w.Close()
	msg := "From: test@example.com\r\nTo: you@example.com\r\nSubject: HTMLMultipart\r\nMIME-Version: 1.0\r\nContent-Type: multipart/alternative; boundary=\"" + boundary + "\"\r\n\r\n" + buf.String()
	subject, body, isHTML, attachments, _, _, err := parseSubjectBodyAndAttachments(msg)
	if err != nil {
		t.Fatalf("parseSubjectBodyAndAttachments failed: %v", err)
	}
	if subject != "HTMLMultipart" {
		t.Errorf("expected subject 'HTMLMultipart', got '%s'", subject)
	}
	if strings.TrimRight(body, "\r\n") != "<b>HTML Body</b>" {
		t.Errorf("expected body '<b>HTML Body</b>', got '%s'", body)
	}
	if !isHTML {
		t.Errorf("expected isHTML true, got false")
	}
	if len(attachments) != 0 {
		t.Errorf("expected 0 attachments, got %d", len(attachments))
	}
}

func TestParseSubjectBodyAndAttachments_MultipartNoBody(t *testing.T) {
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	boundary := w.Boundary()
	// Only attachment, no body part
	attPart, _ := w.CreatePart(map[string][]string{
		"Content-Type":              {"application/octet-stream; name=\"file.bin\""},
		"Content-Disposition":       {"attachment; filename=\"file.bin\""},
		"Content-Transfer-Encoding": {"base64"},
	})
	attContent := base64.StdEncoding.EncodeToString([]byte("binarydata"))
	attPart.Write([]byte(attContent))
	w.Close()
	msg := "From: test@example.com\r\nTo: you@example.com\r\nSubject: OnlyAttachment\r\nMIME-Version: 1.0\r\nContent-Type: multipart/mixed; boundary=\"" + boundary + "\"\r\n\r\n" + buf.String()
	subject, body, isHTML, attachments, _, _, err := parseSubjectBodyAndAttachments(msg)
	if err != nil {
		t.Fatalf("parseSubjectBodyAndAttachments failed: %v", err)
	}
	if subject != "OnlyAttachment" {
		t.Errorf("expected subject 'OnlyAttachment', got '%s'", subject)
	}
	if body != "" {
		t.Errorf("expected empty body, got '%s'", body)
	}
	if isHTML {
		t.Errorf("expected isHTML false, got true")
	}
	if len(attachments) != 1 {
		t.Errorf("expected 1 attachment, got %d", len(attachments))
	}
	if attachments[0].Filename != "file.bin" {
		t.Errorf("expected attachment filename 'file.bin', got '%s'", attachments[0].Filename)
	}
	decoded, _ := base64.StdEncoding.DecodeString(attachments[0].Content)
	if string(decoded) != "binarydata" {
		t.Errorf("expected attachment content 'binarydata', got '%s'", string(decoded))
	}
}

func TestParseSubjectBodyAndAttachments_EncodedSubject(t *testing.T) {
	raw := "From: test@example.com\r\nTo: you@example.com\r\nSubject: =?UTF-8?B?SGVsbG8g8J+agA==?=\r\n\r\nBody"
	subject, body, _, _, _, _, err := parseSubjectBodyAndAttachments(raw)
	if err != nil {
		t.Fatalf("parseSubjectBodyAndAttachments failed: %v", err)
	}
	if subject != "Hello 🚀" {
		t.Errorf("expected subject 'Hello 🚀', got '%s'", subject)
	}
	if strings.TrimRight(body, "\r\n") != "Body" {
		t.Errorf("expected body 'Body', got '%s'", body)
	}
}

func TestParseSubjectBodyAndAttachments_NestedMultipartMixedAlternative(t *testing.T) {
	// Build inner multipart/alternative with text and HTML
	var innerBuf bytes.Buffer
	innerWriter := multipart.NewWriter(&innerBuf)
	innerBoundary := innerWriter.Boundary()
	textPart, _ := innerWriter.CreatePart(map[string][]string{
		"Content-Type":              {"text/plain; charset=utf-8"},
		"Content-Transfer-Encoding": {"7bit"},
	})
	textPart.Write([]byte("Plain body"))
	htmlPart, _ := innerWriter.CreatePart(map[string][]string{
		"Content-Type":              {"text/html; charset=utf-8"},
		"Content-Transfer-Encoding": {"7bit"},
	})
	htmlPart.Write([]byte("<b>HTML body</b>"))
	innerWriter.Close()

	// Build outer multipart/mixed with the inner alternative + attachment
	var outerBuf bytes.Buffer
	outerWriter := multipart.NewWriter(&outerBuf)
	outerBoundary := outerWriter.Boundary()
	altPart, _ := outerWriter.CreatePart(map[string][]string{
		"Content-Type": {"multipart/alternative; boundary=\"" + innerBoundary + "\""},
	})
	altPart.Write(innerBuf.Bytes())
	attPart, _ := outerWriter.CreatePart(map[string][]string{
		"Content-Type":              {"application/pdf; name=\"doc.pdf\""},
		"Content-Disposition":       {"attachment; filename=\"doc.pdf\""},
		"Content-Transfer-Encoding": {"base64"},
	})
	attPart.Write([]byte(base64.StdEncoding.EncodeToString([]byte("pdf content"))))
	outerWriter.Close()

	msg := "From: test@example.com\r\nTo: you@example.com\r\nSubject: Nested\r\nMIME-Version: 1.0\r\nContent-Type: multipart/mixed; boundary=\"" + outerBoundary + "\"\r\n\r\n" + outerBuf.String()

	subject, body, isHTML, attachments, _, _, err := parseSubjectBodyAndAttachments(msg)
	if err != nil {
		t.Fatalf("parseSubjectBodyAndAttachments failed: %v", err)
	}
	if subject != "Nested" {
		t.Errorf("expected subject 'Nested', got '%s'", subject)
	}
	if strings.TrimRight(body, "\r\n") != "<b>HTML body</b>" {
		t.Errorf("expected HTML body '<b>HTML body</b>', got '%s'", body)
	}
	if !isHTML {
		t.Errorf("expected isHTML true, got false")
	}
	if len(attachments) != 1 {
		t.Fatalf("expected 1 attachment, got %d", len(attachments))
	}
	if attachments[0].Filename != "doc.pdf" {
		t.Errorf("expected attachment filename 'doc.pdf', got '%s'", attachments[0].Filename)
	}
}

func TestParseSubjectBodyAndAttachments_MultipartAlternativePreferHTML(t *testing.T) {
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	boundary := w.Boundary()
	textPart, _ := w.CreatePart(map[string][]string{
		"Content-Type":              {"text/plain"},
		"Content-Transfer-Encoding": {"7bit"},
	})
	textPart.Write([]byte("Plain"))
	htmlPart, _ := w.CreatePart(map[string][]string{
		"Content-Type":              {"text/html"},
		"Content-Transfer-Encoding": {"7bit"},
	})
	htmlPart.Write([]byte("<b>HTML</b>"))
	w.Close()

	msg := "From: test@example.com\r\nTo: you@example.com\r\nSubject: Alt\r\nMIME-Version: 1.0\r\nContent-Type: multipart/alternative; boundary=\"" + boundary + "\"\r\n\r\n" + buf.String()

	_, body, isHTML, attachments, _, _, err := parseSubjectBodyAndAttachments(msg)
	if err != nil {
		t.Fatalf("parseSubjectBodyAndAttachments failed: %v", err)
	}
	if strings.TrimRight(body, "\r\n") != "<b>HTML</b>" {
		t.Errorf("expected body '<b>HTML</b>', got '%s'", body)
	}
	if !isHTML {
		t.Errorf("expected isHTML true, got false")
	}
	if len(attachments) != 0 {
		t.Errorf("expected 0 attachments, got %d", len(attachments))
	}
}

func TestParseSubjectBodyAndAttachments_MultipartAlternativePlainOnly(t *testing.T) {
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	boundary := w.Boundary()
	textPart, _ := w.CreatePart(map[string][]string{
		"Content-Type":              {"text/plain"},
		"Content-Transfer-Encoding": {"7bit"},
	})
	textPart.Write([]byte("Just plain text"))
	w.Close()

	msg := "From: test@example.com\r\nTo: you@example.com\r\nSubject: PlainOnly\r\nMIME-Version: 1.0\r\nContent-Type: multipart/alternative; boundary=\"" + boundary + "\"\r\n\r\n" + buf.String()

	_, body, isHTML, _, _, _, err := parseSubjectBodyAndAttachments(msg)
	if err != nil {
		t.Fatalf("parseSubjectBodyAndAttachments failed: %v", err)
	}
	if strings.TrimRight(body, "\r\n") != "Just plain text" {
		t.Errorf("expected body 'Just plain text', got '%s'", body)
	}
	if isHTML {
		t.Errorf("expected isHTML false, got true")
	}
}

func TestParseSubjectBodyAndAttachments_InlineImage(t *testing.T) {
	imgData := []byte{0x89, 0x50, 0x4E, 0x47} // PNG magic bytes
	imgB64 := base64.StdEncoding.EncodeToString(imgData)

	// Build multipart/related with HTML body + inline image
	var relBuf bytes.Buffer
	relWriter := multipart.NewWriter(&relBuf)
	relBoundary := relWriter.Boundary()
	htmlPart, _ := relWriter.CreatePart(map[string][]string{
		"Content-Type":              {"text/html; charset=utf-8"},
		"Content-Transfer-Encoding": {"7bit"},
	})
	htmlPart.Write([]byte("<html><body><img src=\"cid:img001\"></body></html>"))
	inlinePart, _ := relWriter.CreatePart(map[string][]string{
		"Content-Type":              {"image/png; name=\"logo.png\""},
		"Content-Disposition":       {"inline; filename=\"logo.png\""},
		"Content-Transfer-Encoding": {"base64"},
		"Content-Id":                {"<img001>"},
	})
	inlinePart.Write([]byte(imgB64))
	relWriter.Close()

	// Wrap in multipart/mixed with an additional regular attachment
	var mixedBuf bytes.Buffer
	mixedWriter := multipart.NewWriter(&mixedBuf)
	mixedBoundary := mixedWriter.Boundary()
	relPart, _ := mixedWriter.CreatePart(map[string][]string{
		"Content-Type": {"multipart/related; boundary=\"" + relBoundary + "\""},
	})
	relPart.Write(relBuf.Bytes())
	attPart, _ := mixedWriter.CreatePart(map[string][]string{
		"Content-Type":              {"application/pdf; name=\"report.pdf\""},
		"Content-Disposition":       {"attachment; filename=\"report.pdf\""},
		"Content-Transfer-Encoding": {"base64"},
	})
	attPart.Write([]byte(base64.StdEncoding.EncodeToString([]byte("pdf data"))))
	mixedWriter.Close()

	msg := "From: test@example.com\r\nTo: you@example.com\r\nSubject: InlineImg\r\nMIME-Version: 1.0\r\nContent-Type: multipart/mixed; boundary=\"" + mixedBoundary + "\"\r\n\r\n" + mixedBuf.String()

	subject, body, isHTML, attachments, _, _, err := parseSubjectBodyAndAttachments(msg)
	if err != nil {
		t.Fatalf("parseSubjectBodyAndAttachments failed: %v", err)
	}
	if subject != "InlineImg" {
		t.Errorf("expected subject 'InlineImg', got '%s'", subject)
	}
	if strings.TrimRight(body, "\r\n") != "<html><body><img src=\"cid:img001\"></body></html>" {
		t.Errorf("expected HTML body with cid reference, got '%s'", body)
	}
	if !isHTML {
		t.Errorf("expected isHTML true, got false")
	}
	if len(attachments) != 2 {
		t.Fatalf("expected 2 attachments, got %d", len(attachments))
	}
	// Find inline attachment
	var inlineAtt, regularAtt *Attachment
	for i := range attachments {
		if attachments[i].IsInline {
			inlineAtt = &attachments[i]
		} else {
			regularAtt = &attachments[i]
		}
	}
	if inlineAtt == nil {
		t.Fatal("expected an inline attachment, found none")
	}
	if inlineAtt.ContentID != "img001" {
		t.Errorf("expected ContentID 'img001', got '%s'", inlineAtt.ContentID)
	}
	if inlineAtt.Filename != "logo.png" {
		t.Errorf("expected inline filename 'logo.png', got '%s'", inlineAtt.Filename)
	}
	if regularAtt == nil {
		t.Fatal("expected a regular attachment, found none")
	}
	if regularAtt.Filename != "report.pdf" {
		t.Errorf("expected regular attachment filename 'report.pdf', got '%s'", regularAtt.Filename)
	}
}

func TestDecodeBase64WithError_Standard(t *testing.T) {
	encoded := base64.StdEncoding.EncodeToString([]byte("test value"))
	decoded, err := decodeBase64WithError(encoded)
	if err != nil {
		t.Fatalf("decodeBase64WithError failed: %v", err)
	}
	if decoded != "test value" {
		t.Errorf("expected 'test value', got '%s'", decoded)
	}
}

func TestDecodeBase64WithError_NoPadding(t *testing.T) {
	// Encode without padding (some SMTP clients do this)
	encoded := base64.RawStdEncoding.EncodeToString([]byte("test value"))
	// Verify it has no padding
	if strings.HasSuffix(encoded, "=") {
		t.Fatal("test setup error: encoded string should not have padding")
	}
	decoded, err := decodeBase64WithError(encoded)
	if err != nil {
		t.Fatalf("decodeBase64WithError with no padding failed: %v", err)
	}
	if decoded != "test value" {
		t.Errorf("expected 'test value', got '%s'", decoded)
	}
}

func TestDecodeBase64WithError_Invalid(t *testing.T) {
	_, err := decodeBase64WithError("not!valid!base64!!!")
	if err == nil {
		t.Error("expected error for invalid base64, got nil")
	}
}

func TestExtractAddress_AngleBrackets(t *testing.T) {
	addr := extractAddress("MAIL FROM:<user@example.com>")
	if addr != "user@example.com" {
		t.Errorf("expected 'user@example.com', got '%s'", addr)
	}
}

func TestExtractAddress_AngleBracketsWithParams(t *testing.T) {
	addr := extractAddress("MAIL FROM:<user@example.com> SIZE=12345")
	if addr != "user@example.com" {
		t.Errorf("expected 'user@example.com', got '%s'", addr)
	}
}

func TestExtractAddress_FallbackWithParameters(t *testing.T) {
	// Without angle brackets, fallback path should strip SMTP parameters
	addr := extractAddress("MAIL FROM: user@example.com SIZE=12345")
	if addr != "user@example.com" {
		t.Errorf("expected 'user@example.com', got '%s'", addr)
	}
}

func TestExtractAddress_FallbackSimple(t *testing.T) {
	addr := extractAddress("MAIL FROM: user@example.com")
	if addr != "user@example.com" {
		t.Errorf("expected 'user@example.com', got '%s'", addr)
	}
}

func TestExtractAddress_Empty(t *testing.T) {
	addr := extractAddress("MAIL FROM:<>")
	if addr != "" {
		t.Errorf("expected empty string, got '%s'", addr)
	}
}

func TestIsValidEmail_Valid(t *testing.T) {
	valid := []string{
		"user@example.com",
		"a@b.co",
		"user.name+tag@domain.org",
	}
	for _, email := range valid {
		if !isValidEmail(email) {
			t.Errorf("expected '%s' to be valid", email)
		}
	}
}

func TestIsValidEmail_Invalid(t *testing.T) {
	invalid := []string{
		"",
		"@example.com",
		"user@",
		"user@domain",
		"user",
		strings.Repeat("a", 65) + "@example.com",
	}
	for _, email := range invalid {
		if isValidEmail(email) {
			t.Errorf("expected '%s' to be invalid", email)
		}
	}
}
