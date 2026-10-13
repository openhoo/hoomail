package smtpserver

import "testing"

func TestParseReviewRootEncapsulatedAttachmentPreservesMessage(t *testing.T) {
	inner := "From: nested@example.test\r\nSubject: attached message\r\nContent-Type: text/plain\r\n\r\ninner body"
	raw := []byte("From: sender@example.test\r\nContent-Type: message/rfc822\r\nContent-Disposition: attachment; filename=attached.eml\r\n\r\n" + inner)
	parsed, err := Parse(raw, "sender@example.test", []string{"recipient@example.test"})
	if err != nil {
		t.Fatal(err)
	}
	if len(parsed.Attachments) != 1 {
		t.Fatalf("attachments=%#v", parsed.Attachments)
	}
	attachment := parsed.Attachments[0]
	if attachment.Filename == nil || *attachment.Filename != "attached.eml" || attachment.ContentType == nil || *attachment.ContentType != "message/rfc822" || string(attachment.Content) != inner {
		t.Fatalf("encapsulated message attachment lost wrapper/header content: %+v body=%q", attachment, attachment.Content)
	}
}
