// Turns a captured request body into a system prompt and plain text turns, or
// refuses it with the reason a replay could not reproduce it faithfully.
//
// Accepted shapes: OpenAI chat `messages`, OpenAI Responses `input` and
// `instructions`, Anthropic `system` and `messages`, Gemini `contents` and
// `systemInstruction`, OpenTelemetry GenAI messages (`role` plus typed
// `parts`), a bare message list, and a bare prompt string.

package cases

import (
	"regexp"
	"strings"

	"github.com/metergraph/ezswap/internal/cases/jsval"
	"github.com/metergraph/ezswap/internal/jsnum"
	"github.com/metergraph/ezswap/internal/types"
)

// MaxInputBytes is the whole composed input (system plus messages) a replay
// may send.
const MaxInputBytes = 64 * 1024

// NotReplayable is the error a request a replay cannot reproduce fails with.
type NotReplayable struct {
	Reason  types.NotReplayableReason
	Message string
}

func (e *NotReplayable) Error() string { return e.Message }

// Block types that carry tool calls or tool results.
var toolBlockTypes = map[string]bool{
	"tool_use":                true,
	"tool_result":             true,
	"server_tool_use":         true,
	"web_search_tool_result":  true,
	"mcp_tool_use":            true,
	"mcp_tool_result":         true,
	"function_call":           true,
	"function_call_output":    true,
	"custom_tool_call":        true,
	"custom_tool_call_output": true,
	"computer_call":           true,
	"computer_call_output":    true,
	"web_search_call":         true,
	"file_search_call":        true,
	"code_interpreter_call":   true,
	"local_shell_call":        true,
	"local_shell_call_output": true,
	"mcp_call":                true,
	"tool_call":               true,
	"tool_call_response":      true,
}

// undefined, null, "text", "input_text" and "output_text".
func isTextBlockType(kind any) bool {
	if jsval.Nullish(kind) {
		return true
	}
	switch kind {
	case "text", "input_text", "output_text":
		return true
	}
	return false
}

var geminiToolParts = []string{"functionCall", "function_call", "functionResponse", "function_response"}

// IsObject is `typeof value === "object" && value !== null && !Array.isArray(value)`.
func IsObject(value any) bool { return jsval.IsObject(value) }

func isToolBlock(kind any) bool {
	s, ok := kind.(string)
	return ok && toolBlockTypes[s]
}

var labelPattern = regexp.MustCompile(`^[A-Za-z0-9_]+$`)

func blockLabel(kind any) string {
	if s, ok := kind.(string); ok && len(s) > 0 && jsval.UTF16Len(s) <= 40 && labelPattern.MatchString(s) {
		return s
	}
	return "unknown"
}

func toolHistory() *NotReplayable {
	return &NotReplayable{
		types.ToolHistory,
		"the request contains tool calls or tool results, which a replay cannot reproduce",
	}
}

func malformed(message string) *NotReplayable {
	return &NotReplayable{types.UnparseableRequest, message}
}

// Text of one message's content, refusing anything a replay would distort.
func contentText(content any) (*string, error) {
	if jsval.Nullish(content) {
		return nil, nil
	}
	if s, ok := content.(string); ok {
		return &s, nil
	}
	items, ok := content.([]any)
	if !ok {
		return nil, malformed("a message has content that is neither text nor a list of blocks")
	}
	texts := []string{}
	for _, item := range items {
		if s, ok := item.(string); ok {
			texts = append(texts, s)
			continue
		}
		if !jsval.IsObject(item) {
			return nil, malformed("a message has an unreadable content block")
		}
		kind := jsval.Get(item, "type")
		if isToolBlock(kind) {
			return nil, toolHistory()
		}
		if isTextBlockType(kind) {
			// OpenAI and Anthropic spell the text `text`; OpenTelemetry spells it `content`.
			text := jsval.Get(item, "text")
			if _, ok := text.(string); !ok {
				text = jsval.Get(item, "content")
			}
			if s, ok := text.(string); ok {
				texts = append(texts, s)
				continue
			}
		}
		return nil, &NotReplayable{
			types.UnsupportedContent,
			"a replay cannot reproduce " + blockLabel(kind) + " content blocks",
		}
	}
	joined := strings.Join(texts, "\n")
	return &joined, nil
}

