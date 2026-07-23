package calendar

import (
	"regexp"
	"strconv"
	"strings"
	"time"
)

const (
	MethodPublish = "PUBLISH"
	MethodRequest = "REQUEST"
	MethodCancel  = "CANCEL"
	MethodReply   = "REPLY"
)

type Attendee = ParsedAttendee

type ParsedAttendee struct {
	Address  string  `json:"address"`
	Name     *string `json:"name,omitempty"`
	Partstat *string `json:"partstat,omitempty"`
	Role     *string `json:"role,omitempty"`
}

type ParsedCalendarEvent struct {
	Method           string           `json:"method"`
	UID              string           `json:"uid"`
	Sequence         int              `json:"sequence"`
	Summary          *string          `json:"summary"`
	Description      *string          `json:"description"`
	Location         *string          `json:"location"`
	Status           *string          `json:"status"`
	OrganizerAddress *string          `json:"organizerAddress"`
	OrganizerName    *string          `json:"organizerName"`
	Attendees        []ParsedAttendee `json:"attendees"`
	DTStart          int64            `json:"dtstart"`
	DTEnd            *int64           `json:"dtend"`
	AllDay           bool             `json:"allDay"`
}

type Event = ParsedCalendarEvent

func (event ParsedCalendarEvent) DedupKey() string {
	return event.UID + "|" + strconv.Itoa(event.Sequence) + "|" + event.Method
}

func IsCalendarPart(contentType, filename string) bool {
	contentType = strings.ToLower(contentType)
	if strings.Contains(contentType, "text/calendar") || strings.Contains(contentType, "application/ics") {
		return true
	}
	return strings.HasSuffix(strings.ToLower(filename), ".ics")
}

func Parse(text string) []ParsedCalendarEvent {
	return ParseICS(text)
}

func ParseIcs(text string) []ParsedCalendarEvent {
	return ParseICS(text)
}

func IsPart(contentType, filename string) bool {
	return IsCalendarPart(contentType, filename)
}

func ParseICS(text string) []ParsedCalendarEvent {
	lines, ok := unfoldLines(text)
	if !ok {
		return []ParsedCalendarEvent{}
	}

	method := MethodPublish
	stack := make([]string, 0, 4)
	events := make([]ParsedCalendarEvent, 0)
	var current *eventBuilder
	seenCalendar := false
	closedCalendar := false

	for _, raw := range lines {
		if raw == "" {
			continue
		}
		line, ok := parseContentLine(raw)
		if !ok {
			return []ParsedCalendarEvent{}
		}

		switch line.name {
		case "BEGIN":
			component := strings.ToUpper(line.value)
			if len(stack) == 0 {
				if component != "VCALENDAR" || seenCalendar {
					return []ParsedCalendarEvent{}
				}
				seenCalendar = true
			} else if stack[len(stack)-1] == "VCALENDAR" && component == "VEVENT" {
				current = newEventBuilder()
			}
			stack = append(stack, component)
			continue
		case "END":
			component := strings.ToUpper(line.value)
			if len(stack) == 0 || stack[len(stack)-1] != component {
				return []ParsedCalendarEvent{}
			}
			if component == "VEVENT" && current != nil {
				event, include, valid := current.build(method)
				if !valid {
					return []ParsedCalendarEvent{}
				}
				if include {
					events = append(events, event)
				}
				current = nil
			}
			stack = stack[:len(stack)-1]
			if component == "VCALENDAR" {
				closedCalendar = true
			}
			continue
		}

		if len(stack) == 1 && stack[0] == "VCALENDAR" && line.name == "METHOD" {
			if value := strings.ToUpper(line.value); value != "" {
				method = value
			}
			continue
		}
		if current != nil && len(stack) > 0 && stack[len(stack)-1] == "VEVENT" {
			current.add(line)
		}
	}

	if !seenCalendar || !closedCalendar || len(stack) != 0 {
		return []ParsedCalendarEvent{}
	}
	return events
}

type contentLine struct {
	name   string
	value  string
	params map[string][]string
}

type eventBuilder struct {
	properties map[string]contentLine
	attendees  []contentLine
}

func newEventBuilder() *eventBuilder {
	return &eventBuilder{
		properties: make(map[string]contentLine),
		attendees:  make([]contentLine, 0),
	}
}

func (builder *eventBuilder) add(line contentLine) {
	if line.name == "ATTENDEE" {
		builder.attendees = append(builder.attendees, line)
		return
	}
	if _, exists := builder.properties[line.name]; !exists {
		builder.properties[line.name] = line
	}
}

