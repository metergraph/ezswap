// Production settings read from a captured request body: sampling parameters,
// the output cap, structured output, a forced tool and web search.

package cases

import (
	"encoding/json"
	"math"
	"slices"
	"strings"

	"github.com/metergraph/replay/internal/cases/jsval"
	"github.com/metergraph/replay/internal/jsnum"
	"github.com/metergraph/replay/internal/types"
)

const (
	MinMaxTokens = 1
	MaxMaxTokens = 32000
)

// Captured settings could carry anything; only short strings are kept.
const maxSettingChars = 64

// Containers a provider may nest its generation settings in.
var settingContainers = []string{"config", "generationConfig", "generation_config", "inferenceConfig", "params"}

func containers(payload any) []any {
	found := []any{payload}
	for _, key := range settingContainers {
		if value := jsval.Get(payload, key); jsval.IsObject(value) {
			found = append(found, value)
		}
	}
	return found
}

func finiteNumber(value any) *float64 {
	n, ok := value.(float64)
	if !ok || math.IsNaN(n) || math.IsInf(n, 0) {
		return nil
	}
	return &n
}

func first[T any](payload any, keys []string, read func(any) *T) *T {
	for _, container := range containers(payload) {
		for _, key := range keys {
			if value := read(jsval.Get(container, key)); value != nil {
				return value
			}
		}
	}
	return nil
}

// ProductionMaxTokens is production's output cap from any of its spellings,
// bounded as a treatment's would be.
func ProductionMaxTokens(payload any) *float64 {
	if !jsval.IsObject(payload) {
		return nil
	}
	value := first(
		payload,
		[]string{"max_tokens", "max_output_tokens", "max_completion_tokens", "maxOutputTokens", "maxTokens"},
		finiteNumber,
	)
	if value == nil || !jsnum.IsInteger(*value) || *value < MinMaxTokens {
		return nil
	}
	capped := math.Min(*value, MaxMaxTokens)
	return &capped
}

func stopSequences(value any) *[]string {
	if s, ok := value.(string); ok {
		if s == "" {
			return nil
		}
		return &[]string{s}
	}
	if list, ok := value.([]any); ok {
		stops := []string{}
		for _, item := range list {
			if s, ok := item.(string); ok && s != "" {
				stops = append(stops, s)
			}
		}
		if len(stops) > 0 {
			return &stops
		}
	}
	return nil
}

func shortString(value any) *string {
	s, ok := value.(string)
	if !ok || s == "" || jsval.UTF16Len(s) > maxSettingChars {
		return nil
	}
	return &s
}

func reasoningEffort(payload any) *string {
	for _, container := range containers(payload) {
		if direct := shortString(jsval.Get(container, "reasoning_effort")); direct != nil {
			return direct
		}
		if reasoning := jsval.Get(container, "reasoning"); jsval.IsObject(reasoning) {
			if effort := shortString(jsval.Get(reasoning, "effort")); effort != nil {
				return effort
			}
		}
	}
	return nil
}

// CapturedParams are the production settings a replay carries, stop and
// top_p included.
func CapturedParams(payload any) types.CallParams {
	if !jsval.IsObject(payload) {
		return types.CallParams{}
	}
	params := types.CallParams{
		Temperature:     first(payload, []string{"temperature"}, finiteNumber),
		MaxTokens:       ProductionMaxTokens(payload),
		TopP:            first(payload, []string{"top_p", "topP"}, finiteNumber),
		ReasoningEffort: reasoningEffort(payload),
	}
	if stop := first(payload, []string{"stop", "stop_sequences", "stopSequences"}, stopSequences); stop != nil {
		params.Stop = *stop
	}
	return params
}

// rawJSON is a value as JSON.stringify writes it, source key order kept.
func rawJSON(value any) json.RawMessage {
	return json.RawMessage(jsval.Stringify(value))
}