func geminiText(parts any) (string, error) {
	list, ok := parts.([]any)
	if !ok {
		return "", malformed("a Gemini message has unreadable parts")
	}
	texts := []string{}
	for _, part := range list {
		if !jsval.IsObject(part) {
			return "", malformed("a Gemini message has an unreadable part")
		}
		for _, key := range geminiToolParts {
			if jsval.Has(part, key) {
				return "", toolHistory()
			}
		}
		text, ok := jsval.Get(part, "text").(string)
		if !ok {
			return "", &NotReplayable{types.UnsupportedContent, "a replay can only reproduce text Gemini parts"}
		}
		texts = append(texts, text)
	}
	return strings.Join(texts, "\n"), nil
}

func geminiSystem(value any) (string, error) {
	if s, ok := value.(string); ok {
		return s, nil
	}
	if jsval.IsObject(value) && jsval.Has(value, "parts") {
		return geminiText(jsval.Get(value, "parts"))
	}
	if _, ok := value.([]any); ok {
		return geminiText(value)
	}
	return "", &NotReplayable{types.UnsupportedContent, "a replay cannot read the Gemini system instruction"}
}

func join(first, second *string) *string {
	if second == nil || *second == "" {
		return first
	}
	if first != nil && *first != "" {
		joined := *first + "\n\n" + *second
		return &joined
	}
	return second
}

// ParsedMessages is a request's system prompt and turns.
type ParsedMessages struct {
	System   *string
	Messages []types.Message
}

func bareText(system *string, text string) ParsedMessages {
	messages := []types.Message{}
	if jsval.Trim(text) != "" {
		messages = append(messages, types.Message{Role: "user", Content: text})
	}
	return ParsedMessages{System: system, Messages: messages}
}