func (builder *eventBuilder) build(method string) (ParsedCalendarEvent, bool, bool) {
	uidLine, hasUID := builder.properties["UID"]
	startLine, hasStart := builder.properties["DTSTART"]
	if !hasUID || !hasStart {
		return ParsedCalendarEvent{}, false, true
	}
	uid := decodeText(uidLine.value)
	if uid == "" {
		return ParsedCalendarEvent{}, false, true
	}

	start, allDay, err := parseCalendarTime(startLine)
	if err != nil {
		return ParsedCalendarEvent{}, false, false
	}
	var endMillis *int64
	if endLine, ok := builder.properties["DTEND"]; ok {
		end, _, err := parseCalendarTime(endLine)
		if err != nil {
			return ParsedCalendarEvent{}, false, false
		}
		value := end.UnixMilli()
		endMillis = &value
	} else if durationLine, ok := builder.properties["DURATION"]; ok {
		duration, valid := parseDuration(durationLine.value)
		if !valid {
			return ParsedCalendarEvent{}, false, false
		}
		value := duration.addTo(start).UnixMilli()
		endMillis = &value
	} else if allDay {
		value := start.AddDate(0, 0, 1).UnixMilli()
		endMillis = &value
	}

	event := ParsedCalendarEvent{
		Method:      strings.ToUpper(method),
		UID:         uid,
		Sequence:    parseSequence(builder.properties["SEQUENCE"].value),
		Summary:     optionalText(builder.properties["SUMMARY"]),
		Description: optionalText(builder.properties["DESCRIPTION"]),
		Location:    optionalText(builder.properties["LOCATION"]),
		Status:      optionalUpper(builder.properties["STATUS"]),
		Attendees:   make([]ParsedAttendee, 0, len(builder.attendees)),
		DTStart:     start.UnixMilli(),
		DTEnd:       endMillis,
		AllDay:      allDay,
	}

	if organizer, ok := builder.properties["ORGANIZER"]; ok {
		event.OrganizerAddress = mailto(organizer.value)
		event.OrganizerName = optionalParameter(organizer.params, "CN", false)
	}
	for _, attendeeLine := range builder.attendees {
		address := mailto(attendeeLine.value)
		if address == nil {
			continue
		}
		event.Attendees = append(event.Attendees, ParsedAttendee{
			Address:  *address,
			Name:     optionalParameter(attendeeLine.params, "CN", false),
			Partstat: optionalParameter(attendeeLine.params, "PARTSTAT", true),
			Role:     optionalParameter(attendeeLine.params, "ROLE", true),
		})
	}

	return event, true, true
}

func unfoldLines(text string) ([]string, bool) {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	text = strings.ReplaceAll(text, "\r", "\n")
	physical := strings.Split(text, "\n")
	lines := make([]string, 0, len(physical))
	for _, line := range physical {
		if strings.HasPrefix(line, " ") || strings.HasPrefix(line, "\t") {
			if len(lines) == 0 {
				return nil, false
			}
			lines[len(lines)-1] += line[1:]
			continue
		}
		lines = append(lines, line)
	}
	return lines, true
}

func parseContentLine(raw string) (contentLine, bool) {
	colon := separatorIndex(raw, ':')
	if colon <= 0 {
		return contentLine{}, false
	}
	parts := splitOutsideQuotes(raw[:colon], ';')
	name := strings.ToUpper(parts[0])
	if name == "" {
		return contentLine{}, false
	}
	line := contentLine{name: name, value: raw[colon+1:], params: make(map[string][]string)}
	for _, part := range parts[1:] {
		equals := separatorIndex(part, '=')
		if equals <= 0 {
			return contentLine{}, false
		}
		key := strings.ToUpper(part[:equals])
		values := splitOutsideQuotes(part[equals+1:], ',')
		for index := range values {
			values[index] = decodeParameter(values[index])
		}
		line.params[key] = values
	}
	return line, true
}

func separatorIndex(value string, separator byte) int {
	quoted := false
	escaped := false
	for index := range len(value) {
		character := value[index]
		if escaped {
			escaped = false
			continue
		}
		if character == '\\' {
			escaped = true
			continue
		}
		if character == '"' {
			quoted = !quoted
			continue
		}
		if character == separator && !quoted {
			return index
		}
	}
	return -1
}

func splitOutsideQuotes(value string, separator byte) []string {
	parts := make([]string, 0, 4)
	start := 0
	quoted := false
	escaped := false
	for index := range len(value) {
		character := value[index]
		if escaped {
			escaped = false
			continue
		}
		if character == '\\' {
			escaped = true
			continue
		}
		if character == '"' {
			quoted = !quoted
			continue
		}
		if character == separator && !quoted {
			parts = append(parts, value[start:index])
			start = index + 1
		}
	}
	return append(parts, value[start:])
}

