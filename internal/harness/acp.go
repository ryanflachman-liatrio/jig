package harness

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync/atomic"

	acpsdk "github.com/coder/acp-go-sdk"

	"jig/harness/acp"
	"jig/internal/interaction"
)

// AcpHarness drives Claude over the Agent Client Protocol via Zed's npx
// adapter, reusing harness/acp's proven Connect/NewSession/Prompt connection
// code (see ADR 0010, ADR 0011, and Unit 1's spike) instead of
// re-implementing the handshake. This file and the harness/acp module it
// imports are the only places acp-go-sdk is imported (dependency
// confinement — see the spec's success metric 5).
type AcpHarness struct{}

// NewAcpHarness returns an AcpHarness ready to use.
func NewAcpHarness() *AcpHarness { return &AcpHarness{} }

func (*AcpHarness) Name() string { return "acp" }

// Capabilities advertises what this harness actually implements:
//   - CapPermissionCallback — real permission-decision round-trips.
//   - CapUserQuestion — ACP form elicitation round-trips.
//   - CapPartialStreaming — EventText emitted for each text chunk.
//   - CapStructuredOutput — schema injected into the prompt; JSON extracted
//     from the agent's response with an automatic retry loop on parse failure.
//
// Session resume is not implemented and is omitted rather than stubbed true.
func (*AcpHarness) Capabilities() CapabilitySet {
	return NewCapabilitySet(CapPermissionCallback, CapUserQuestion, CapPartialStreaming, CapStructuredOutput)
}

// Open spawns the adapter, opens a session at spec.Cwd, and starts the
// prompt turn in the background so Messages() can begin delivering events
// immediately. It rejects any capability-gated SessionSpec field this
// harness does not advertise, rather than silently ignoring it.
func (h *AcpHarness) Open(ctx context.Context, spec SessionSpec) (Session, error) {
	if spec.Resume != "" {
		return nil, fmt.Errorf("acp: session resume not supported (CapSessionResume not advertised)")
	}

	events := make(chan Event, 32)
	sess := &acpSession{events: events, hasSchema: spec.Schema != nil, schema: spec.Schema}

	var decide acp.Decider
	if spec.Permission != nil {
		decide = func(tc acpsdk.ToolCallUpdate) bool {
			return spec.Permission(toolCallName(tc), toolCallInput(tc)).Allow
		}
	}

	var elicit acp.Elicitor
	if spec.Question != nil {
		elicit = newACPElicitor(spec.Question)
	}
	conn, err := acp.Connect(ctx, decide, func(ev acp.Event) {
		sess.onEvent(ev)
	}, elicit)
	if err != nil {
		return nil, fmt.Errorf("acp: %w", err)
	}
	sess.conn = conn

	sessionID, err := conn.NewSession(ctx, spec.Cwd)
	if err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("acp: %w", err)
	}
	events <- Event{Type: EventSessionID, SessionID: sessionID}

	go sess.run(ctx, sessionID, spec.Prompt)
	return sess, nil
}

func newACPElicitor(ask QuestionFn) acp.Elicitor {
	var seq atomic.Uint64
	return func(
		ctx context.Context,
		params acpsdk.UnstableCreateElicitationRequest,
	) (acpsdk.UnstableCreateElicitationResponse, error) {
		req, companions, err := parseACPQuestion(params, seq.Add(1))
		if err != nil {
			return acpsdk.UnstableCreateElicitationResponse{}, err
		}
		resp := ask(ctx, req)
		return encodeACPQuestionResponse(req, companions, resp)
	}
}

