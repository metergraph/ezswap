// Package cases turns a CapturedCall into a Case: what a replay sends,
// production's output to compare against, and the reason a call cannot be
// replayed when it cannot.
package cases

import (
	"errors"
	"regexp"
	"slices"
	"strings"

	"github.com/metergraph/ezswap/internal/cases/jsval"
	"github.com/metergraph/ezswap/internal/types"
)

const InputPreviewChars = 280

// PrepareCaseOptions tune PrepareCase.
type PrepareCaseOptions struct {
	// Replay calls whose production request searched the web, without search.
	// Off by default: such a call is not replayable ("web_search_not_replayed").
	AllowNoSearch bool
}

// ToolsNotOffered is added to `missing` when production offered tools the
// replay leaves out.
const ToolsNotOffered = "tools_not_offered"

// decodeJSON reads a string as JSON, objects ordered; anything else, or a
// string that does not parse, is returned as it is.
func decodeJSON(value any) any {
	s, ok := value.(string)
	if !ok {
		return value
	}
	parsed, err := jsval.Parse(s)
	if err != nil {
		return value
	}
	return parsed
}

// SingleToolCallOutput is a forced tool's answer: the arguments of the one
// tool call, when they are a JSON object.
func SingleToolCallOutput(toolCalls any) *string {
	toolCalls = decodeJSON(toolCalls)
	list, ok := toolCalls.([]any)
	if !ok || len(list) != 1 || !jsval.IsObject(list[0]) {
		return nil
	}
	call := list[0]
	// Normalized {arguments}, OpenAI chat {function: {arguments}}, Anthropic tool_use {input},
	// Gemini functionCall {args}.
	var fn any
	if f := jsval.Get(call, "function"); jsval.IsObject(f) {
		fn = f
	} else if f := jsval.Get(call, "functionCall"); jsval.IsObject(f) {
		fn = f
	}
	var args any
	for _, candidate := range []any{
		jsval.Get(call, "arguments"),
		jsval.Get(call, "input"),
		jsval.Get(call, "args"),
		jsval.Get(fn, "arguments"),
		jsval.Get(fn, "args"),
	} {
		if !jsval.Nullish(candidate) {
			args = candidate
			break
		}
	}
	if jsval.IsObject(args) {
		out := jsval.Stringify(args)
		return &out
	}
	if s, ok := args.(string); ok {
		if parsed, ok := parseOutputJSON(s); ok && jsval.IsObject(parsed) {
			return &s
		}
	}
	return nil
}

var fencePattern = regexp.MustCompile("^```[A-Za-z0-9_-]*[" + jsval.SpaceClass + "]*\\n((?s:.)*?)\\n?```$")

// Parse an output as JSON, accepting one surrounding Markdown code fence.
func parseOutputJSON(output string) (any, bool) {
	text := jsval.Trim(output)
	if fenced := fencePattern.FindStringSubmatch(text); fenced != nil {
		text = jsval.Trim(fenced[1])
	}
	parsed, err := jsval.Parse(text)
	if err != nil {
		return nil, false
	}
	return parsed, true
}

// InputPreview is a short preview of the last non-blank user turn.
func InputPreview(messages []types.Message) *string {
	for i := len(messages) - 1; i >= 0; i-- {
		message := messages[i]
		if message.Role == "user" && jsval.Trim(message.Content) != "" {
			joined := strings.Join(jsval.SplitSpace(message.Content), " ")
			preview := jsval.SliceUTF16(joined, InputPreviewChars)
			return &preview
		}
	}
	return nil
}

func productionOutput(call types.CapturedCall) types.CaseOutput {
	response := call.Response
	output := response.Text
	if output == nil || jsval.Trim(*output) == "" {
		// A forced tool call writes no text: its arguments are the answer.
		var toolCalls any
		if response.ToolCalls != nil {
			toolCalls = response.ToolCalls
		}
		if args := SingleToolCallOutput(toolCalls); args != nil {
			output = args
		}
	}
	var finishReason *string
	if response.FinishReason != nil {
		if trimmed := jsval.Trim(*response.FinishReason); trimmed != "" {
			finishReason = &trimmed
		}
	}
	var latency *float64
	// No model answers in 0 ms: a zero means the source logged no duration
	// (Langfuse writes endTime = startTime when a call was not timed).
	if response.LatencyMs != nil && *response.LatencyMs > 0 {
		latency = response.LatencyMs
	}
	return types.CaseOutput{
		Output:       output,
		FinishReason: finishReason,
		Refusal:      response.Refusal,
		Usage:        response.Usage,
		CostUSD:      response.CostUSD,
		LatencyMs:    latency,
	}
}

// requestMessagesSafe is RequestMessages with a panic read as an unparseable
// request: a parser failure of any kind marks the call not replayable.
func requestMessagesSafe(payload any) (parsed ParsedMessages, err error) {
	defer func() {
		if r := recover(); r != nil {
			err = errors.New("the request could not be read")
		}
	}()
	return RequestMessages(payload)
}

// PrepareCase reads one captured call into a Case, replayable or with the
// reason it is not.
func PrepareCase(call types.CapturedCall, options PrepareCaseOptions) types.Case {
	payload := decodeJSON(call.Request)
	missing := append([]string{}, call.Missing...)
	webSearch := WebSearchSeen(payload)
	kase := types.Case{
		ID:         call.ID,
		TraceID:    call.TraceID,
		TS:         call.TS,
		Source:     call.Source,
		Provider:   call.Provider,
		Model:      call.Model,
		Messages:   []types.Message{},
		Params:     CapturedParams(payload),
		WebSearch:  webSearch,
		Production: productionOutput(call),
		Fidelity:   "full",
		Part:       call.Part,
	}
	finish := func(reason types.NotReplayableReason) types.Case {
		if reason != "" {
			kase.NotReplayable = &reason
		}
		kase.Missing = missing
		if len(missing) > 0 {
			kase.Fidelity = "partial"
		} else {
			kase.Fidelity = "full"
		}
		return kase
	}

	if call.Request == nil {
		return finish(types.ContentUnavailable)
	}
	if call.Truncated {
		return finish(types.ContentTruncated)
	}
	parsed, err := requestMessagesSafe(payload)
	if err != nil {
		var refusal *NotReplayable
		if errors.As(err, &refusal) {
			return finish(refusal.Reason)
		}
		return finish(types.UnparseableRequest)
	}
	kase.System = parsed.System
	kase.Messages = parsed.Messages
	kase.InputPreview = InputPreview(kase.Messages)
	kase.ResponseFormat = ResponseFormat(payload)
	kase.ForcedTool = ForcedTool(payload)

	tools := OfferedTools(payload)
	if kase.ForcedTool == nil && len(tools) > 0 {
		// A forced choice among several tools, or of a tool not offered alone,
		// cannot be reproduced by a replay that offers none.
		if ChoiceForcesTool(payload) {
			return finish(types.ToolDefinitionsNotOffered)
		}
		// The model was free to answer in text; the replay offers no tools, and
		// the case is marked in `missing`.
		if !slices.Contains(missing, ToolsNotOffered) {
			missing = append(missing, ToolsNotOffered)
		}
	}
	if webSearch && !options.AllowNoSearch {
		return finish(types.WebSearchNotReplayed)
	}
	return finish("")
}