// System prompt and turns as captured, before merging and the size rules.
func capturedMessages(payload any) (ParsedMessages, error) {
	if s, ok := payload.(string); ok {
		return bareText(nil, s), nil
	}
	if list, ok := payload.([]any); ok {
		payload = map[string]any{"messages": list}
	}
	if !jsval.IsObject(payload) {
		return ParsedMessages{}, malformed("the request is not an object, a message list, or text")
	}
	var system *string
	for _, key := range []string{"system", "system_instructions", "instructions"} {
		value := jsval.Get(payload, key)
		if !jsval.Nullish(value) {
			text, err := contentText(value)
			if err != nil {
				return ParsedMessages{}, err
			}
			system = join(system, text)
		}
	}
	var config any = map[string]any{}
	if c := jsval.Get(payload, "config"); jsval.IsObject(c) {
		config = c
	}
	for _, container := range []any{payload, config} {
		for _, key := range []string{"system_instruction", "systemInstruction"} {
			value := jsval.Get(container, key)
			if !jsval.Nullish(value) {
				text, err := geminiSystem(value)
				if err != nil {
					return ParsedMessages{}, err
				}
				system = join(system, &text)
			}
		}
	}
	messages := []types.Message{}
	if jsval.Has(payload, "contents") && jsval.Get(payload, "contents") != jsval.Undefined && !jsval.Has(payload, "messages") {
		contents := jsval.Get(payload, "contents")
		if s, ok := contents.(string); ok {
			return bareText(system, s), nil
		}
		list, ok := contents.([]any)
		if !ok {
			return ParsedMessages{}, malformed("the Gemini contents are not a list")
		}
		for _, item := range list {
			if !jsval.IsObject(item) {
				return ParsedMessages{}, malformed("a Gemini message is not an object")
			}
			role := "user"
			if jsval.Get(item, "role") == "model" {
				role = "assistant"
			}
			text, err := geminiText(jsval.Get(item, "parts"))
			if err != nil {
				return ParsedMessages{}, err
			}
			if text != "" {
				messages = append(messages, types.Message{Role: role, Content: text})
			}
		}
		return ParsedMessages{System: system, Messages: messages}, nil
	}
	hasMessages := jsval.Has(payload, "messages")
	var raw any
	if hasMessages {
		raw = jsval.Get(payload, "messages")
	} else {
		raw = jsval.Get(payload, "input")
	}
	if prompt, ok := jsval.Get(payload, "prompt").(string); jsval.Nullish(raw) && ok {
		raw = prompt
	}
	if s, ok := raw.(string); ok && !hasMessages {
		// Responses `input` and completion `prompt` may be a bare string.
		return bareText(system, s), nil
	}
	if jsval.Nullish(raw) {
		return ParsedMessages{System: system, Messages: messages}, nil
	}
	list, ok := raw.([]any)
	if !ok {
		return ParsedMessages{}, malformed("the request messages are not a list")
	}
	for _, item := range list {
		if !jsval.IsObject(item) {
			return ParsedMessages{}, malformed("a message is not an object")
		}
		kind := jsval.Get(item, "type")
		toolCalls := jsval.Get(item, "tool_calls")
		calls := false
		if l, ok := toolCalls.([]any); ok {
			calls = len(l) > 0
		} else {
			calls = jsval.Truthy(toolCalls)
		}
		if isToolBlock(kind) || calls || jsval.Truthy(jsval.Get(item, "function_call")) {
			return ParsedMessages{}, toolHistory()
		}
		var roleValue any = "user"
		if r := jsval.Get(item, "role"); !jsval.Nullish(r) {
			roleValue = r
		}
		role := jsval.ToLower(jsval.ToString(roleValue))
		if role == "tool" || role == "function" {
			return ParsedMessages{}, toolHistory()
		}
		// OpenTelemetry GenAI messages carry typed `parts` instead of `content`.
		var content any
		if jsval.Has(item, "parts") && !jsval.Has(item, "content") {
			content = jsval.Get(item, "parts")
		} else {
			content = jsval.Get(item, "content")
		}
		text, err := contentText(content)
		if err != nil {
			return ParsedMessages{}, err
		}
		switch role {
		case "system", "developer":
			system = join(system, text)
		case "user", "assistant":
			if text != nil && *text != "" {
				messages = append(messages, types.Message{Role: role, Content: *text})
			}
		default:
			return ParsedMessages{}, malformed("a message has an unsupported role " + blockLabel(role))
		}
	}
	return ParsedMessages{System: system, Messages: messages}, nil
}

// RequestMessages is the system prompt and turns a replay sends. Same-role
// turns merge, the last turn must be the user's and the whole input must fit
// in 64 KiB. Fails with a *NotReplayable otherwise.
func RequestMessages(payload any) (ParsedMessages, error) {
	parsed, err := capturedMessages(payload)
	if err != nil {
		return ParsedMessages{}, err
	}
	system, messages := parsed.System, parsed.Messages
	if len(messages) == 0 {
		return ParsedMessages{}, &NotReplayable{types.NoMessages, "the request has no text turns to replay"}
	}
	// Dropping an empty turn can leave two turns from one role in a row, which
	// providers reject; merge them so the conversation keeps its order.
	merged := []types.Message{}
	for _, message := range messages {
		if n := len(merged); n > 0 && merged[n-1].Role == message.Role {
			merged[n-1] = types.Message{Role: message.Role, Content: merged[n-1].Content + "\n\n" + message.Content}
		} else {
			merged = append(merged, message)
		}
	}
	if merged[len(merged)-1].Role != "user" {
		return ParsedMessages{}, &NotReplayable{types.LastTurnNotUser, "the request ends with an assistant turn"}
	}
	size := 0
	if system != nil {
		size += len(*system)
	}
	for _, m := range merged {
		size += len(m.Content)
	}
	if size > MaxInputBytes {
		return ParsedMessages{}, &NotReplayable{
			types.InputTooLarge,
			"the replay input exceeds " + jsnum.String(float64(MaxInputBytes)/1024) + " KiB",
		}
	}
	return ParsedMessages{System: system, Messages: merged}, nil
}