func parseACPQuestion(
	params acpsdk.UnstableCreateElicitationRequest,
	id uint64,
) (interaction.QuestionRequest, map[string]string, error) {
	if params.Form == nil {
		return interaction.QuestionRequest{}, nil, fmt.Errorf("acp: only form elicitation is supported")
	}
	form := params.Form
	keys := make([]string, 0, len(form.RequestedSchema.Properties))
	for key := range form.RequestedSchema.Properties {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool {
		left, leftOK := acpQuestionIndex(keys[i])
		right, rightOK := acpQuestionIndex(keys[j])
		if leftOK && rightOK && left != right {
			return left < right
		}
		if leftOK != rightOK {
			return leftOK
		}
		return keys[i] < keys[j]
	})

	customByField := make(map[string]string)
	customKeys := make(map[string]bool)
	for _, key := range keys {
		prop, err := acpProperty(form.RequestedSchema.Properties[key])
		if err != nil {
			return interaction.QuestionRequest{}, nil, fmt.Errorf("acp: elicitation field %q: %w", key, err)
		}
		meta, _ := prop["_meta"].(map[string]any)
		marker, _ := meta["_askUserQuestionCustomAnswer"].(map[string]any)
		questionID, _ := marker["questionId"].(string)
		isCustom, _ := marker["isCustomAnswer"].(bool)
		if isCustom && questionID != "" {
			customByField[questionID] = key
			customKeys[key] = true
		}
	}

	required := make(map[string]bool, len(form.RequestedSchema.Required))
	for _, key := range form.RequestedSchema.Required {
		required[key] = true
	}
	req := interaction.QuestionRequest{
		ID:      fmt.Sprintf("acp-question-%d", id),
		Message: form.Message,
	}
	for _, key := range keys {
		if customKeys[key] {
			continue
		}
		prop, _ := acpProperty(form.RequestedSchema.Properties[key])
		field, err := acpQuestionField(key, prop)
		if err != nil {
			return interaction.QuestionRequest{}, nil, fmt.Errorf("acp: elicitation field %q: %w", key, err)
		}
		field.Required = required[key]
		_, field.AllowCustom = customByField[key]
		req.Fields = append(req.Fields, field)
	}
	if len(req.Fields) == 1 && strings.TrimSpace(req.Fields[0].Description) == "" &&
		strings.TrimSpace(form.Message) != "" {
		req.Fields[0].Prompt = form.Message
	}
	if err := req.Validate(); err != nil {
		return interaction.QuestionRequest{}, nil, fmt.Errorf("acp: invalid elicitation: %w", err)
	}
	return req, customByField, nil
}

func acpQuestionIndex(key string) (int, bool) {
	suffix, ok := strings.CutPrefix(key, "question_")
	if !ok {
		return 0, false
	}
	if before, _, found := strings.Cut(suffix, "_"); found {
		suffix = before
	}
	index, err := strconv.Atoi(suffix)
	return index, err == nil
}

func acpProperty(raw any) (map[string]any, error) {
	if prop, ok := raw.(map[string]any); ok {
		return prop, nil
	}
	data, err := json.Marshal(raw)
	if err != nil {
		return nil, err
	}
	var prop map[string]any
	if err := json.Unmarshal(data, &prop); err != nil {
		return nil, err
	}
	return prop, nil
}

func acpQuestionField(id string, prop map[string]any) (interaction.QuestionField, error) {
	kind, _ := prop["type"].(string)
	field := interaction.QuestionField{
		ID:          id,
		Header:      stringValue(prop["title"]),
		Prompt:      stringValue(prop["description"]),
		Description: stringValue(prop["description"]),
	}
	switch kind {
	case "string":
		options, err := acpOptions(prop["oneOf"], prop["enum"])
		if err != nil {
			return interaction.QuestionField{}, err
		}
		if len(options) == 0 {
			field.Kind = interaction.FieldText
		} else {
			field.Kind = interaction.FieldSingleSelect
			field.Options = options
		}
	case "array":
		items, ok := prop["items"].(map[string]any)
		if !ok {
			return interaction.QuestionField{}, fmt.Errorf("array field requires object items")
		}
		options, err := acpOptions(items["anyOf"], items["enum"])
		if err != nil {
			return interaction.QuestionField{}, err
		}
		if len(options) == 0 {
			return interaction.QuestionField{}, fmt.Errorf("array field requires enum options")
		}
		field.Kind = interaction.FieldMultiSelect
		field.Options = options
	default:
		return interaction.QuestionField{}, fmt.Errorf("unsupported field type %q", kind)
	}
	if strings.TrimSpace(field.Prompt) == "" {
		field.Prompt = field.Header
	}
	if strings.TrimSpace(field.Prompt) == "" {
		field.Prompt = id
	}
	return field, nil
}

func acpOptions(oneOf, enum any) ([]interaction.QuestionOption, error) {
	if raw, ok := oneOf.([]any); ok {
		options := make([]interaction.QuestionOption, 0, len(raw))
		for _, item := range raw {
			obj, err := acpProperty(item)
			if err != nil {
				return nil, fmt.Errorf("invalid enum option: %w", err)
			}
			value := stringValue(obj["const"])
			label := stringValue(obj["title"])
			if label == "" {
				label = value
			}
			options = append(options, interaction.QuestionOption{
				Value:       value,
				Label:       label,
				Description: stringValue(obj["description"]),
			})
		}
		return options, nil
	}
	if raw, ok := enum.([]any); ok {
		options := make([]interaction.QuestionOption, 0, len(raw))
		for _, item := range raw {
			value, ok := item.(string)
			if !ok {
				return nil, fmt.Errorf("only string enum options are supported")
			}
			options = append(options, interaction.QuestionOption{Value: value, Label: value})
		}
		return options, nil
	}
	return nil, nil
}