func decodeParameter(value string) string {
	if len(value) >= 2 && value[0] == '"' && value[len(value)-1] == '"' {
		value = value[1 : len(value)-1]
	}
	value = strings.ReplaceAll(value, "^^", "\x00")
	value = strings.ReplaceAll(value, "^n", "\n")
	value = strings.ReplaceAll(value, "^N", "\n")
	value = strings.ReplaceAll(value, "^'", "\"")
	value = strings.ReplaceAll(value, "\x00", "^")
	value = strings.ReplaceAll(value, "\\\"", "\"")
	value = strings.ReplaceAll(value, "\\\\", "\\")
	return value
}

func decodeText(value string) string {
	var decoded strings.Builder
	decoded.Grow(len(value))
	for index := 0; index < len(value); index++ {
		if value[index] != '\\' || index+1 >= len(value) {
			decoded.WriteByte(value[index])
			continue
		}
		index++
		switch value[index] {
		case 'n', 'N':
			decoded.WriteByte('\n')
		case '\\', ',', ';':
			decoded.WriteByte(value[index])
		default:
			decoded.WriteByte(value[index])
		}
	}
	return decoded.String()
}

func mailto(value string) *string {
	if value == "" {
		return nil
	}
	if len(value) >= len("mailto:") && strings.EqualFold(value[:len("mailto:")], "mailto:") {
		value = value[len("mailto:"):]
	}
	value = strings.ToLower(value)
	if value == "" {
		return nil
	}
	return &value
}

func optionalText(line contentLine) *string {
	value := decodeText(line.value)
	if value == "" {
		return nil
	}
	return &value
}

func optionalUpper(line contentLine) *string {
	value := strings.ToUpper(line.value)
	if value == "" {
		return nil
	}
	return &value
}

func optionalParameter(params map[string][]string, name string, upper bool) *string {
	values := params[name]
	if len(values) == 0 || values[0] == "" {
		return nil
	}
	value := values[0]
	if upper {
		value = strings.ToUpper(value)
	}
	return &value
}

func parseSequence(value string) int {
	sequence, err := strconv.Atoi(value)
	if err != nil {
		return 0
	}
	return sequence
}

func parseCalendarTime(line contentLine) (time.Time, bool, error) {
	value := line.value
	valueType := firstParameter(line.params, "VALUE")
	allDay := strings.EqualFold(valueType, "DATE") || valueType == "" && (len(value) == 8 || len(value) == 9 && strings.HasSuffix(strings.ToUpper(value), "Z"))
	if allDay {
		value = strings.TrimSuffix(strings.ToUpper(value), "Z")
		parsed, err := time.ParseInLocation("20060102", value, time.Local)
		return parsed, true, err
	}

	if strings.HasSuffix(strings.ToUpper(value), "Z") {
		parsed, err := time.ParseInLocation("20060102T150405Z", strings.ToUpper(value), time.UTC)
		return parsed, false, err
	}
	location := time.Local
	if timezoneID := firstParameter(line.params, "TZID"); timezoneID != "" {
		if loaded, err := time.LoadLocation(timezoneID); err == nil {
			location = loaded
		}
	}
	parsed, err := time.ParseInLocation("20060102T150405", value, location)
	return parsed, false, err
}

func firstParameter(params map[string][]string, name string) string {
	values := params[name]
	if len(values) == 0 {
		return ""
	}
	return values[0]
}

type calendarDuration struct {
	sign                    int
	weeks, days             int
	hours, minutes, seconds int
}

var durationPattern = regexp.MustCompile(`^([+-])?P(?:(\d+)W)?(?:(\d+)D)?(?:T(?:(\d+)H)?(?:(\d+)M)?(?:(\d+)S)?)?$`)

func parseDuration(value string) (calendarDuration, bool) {
	matches := durationPattern.FindStringSubmatch(strings.ToUpper(value))
	if matches == nil {
		return calendarDuration{}, false
	}
	duration := calendarDuration{sign: 1}
	if matches[1] == "-" {
		duration.sign = -1
	}
	fields := []*int{&duration.weeks, &duration.days, &duration.hours, &duration.minutes, &duration.seconds}
	hasValue := false
	for index, field := range fields {
		if matches[index+2] == "" {
			continue
		}
		parsed, err := strconv.Atoi(matches[index+2])
		if err != nil {
			return calendarDuration{}, false
		}
		*field = parsed
		hasValue = true
	}
	return duration, hasValue
}

func (duration calendarDuration) addTo(value time.Time) time.Time {
	days := duration.sign * (duration.weeks*7 + duration.days)
	value = value.AddDate(0, 0, days)
	clock := time.Duration(duration.sign) * (time.Duration(duration.hours)*time.Hour + time.Duration(duration.minutes)*time.Minute + time.Duration(duration.seconds)*time.Second)
	return value.Add(clock)
}
