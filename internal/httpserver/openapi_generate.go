//go:build ignore

package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

func ref(name string) map[string]any { return map[string]any{"$ref": "#/components/schemas/" + name} }

func array(items any) map[string]any { return map[string]any{"type": "array", "items": items} }

func object(required []string, properties map[string]any) map[string]any {
	result := map[string]any{"type": "object", "properties": properties}
	if len(required) != 0 {
		result["required"] = required
	}
	return result
}

func nullable(schema map[string]any) map[string]any {
	result := make(map[string]any, len(schema)+1)
	for key, value := range schema {
		result[key] = value
	}
	result["nullable"] = true
	return result
}

func jsonResponse(description string, schema any) map[string]any {
	return map[string]any{"description": description, "content": map[string]any{"application/json": map[string]any{"schema": schema}}}
}

func errorResponses(codes ...string) map[string]any {
	responses := map[string]any{"500": map[string]any{"description": "Internal server error", "content": map[string]any{"text/plain": map[string]any{"schema": map[string]any{"type": "string"}}}}}
	for _, code := range codes {
		description := "Invalid request"
		if code == "404" {
			description = "Resource not found"
		} else if code == "502" {
			description = "SMTP server unavailable"
		}
		responses[code] = jsonResponse(description, ref("Error"))
	}
	return responses
}

func withSuccess(responses map[string]any, status, description string, schema any) map[string]any {
	responses[status] = jsonResponse(description, schema)
	return responses
}

func pathParameter(name, description string) map[string]any {
	return map[string]any{"name": name, "in": "path", "required": true, "description": description, "schema": map[string]any{"type": "integer", "format": "int64"}}
}

func operation(tag, summary string, responses map[string]any) map[string]any {
	return map[string]any{"tags": []string{tag}, "summary": summary, "responses": responses}
}