func stringValue(v any) string {
	s, _ := v.(string)
	return s
}

func encodeACPQuestionResponse(
	req interaction.QuestionRequest,
	companions map[string]string,
	resp interaction.QuestionResponse,
) (acpsdk.UnstableCreateElicitationResponse, error) {
	if err := resp.Validate(req); err != nil {
		return acpsdk.UnstableCreateElicitationResponse{}, fmt.Errorf("acp: invalid elicitation response: %w", err)
	}
	switch resp.Action {
	case interaction.ActionCancel:
		return acpsdk.NewUnstableCreateElicitationResponseCancel(), nil
	case interaction.ActionDecline:
		return acpsdk.NewUnstableCreateElicitationResponseDecline(), nil
	}
	content := make(map[string]any, len(resp.Answers))
	for _, field := range req.Fields {
		answer, ok := resp.Answers[field.ID]
		if !ok {
			continue
		}
		if answer.Custom != "" {
			key := companions[field.ID]
			if key == "" && field.Kind == interaction.FieldText {
				key = field.ID
			}
			if key != "" {
				content[key] = answer.Custom
			}
			continue
		}
		switch field.Kind {
		case interaction.FieldMultiSelect:
			content[field.ID] = append([]string(nil), answer.Values...)
		default:
			if len(answer.Values) > 0 {
				content[field.ID] = answer.Values[0]
			}
		}
	}
	out := acpsdk.NewUnstableCreateElicitationResponseAccept()
	out.Accept.Content = content
	return out, nil
}

// acpSession adapts an acp.Conn's single-turn Prompt call to harness.Session.
// When hasSchema is true, run() injects the JSON schema into the prompt and
// parses the accumulated assistant text as JSON, retrying up to
// acpMaxStructuredAttempts times on parse failure.
//
// Event translation is stateful: text chunks accumulate until a natural
// boundary (first new tool call or end of turn), at which point a single
// EventAssistantEnd is emitted — mirroring how ClaudeHarness groups all blocks
// in one AssistantMessage into one transcript entry. Tool calls are buffered by
// ID and emitted once via EventToolUse (which carries the final title),
// eliminating duplicate entries from the ACP adapter's streaming title updates.
//
// All fields written by onEvent and read by run() are safe from concurrent
// access: the ACP SDK invokes onUpdate synchronously, and all callbacks
// complete before Prompt returns — so run() reads these fields only after the
// last callback has fired.
type acpSession struct {
	conn      *acp.Conn
	events    chan Event
	hasSchema bool
	schema    map[string]any

	// lastText accumulates EventMessage chunks for structured-output extraction.
	// Reset when the first new tool call ID is seen so only the final text
	// response (after all tool results) is captured.
	lastText string

	// pendingTools maps tool_id → most-recently-seen title. The ACP adapter
	// streams tool call starts and title updates as separate EventToolCall
	// events; we buffer them here and emit a single EventToolUse only when the
	// corresponding EventToolCallUpdate (the result) arrives.
	pendingTools map[string]string

	// hasTextSinceFlush is true whenever text or thinking events have been
	// emitted since the last EventAssistantEnd. Used to decide whether to flush
	// before the first new tool call, and to flush any trailing text after
	// Prompt returns.
	hasTextSinceFlush bool
}

func (s *acpSession) Messages() <-chan Event { return s.events }

// Send is a no-op: ACP's Prompt already blocks for the whole turn (including
// any permission round-trips), so there is no mid-session injection point
// analogous to ClaudeHarness's AskUserQuestion tool-result channel.
func (s *acpSession) Send(_ context.Context, _ ToolResult) error {
	return fmt.Errorf("acp: mid-session Send not supported")
}

func (s *acpSession) Close() error {
	return s.conn.Close()
}

// acpMaxStructuredAttempts caps the prompt-inject → parse → retry loop so a
// persistently non-compliant model does not spin indefinitely.
const acpMaxStructuredAttempts = 3