// ResponseFormat is the structured output production asked for, as a
// chat-completions `response_format`: a top-level one, or a Responses API
// `text.format` (flat: name, description, schema, strict beside type)
// translated to the nested chat shape. It is raw JSON in production's key
// order (JSON.stringify form), or nil.
func ResponseFormat(payload any) json.RawMessage {
	if !jsval.IsObject(payload) {
		return nil
	}
	value := jsval.Get(payload, "response_format")
	if jsval.IsObject(value) {
		if kind := jsval.Get(value, "type"); kind == "json_object" || kind == "json_schema" {
			return rawJSON(value)
		}
	}
	text := jsval.Get(payload, "text")
	var format any
	if jsval.IsObject(text) {
		format = jsval.Get(text, "format")
	}
	if !jsval.IsObject(format) {
		return nil
	}
	if jsval.Get(format, "type") == "json_object" {
		out := jsval.NewObject()
		out.Set("type", "json_object")
		return rawJSON(out)
	}
	if jsval.Get(format, "type") == "json_schema" && jsval.IsObject(jsval.Get(format, "schema")) {
		name := "output"
		if s, ok := jsval.Get(format, "name").(string); ok && s != "" {
			name = s
		}
		jsonSchema := jsval.NewObject()
		jsonSchema.Set("name", name)
		jsonSchema.Set("schema", jsval.Get(format, "schema"))
		if description, ok := jsval.Get(format, "description").(string); ok {
			jsonSchema.Set("description", description)
		}
		// Only when production set it: the default differs between the two APIs.
		if strict, ok := jsval.Get(format, "strict").(bool); ok {
			jsonSchema.Set("strict", strict)
		}
		out := jsval.NewObject()
		out.Set("type", "json_schema")
		out.Set("json_schema", jsonSchema)
		return rawJSON(out)
	}
	return nil
}

// Gemini names its search grounding tool by one of these keys.
var googleSearchTools = []string{"google_search", "googleSearch", "google_search_retrieval", "googleSearchRetrieval"}

func isWebSearchTool(tool any) bool {
	if !jsval.IsObject(tool) {
		return false
	}
	var kind any = ""
	if t := jsval.Get(tool, "type"); !jsval.Nullish(t) {
		kind = t
	}
	if strings.HasPrefix(jsval.ToString(kind), "web_search") {
		return true
	}
	for _, key := range googleSearchTools {
		if jsval.Has(tool, key) {
			return true
		}
	}
	return false
}

func toolLists(payload any) [][]any {
	lists := [][]any{}
	if tools, ok := jsval.Get(payload, "tools").([]any); ok {
		lists = append(lists, tools)
	}
	if config := jsval.Get(payload, "config"); jsval.IsObject(config) {
		if tools, ok := jsval.Get(config, "tools").([]any); ok {
			lists = append(lists, tools)
		}
	}
	return lists
}

// WebSearchSeen reports whether production searched the web. Read from chat
// completions' `web_search_options` or a built-in search tool (OpenAI
// Responses `web_search`/`web_search_preview`, Anthropic `web_search_*`,
// Gemini's `google_search` grounding).
func WebSearchSeen(payload any) bool {
	if !jsval.IsObject(payload) {
		return false
	}
	if jsval.IsObject(jsval.Get(payload, "web_search_options")) {
		return true
	}
	for _, tools := range toolLists(payload) {
		if slices.ContainsFunc(tools, isWebSearchTool) {
			return true
		}
	}
	return false
}

// OfferedTools are the tools production offered that a replay could call:
// everything but web search.
func OfferedTools(payload any) []any {
	offered := []any{}
	if !jsval.IsObject(payload) {
		return offered
	}
	for _, tools := range toolLists(payload) {
		for _, tool := range tools {
			if !isWebSearchTool(tool) {
				offered = append(offered, tool)
			}
		}
	}
	return offered
}

func nullishOr(value, fallback any) any {
	if jsval.Nullish(value) {
		return fallback
	}
	return value
}