func main() {
	versionBytes, err := os.ReadFile("../version/version")
	if err != nil {
		panic(err)
	}
	version := strings.TrimSpace(string(versionBytes))
	stringSchema := map[string]any{"type": "string"}
	integer := map[string]any{"type": "integer", "format": "int64"}
	boolean := map[string]any{"type": "boolean"}
	address := object([]string{"address"}, map[string]any{
		"address": stringSchema,
		"name":    nullable(stringSchema),
	})

	schemas := map[string]any{
		"Error":   object([]string{"error"}, map[string]any{"error": stringSchema}),
		"Success": object([]string{"ok"}, map[string]any{"ok": boolean}),
		"Mailbox": object([]string{"id", "address", "created_at", "last_message_at", "total_count", "unread_count"}, map[string]any{
			"id": integer, "address": stringSchema, "created_at": integer, "last_message_at": nullable(integer), "total_count": integer, "unread_count": integer,
		}),
		"MessageListItem": object([]string{"id", "from_address", "from_name", "subject", "snippet", "is_read", "received_at", "has_ical", "attachment_count"}, map[string]any{
			"id": integer, "from_address": nullable(stringSchema), "from_name": nullable(stringSchema), "subject": nullable(stringSchema), "snippet": stringSchema,
			"is_read": map[string]any{"type": "integer", "enum": []int{0, 1}}, "received_at": integer, "has_ical": map[string]any{"type": "integer", "enum": []int{0, 1}}, "attachment_count": integer,
		}),
		"CalendarEvent": object([]string{"id", "uid", "sequence", "summary", "description", "location", "status", "organizerAddress", "organizerName", "attendees", "dtstart", "dtend", "allDay", "lastMessageId", "updatedAt"}, map[string]any{
			"id": integer, "uid": stringSchema, "sequence": map[string]any{"type": "integer"}, "summary": nullable(stringSchema), "description": nullable(stringSchema), "location": nullable(stringSchema),
			"status": stringSchema, "organizerAddress": nullable(stringSchema), "organizerName": nullable(stringSchema), "attendees": array(address), "dtstart": integer,
			"dtend": nullable(integer), "allDay": boolean, "lastMessageId": nullable(integer), "updatedAt": integer,
		}),
		"AttachmentInfo": object([]string{"id", "filename", "contentType", "size"}, map[string]any{
			"id": integer, "filename": nullable(stringSchema), "contentType": nullable(stringSchema), "size": integer,
		}),
		"Message": object([]string{"id", "mailboxId", "fromAddress", "fromName", "to", "cc", "subject", "html", "text", "headers", "size", "receivedAt", "icalEvents"}, map[string]any{
			"id": integer, "mailboxId": integer, "fromAddress": nullable(stringSchema), "fromName": nullable(stringSchema), "to": array(address), "cc": array(address),
			"subject": nullable(stringSchema), "html": nullable(stringSchema), "text": nullable(stringSchema), "headers": map[string]any{"type": "object", "additionalProperties": stringSchema},
			"size": integer, "receivedAt": integer, "icalEvents": array(map[string]any{"type": "object", "additionalProperties": true}),
		}),
		"MessageAction": object([]string{"action", "ids"}, map[string]any{
			"action": map[string]any{"type": "string", "enum": []string{"delete", "read", "unread"}}, "ids": array(integer),
		}),
		"SendTestRequest": object(nil, map[string]any{
			"to": map[string]any{"type": "string", "format": "email", "default": "test@hoomail.local"}, "subject": stringSchema,
			"kind": map[string]any{"type": "string", "enum": []string{"plain", "invite", "update", "cancellation"}, "default": "plain"},
		}),
		"Analysis": object([]string{"version", "state", "parsedThroughPath", "unavailableRuleFamilies", "truncated"}, map[string]any{
			"version": map[string]any{"type": "integer"}, "state": stringSchema, "parsedThroughPath": nullable(stringSchema), "unavailableRuleFamilies": array(stringSchema), "truncated": boolean,
		}),
		"InspectionSummary": object([]string{"fail", "warning", "advisory", "observed", "pass", "notEvaluated"}, map[string]any{
			"fail": map[string]any{"type": "integer"}, "warning": map[string]any{"type": "integer"}, "advisory": map[string]any{"type": "integer"}, "observed": map[string]any{"type": "integer"}, "pass": map[string]any{"type": "integer"}, "notEvaluated": map[string]any{"type": "integer"},
		}),
		"Reference": object([]string{"label", "url"}, map[string]any{"label": stringSchema, "url": map[string]any{"type": "string", "format": "uri"}}),
		"Evidence": object([]string{"source"}, map[string]any{
			"source": stringSchema, "path": stringSchema, "field": stringSchema, "occurrence": map[string]any{"type": "integer"}, "line": map[string]any{"type": "integer"}, "value": stringSchema,
		}),
		"Finding": object([]string{"id", "category", "outcome", "severity", "basis", "applicability", "label", "detail", "evidence", "evidenceTruncated", "reference"}, map[string]any{
			"id": stringSchema, "category": stringSchema, "outcome": stringSchema, "severity": stringSchema, "basis": stringSchema, "applicability": stringSchema,
			"label": stringSchema, "detail": stringSchema, "evidence": array(ref("Evidence")), "evidenceTruncated": boolean, "reference": nullable(ref("Reference")),
		}),
		"Resource": object([]string{"kind", "path", "url", "text", "occurrenceCount"}, map[string]any{
			"kind": stringSchema, "path": nullable(stringSchema), "url": stringSchema, "text": stringSchema, "occurrenceCount": map[string]any{"type": "integer"},
		}),
		"MimeNode": object([]string{"path", "contentType", "charset", "encoding", "disposition", "filename", "contentId", "rawSize", "decodedSize", "children"}, map[string]any{
			"path": stringSchema, "contentType": stringSchema, "charset": nullable(stringSchema), "encoding": nullable(stringSchema), "disposition": nullable(stringSchema),
			"filename": nullable(stringSchema), "contentId": nullable(stringSchema), "rawSize": nullable(map[string]any{"type": "integer"}), "decodedSize": nullable(map[string]any{"type": "integer"}), "children": array(ref("MimeNode")),
		}),
		"InspectionReport": object([]string{"analysis", "summary", "findings", "resources", "mimeTree"}, map[string]any{
			"analysis": ref("Analysis"), "summary": ref("InspectionSummary"), "findings": array(ref("Finding")), "resources": array(ref("Resource")), "mimeTree": nullable(ref("MimeNode")),
		}),
		"Event": object([]string{"type"}, map[string]any{
			"type":    map[string]any{"type": "string", "enum": []string{"mailbox:new", "mailbox:deleted", "messages:changed", "calendar:changed", "message:new", "reset"}},
			"mailbox": object([]string{"id", "address"}, map[string]any{"id": integer, "address": stringSchema}), "mailboxId": integer,
			"message": object([]string{"id", "subject", "fromAddress", "fromName"}, map[string]any{"id": integer, "subject": nullable(stringSchema), "fromAddress": nullable(stringSchema), "fromName": nullable(stringSchema)}),
		}),
	}

	mailboxes := operation("Mailboxes", "List mailboxes", withSuccess(errorResponses(), "200", "Mailboxes", object([]string{"mailboxes"}, map[string]any{"mailboxes": array(ref("Mailbox"))})))
	deleteMailbox := operation("Mailboxes", "Delete a mailbox and its messages", withSuccess(errorResponses("400", "404"), "200", "Mailbox deleted", ref("Success")))
	deleteMailbox["parameters"] = []any{pathParameter("mailboxId", "Mailbox ID")}
	listMessages := operation("Messages", "List or search mailbox messages", withSuccess(errorResponses("400"), "200", "Messages", object([]string{"messages"}, map[string]any{"messages": array(ref("MessageListItem"))})))
	listMessages["parameters"] = []any{pathParameter("mailboxId", "Mailbox ID"), map[string]any{"name": "q", "in": "query", "description": "Case-insensitive message search", "schema": stringSchema}}
	listEvents := operation("Calendar", "List mailbox calendar events", withSuccess(errorResponses("400"), "200", "Calendar events", object([]string{"events"}, map[string]any{"events": array(ref("CalendarEvent"))})))
	listEvents["parameters"] = []any{pathParameter("mailboxId", "Mailbox ID")}
	getMessage := operation("Messages", "Get a message and mark it read", withSuccess(errorResponses("400", "404"), "200", "Message detail", object([]string{"message", "attachments"}, map[string]any{"message": ref("Message"), "attachments": array(ref("AttachmentInfo"))})))
	getMessage["parameters"] = []any{pathParameter("messageId", "Message ID")}
	getSource := operation("Messages", "Get exact stored RFC 822 source without marking read", errorResponses("400", "404"))
	getSource["responses"].(map[string]any)["200"] = map[string]any{"description": "Raw message source", "content": map[string]any{"message/rfc822": map[string]any{"schema": map[string]any{"type": "string", "format": "binary"}}}}
	getSource["parameters"] = []any{pathParameter("messageId", "Message ID")}
	inspectMessage := operation("Messages", "Analyze a stored message offline", withSuccess(errorResponses("400", "404"), "200", "Inspection report", ref("InspectionReport")))
	inspectMessage["parameters"] = []any{pathParameter("messageId", "Message ID")}
	actions := operation("Messages", "Apply an action to messages", withSuccess(errorResponses("400"), "200", "Action applied", ref("Success")))
	actions["requestBody"] = map[string]any{"required": true, "content": map[string]any{"application/json": map[string]any{"schema": ref("MessageAction")}}}
	attachment := operation("Attachments", "Download or display an attachment", errorResponses("400", "404"))
	attachment["responses"].(map[string]any)["200"] = map[string]any{"description": "Attachment bytes", "content": map[string]any{"application/octet-stream": map[string]any{"schema": map[string]any{"type": "string", "format": "binary"}}}}
	attachment["parameters"] = []any{pathParameter("attachmentId", "Attachment ID"), map[string]any{"name": "download", "in": "query", "description": "Set to 1 to force attachment disposition", "schema": map[string]any{"type": "string", "enum": []string{"1"}}}}
	stream := operation("Events", "Stream live mailbox events", map[string]any{"200": map[string]any{"description": "Server-sent events. Each data field contains an Event JSON object.", "content": map[string]any{"text/event-stream": map[string]any{"schema": map[string]any{"type": "string"}, "example": "data: {\"type\":\"connected\"}\n\n"}}}, "500": errorResponses()["500"]})
	reset := operation("System", "Delete all stored mailboxes and messages", withSuccess(errorResponses(), "200", "Store reset", ref("Success")))
	sendTest := operation("System", "Send a built-in test message through Hoomail SMTP", withSuccess(errorResponses("400", "502"), "200", "Test message sent", ref("Success")))
	sendTest["requestBody"] = map[string]any{"content": map[string]any{"application/json": map[string]any{"schema": ref("SendTestRequest")}}}

	document := map[string]any{
		"openapi": "3.0.3",
		"info":    map[string]any{"title": "Hoomail API", "version": version, "description": "HTTP API for Hoomail mailboxes, messages, attachments, calendar data, and live events."},
		"tags": []any{
			map[string]any{"name": "Mailboxes"}, map[string]any{"name": "Messages"}, map[string]any{"name": "Attachments"}, map[string]any{"name": "Calendar"}, map[string]any{"name": "Events"}, map[string]any{"name": "System"},
		},
		"paths": map[string]any{
			"/api/mailboxes":                      map[string]any{"get": mailboxes},
			"/api/mailboxes/{mailboxId}":          map[string]any{"delete": deleteMailbox},
			"/api/mailboxes/{mailboxId}/messages": map[string]any{"get": listMessages},
			"/api/mailboxes/{mailboxId}/events":   map[string]any{"get": listEvents},
			"/api/messages/{messageId}":           map[string]any{"get": getMessage},
			"/api/messages/{messageId}/source":    map[string]any{"get": getSource},
			"/api/messages/{messageId}/inspect":   map[string]any{"get": inspectMessage},
			"/api/messages/actions":               map[string]any{"post": actions},
			"/api/attachments/{attachmentId}":     map[string]any{"get": attachment},
			"/api/events":                         map[string]any{"get": stream},
			"/api/reset":                          map[string]any{"post": reset},
			"/api/send-test":                      map[string]any{"post": sendTest},
		},
		"components": map[string]any{"schemas": schemas},
	}

	encoded, err := json.Marshal(document)
	if err != nil {
		panic(err)
	}
	if err := os.WriteFile("openapi.json", append(encoded, '\n'), 0o644); err != nil {
		panic(err)
	}
	fmt.Printf("generated openapi.json (%d bytes)\n", len(encoded)+1)
}