func (s *acpSession) run(ctx context.Context, sessionID, prompt string) {
	defer close(s.events)

	if s.schema == nil {
		// No structured output required: single turn, no extraction.
		stopReason, err := s.conn.Prompt(ctx, sessionID, prompt)
		s.flushText()
		if err != nil {
			s.events <- Event{Type: EventResult, IsError: true, ErrText: err.Error(), SessionID: sessionID}
			return
		}
		s.events <- Event{Type: EventResult, SessionID: sessionID, Subtype: string(stopReason)}
		return
	}

	// Structured output: inject the schema into the initial prompt, then call
	// Prompt and try to extract JSON from the accumulated response text.
	// On parse failure, emit a user-turn retry message (so it appears in the
	// transcript) and call Prompt again with that message as the new turn.
	//
	// Intermediate content (thinking, tool calls, mid-turn prose) is handled
	// transparently: onEvent resets lastText at the first new tool call so only
	// the final text response (after all tool results) is captured for JSON
	// extraction.
	currentPrompt := appendSchemaPrompt(prompt, s.schema)
	var (
		lastParseErr string
		subtype      string
	)

	for attempt := 0; attempt < acpMaxStructuredAttempts; attempt++ {
		s.lastText = ""
		stopReason, err := s.conn.Prompt(ctx, sessionID, currentPrompt)
		// Capture accumulated text before flushing so extractJSONFromText sees
		// the final assistant response.
		text := s.lastText
		s.flushText()

		if err != nil {
			s.events <- Event{Type: EventResult, IsError: true, ErrText: err.Error(), SessionID: sessionID}
			return
		}
		subtype = string(stopReason)

		parsed, parseErr := extractJSONFromText(text)
		if parseErr == nil {
			s.events <- Event{
				Type:       EventResult,
				SessionID:  sessionID,
				Subtype:    subtype,
				Structured: parsed,
			}
			return
		}
		lastParseErr = parseErr.Error()

		if attempt < acpMaxStructuredAttempts-1 {
			// Emit the retry message as a user turn so the transcript shows
			// what jig sent back to the model.
			retryMsg := buildStructuredRetryPrompt(attempt+1, lastParseErr)
			s.events <- Event{Type: EventText, Text: retryMsg}
			s.events <- Event{Type: EventUserEnd}
			currentPrompt = retryMsg
		}
	}

	s.events <- Event{
		Type:      EventResult,
		IsError:   true,
		ErrText:   fmt.Sprintf("acp: structured output: no valid JSON after %d attempts: %s", acpMaxStructuredAttempts, lastParseErr),
		SessionID: sessionID,
	}
}

// appendSchemaPrompt returns prompt with a structured-output requirement section
// appended. The agent is instructed to place a ```json fenced block as the last
// content of its response so extractJSONFromText can locate it reliably.
func appendSchemaPrompt(prompt string, schema map[string]any) string {
	schemaJSON, err := json.MarshalIndent(schema, "", "  ")
	if err != nil {
		// MarshalIndent on a map[string]any produced by json.Unmarshal cannot
		// fail in practice; fall back without injection so the step still runs.
		return prompt
	}
	var b strings.Builder
	b.WriteString(prompt)
	b.WriteString("\n\n## Structured Output Required\n\n")
	b.WriteString("After completing your task, your response MUST end with a JSON object that conforms to the following JSON Schema:\n\n")
	b.WriteString("```json-schema\n")
	b.Write(schemaJSON)
	b.WriteString("\n```\n\n")
	b.WriteString("Output your JSON as the **last** content in your response, in a fenced code block:\n\n")
	b.WriteString("```json\n{your output here}\n```\n\n")
	b.WriteString("Do not include any text after the closing ``` of the JSON block.")
	return b.String()
}

// buildStructuredRetryPrompt constructs the user-turn message sent when JSON
// extraction fails. attempt is 1-indexed (the number of the attempt that failed).
func buildStructuredRetryPrompt(attempt int, parseErr string) string {
	return fmt.Sprintf(
		"Your previous response (attempt %d) did not contain valid JSON in a ```json fenced block.\n\n"+
			"Parse error: %s\n\n"+
			"Please provide your structured output again. "+
			"End your response with the required JSON wrapped in a ```json fenced code block "+
			"as the last content, with no text after the closing ```.",
		attempt, parseErr,
	)
}