// ChoiceForcesTool reports whether the tool choice makes the model call a
// tool, rather than leave it free to.
func ChoiceForcesTool(payload any) bool {
	if !jsval.IsObject(payload) {
		return false
	}
	choice := jsval.Get(payload, "tool_choice")
	if choice == "required" || choice == "any" {
		return true
	}
	if jsval.IsObject(choice) {
		switch jsval.ToString(jsval.Get(choice, "type")) {
		case "tool", "any", "function":
			return true
		}
	}
	// Gemini: toolConfig.functionCallingConfig.mode ANY.
	var config any = map[string]any{}
	if c := jsval.Get(payload, "config"); jsval.IsObject(c) {
		config = c
	}
	for _, container := range []any{payload, config} {
		toolConfig := nullishOr(jsval.Get(container, "toolConfig"), jsval.Get(container, "tool_config"))
		var calling any
		if jsval.IsObject(toolConfig) {
			calling = nullishOr(jsval.Get(toolConfig, "functionCallingConfig"), jsval.Get(toolConfig, "function_calling_config"))
		}
		if jsval.IsObject(calling) && strings.ToUpper(jsval.ToString(jsval.Get(calling, "mode"))) == "ANY" {
			return true
		}
	}
	return false
}

func forced(name string, description any, schema any) *types.ForcedTool {
	tool := &types.ForcedTool{Name: name, Parameters: rawJSON(schema)}
	if d, ok := description.(string); ok {
		tool.Description = &d
	}
	return tool
}

// Gemini: one function declaration and function calling mode ANY.
func geminiForcedTool(payload any, declarations []any) *types.ForcedTool {
	if len(declarations) != 1 || !jsval.IsObject(declarations[0]) {
		return nil
	}
	declaration := declarations[0]
	name, _ := jsval.Get(declaration, "name").(string)
	description := jsval.Get(declaration, "description")
	schema := nullishOr(jsval.Get(declaration, "parameters"), jsval.Get(declaration, "parametersJsonSchema"))
	if name == "" || !jsval.IsObject(schema) || !ChoiceForcesTool(payload) {
		return nil
	}
	return forced(name, description, schema)
}

// ForcedTool is a request that offered exactly one tool and forced the model
// to call it. Anything else (several tools, a choice the model makes, no
// tools) is nil: an agent loop a replay cannot reproduce.
func ForcedTool(payload any) *types.ForcedTool {
	if !jsval.IsObject(payload) {
		return nil
	}
	tools, ok := jsval.Get(payload, "tools").([]any)
	choice := jsval.Get(payload, "tool_choice")
	if !ok || len(tools) != 1 || !jsval.IsObject(tools[0]) {
		return nil
	}
	tool := tools[0]
	declarations := nullishOr(jsval.Get(tool, "functionDeclarations"), jsval.Get(tool, "function_declarations"))
	if list, ok := declarations.([]any); ok {
		return geminiForcedTool(payload, list)
	}
	var nameValue, description, schema any
	if fn := jsval.Get(tool, "function"); jsval.IsObject(fn) {
		// OpenAI chat: {type: function, function: {name, description, parameters}}
		nameValue, description, schema = jsval.Get(fn, "name"), jsval.Get(fn, "description"), jsval.Get(fn, "parameters")
	} else if jsval.Get(tool, "type") == "function" {
		// OpenAI Responses and OpenTelemetry: {type: function, name, description, parameters}
		nameValue, description, schema = jsval.Get(tool, "name"), jsval.Get(tool, "description"), jsval.Get(tool, "parameters")
	} else {
		// Anthropic: {name, description, input_schema}
		nameValue, description, schema = jsval.Get(tool, "name"), jsval.Get(tool, "description"), jsval.Get(tool, "input_schema")
	}
	name, _ := nameValue.(string)
	if name == "" || !jsval.IsObject(schema) {
		return nil
	}
	isForced := choice == "required"
	if !isForced && jsval.IsObject(choice) {
		kind := jsval.Get(choice, "type")
		fn := jsval.Get(choice, "function")
		isForced = (kind == "tool" && jsval.Get(choice, "name") == name) ||
			kind == "any" ||
			(kind == "function" &&
				((jsval.IsObject(fn) && jsval.Get(fn, "name") == name) || jsval.Get(choice, "name") == name))
	}
	if !isForced {
		return nil
	}
	return forced(name, description, schema)
}
