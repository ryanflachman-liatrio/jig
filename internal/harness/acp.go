package harness

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"
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
//   - CapPartialStreaming — EventTextDelta emitted for each text chunk.
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

	events := make(chan Event, 16)
	tr := &acpTranslator{out: events}
	sess := &acpSession{events: events, tr: tr, schema: spec.Schema}

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
		tr.handle(ev)
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

// acpSession adapts an acp.Conn's single-turn Prompt call to harness.Session:
// run streams translated events as they arrive and closes events with a
// terminal EventResult once the turn completes. When schema is non-nil the run
// loop injects the schema into the prompt and retries if the response does not
// contain parseable JSON.
type acpSession struct {
	conn   *acp.Conn
	events chan Event
	tr     *acpTranslator
	schema map[string]any // non-nil when CapStructuredOutput was requested
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
		s.tr.flushAll()
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
	// transparently: acpTranslator flushes and resets textBuf at every state
	// transition (thought→text, text→tool call). By the time Prompt returns,
	// AccumulatedText() holds only the agent's *final* text segment — the one
	// after its last tool call — which is exactly where the schema instruction
	// directs it to place the JSON. All prior segments are already in the
	// events channel (and thus in the transcript) from those earlier flushes.
	currentPrompt := appendSchemaPrompt(prompt, s.schema)
	var (
		lastParseErr string
		subtype      string
	)

	for attempt := 0; attempt < acpMaxStructuredAttempts; attempt++ {
		stopReason, err := s.conn.Prompt(ctx, sessionID, currentPrompt)
		// Capture the accumulated text before flushAll clears the buffer.
		text := s.tr.AccumulatedText()
		s.tr.flushAll()

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

// acpTranslator accumulates streaming ACP text/thought chunks into jig harness
// Events. ACP delivers message content as many small EventMessage/EventThought
// notifications (streaming); this buffers them and emits a single EventText or
// EventThinking entry per turn rather than one entry per chunk.
//
// Invariant: at most one of textBuf/thinkBuf is non-empty at a time. A
// transition from text→thought or thought→text flushes the previous buffer
// first. EventToolCall flushes both. flushAll flushes both at turn end.
//
// EventTextDelta is emitted for each raw EventMessage chunk so the TUI's
// live-typing tail updates in real time even though the finalised EventText
// entry only arrives at flush time.
type acpTranslator struct {
	out      chan<- Event
	mu       sync.Mutex
	textBuf  strings.Builder
	thinkBuf strings.Builder
}

func (t *acpTranslator) handle(ev acp.Event) {
	t.mu.Lock()
	defer t.mu.Unlock()
	switch ev.Kind {
	case acp.EventMessage:
		t.flushThinkLocked()
		t.textBuf.WriteString(ev.Text)
		t.out <- Event{Type: EventTextDelta, Text: ev.Text}
	case acp.EventThought:
		t.flushTextLocked()
		t.thinkBuf.WriteString(ev.Text)
	case acp.EventToolCall:
		t.flushTextLocked()
		t.flushThinkLocked()
		t.out <- Event{Type: EventToolUse, ToolUseID: ev.ToolID, Name: ev.Title}
		t.out <- Event{Type: EventAssistantEnd}
	case acp.EventToolCallUpdate:
		t.out <- Event{
			Type:      EventToolResult,
			ToolUseID: ev.ToolID,
			Content:   ev.Status,
			IsError:   ev.Status == "failed",
		}
		t.out <- Event{Type: EventUserEnd}
	}
}

// AccumulatedText returns the full text buffered for the current turn without
// clearing the buffer. Call this before flushAll to capture the response for
// JSON extraction while still letting flushAll emit the canonical EventText
// entry to the transcript.
func (t *acpTranslator) AccumulatedText() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.textBuf.String()
}

// flushAll flushes any remaining buffered content at end-of-turn. Called once
// from acpSession.run after conn.Prompt returns, at which point no further
// handle calls will be made.
func (t *acpTranslator) flushAll() {
	t.mu.Lock()
	defer t.mu.Unlock()
	// Thoughts precede text in Claude's turn structure; flush in that order.
	t.flushThinkLocked()
	t.flushTextLocked()
}

func (t *acpTranslator) flushTextLocked() {
	if t.textBuf.Len() == 0 {
		return
	}
	t.out <- Event{Type: EventText, Text: t.textBuf.String()}
	t.out <- Event{Type: EventAssistantEnd}
	t.textBuf.Reset()
}

func (t *acpTranslator) flushThinkLocked() {
	if t.thinkBuf.Len() == 0 {
		return
	}
	t.out <- Event{Type: EventThinking, Text: t.thinkBuf.String()}
	t.out <- Event{Type: EventAssistantEnd}
	t.thinkBuf.Reset()
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