// extractJSONFromText locates the last ```json...``` fenced block in text and
// parses its content as JSON. If no valid fenced block is found it falls back
// to parsing the entire trimmed text as JSON.
func extractJSONFromText(text string) (json.RawMessage, error) {
	const opener = "```json"
	const closer = "```"

	// LastIndex picks the final block so preceding prose or quoted examples
	// do not shadow the agent's actual output.
	lastOpen := strings.LastIndex(text, opener)
	if lastOpen >= 0 {
		after := text[lastOpen+len(opener):]
		// Skip the newline that conventionally follows the opener.
		after = strings.TrimLeft(after, "\r\n")
		if closeIdx := strings.Index(after, closer); closeIdx >= 0 {
			candidate := strings.TrimSpace(after[:closeIdx])
			var raw json.RawMessage
			if err := json.Unmarshal([]byte(candidate), &raw); err == nil {
				return raw, nil
			}
		}
	}

	// Fallback: the model emitted bare JSON without fence markers.
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return nil, fmt.Errorf("response was empty")
	}
	var raw json.RawMessage
	if err := json.Unmarshal([]byte(trimmed), &raw); err != nil {
		return nil, fmt.Errorf("no valid JSON found in response: %w", err)
	}
	return raw, nil
}

// flushText emits EventAssistantEnd if any text or thinking has been emitted
// since the last flush, grouping all preceding chunks into one transcript entry.
func (s *acpSession) flushText() {
	if s.hasTextSinceFlush {
		s.events <- Event{Type: EventAssistantEnd}
		s.hasTextSinceFlush = false
	}
}

// onEvent translates one ACP event into harness events with stateful grouping:
//
//   - Text and thinking chunks (EventMessage, EventThought) are forwarded
//     individually as EventText/EventThinking — no EventAssistantEnd per chunk.
//   - The first new tool call ID flushes any preceding text (one AssistantEnd),
//     then buffers the tool's title. Subsequent EventToolCall events for the
//     same ID only update the buffered title — no extra flush, no extra entry.
//   - EventToolCallUpdate (the tool result) emits the buffered EventToolUse
//     with the final title, then AssistantEnd, then the result and UserEnd.
//   - After Prompt returns, run() calls flushText() to close the final turn.
//
// This mirrors ClaudeHarness.pump, which groups all blocks within one SDK
// AssistantMessage into a single transcript entry via a single AssistantEnd.
func (s *acpSession) onEvent(ev acp.Event) {
	switch ev.Kind {
	case acp.EventMessage:
		if s.hasSchema {
			s.lastText += ev.Text
		}
		s.events <- Event{Type: EventText, Text: ev.Text}
		s.hasTextSinceFlush = true

	case acp.EventThought:
		s.events <- Event{Type: EventThinking, Text: ev.Text}
		s.hasTextSinceFlush = true

	case acp.EventToolCall:
		if s.pendingTools == nil {
			s.pendingTools = make(map[string]string)
		}
		_, existed := s.pendingTools[ev.ToolID]
		s.pendingTools[ev.ToolID] = ev.Title
		if !existed {
			// First time seeing this tool ID: flush any preceding text so it
			// lands in its own assistant entry, separate from the tool calls.
			s.flushText()
			if s.hasSchema {
				// Reset accumulator so only the final text response (after all
				// tool results) is captured for JSON extraction.
				s.lastText = ""
			}
		}
		// Subsequent EventToolCall events for the same ID are title updates;
		// they update pendingTools[id] and do nothing else. The EventToolUse
		// is emitted once, when EventToolCallUpdate arrives with the result.

	case acp.EventToolCallUpdate:
		title := ""
		if s.pendingTools != nil {
			title = s.pendingTools[ev.ToolID]
			delete(s.pendingTools, ev.ToolID)
		}
		s.events <- Event{Type: EventToolUse, ToolUseID: ev.ToolID, Name: title}
		s.events <- Event{Type: EventAssistantEnd}
		s.events <- Event{Type: EventToolResult, ToolUseID: ev.ToolID, Content: ev.Status, IsError: ev.Status == "failed"}
		s.events <- Event{Type: EventUserEnd}
	}
}

// toolCallName returns the human-readable tool name a permission decision is
// keyed on. ACP's ToolCallUpdate carries only a Title (no separate machine
// tool name), so Title stands in for both.
func toolCallName(tc acpsdk.ToolCallUpdate) string {
	if tc.Title != nil {
		return *tc.Title
	}
	return ""
}

// toolCallInput extracts the tool call's raw input as a map, matching
// PermissionFn's signature. A non-object RawInput (or none at all) yields an
// empty map rather than an error — the permission decision still runs, just
// without argument detail to inspect.
func toolCallInput(tc acpsdk.ToolCallUpdate) map[string]any {
	if m, ok := tc.RawInput.(map[string]any); ok {
		return m
	}
	return map[string]any{}
}

var (
	_ Harness = (*AcpHarness)(nil)
	_ Session = (*acpSession)(nil)
)
