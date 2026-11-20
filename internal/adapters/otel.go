// OpenTelemetry GenAI spans: an OTLP JSON export (resourceSpans, scopeSpans,
// spans) or one span per line. Reads the current `gen_ai.input.messages` /
// `gen_ai.output.messages` attributes and the older indexed
// `gen_ai.prompt.N.*` / `gen_ai.completion.N.*` form. Content capture is
// opt-in in most SDKs: a span without it has no request, and cases reports
// it as content_unavailable.

package adapters

import (
	"regexp"
	"slices"
	"sort"
	"strings"

	"github.com/metergraph/ezswap/internal/cases/jsval"
	"github.com/metergraph/ezswap/internal/types"
)

// Attributes are kept as an ordered object: Object.entries walks them in
// insertion order, and an attribute OTLP sent without a value is present but
// undefined (jsval.Undefined).
type attributes = *jsval.Object

// One OTLP AnyValue to a plain value.
func anyValue(value any) any {
	if !IsObject(value) {
		return value
	}
	if has(value, "stringValue") {
		return get(value, "stringValue")
	}
	if has(value, "intValue") {
		return numOrNil(get(value, "intValue"))
	}
	if has(value, "doubleValue") {
		return numOrNil(get(value, "doubleValue"))
	}
	if has(value, "boolValue") {
		return get(value, "boolValue")
	}
	if has(value, "bytesValue") {
		return get(value, "bytesValue")
	}
	if array := get(value, "arrayValue"); IsObject(array) {
		values, ok := get(array, "values").([]any)
		if !ok {
			return []any{}
		}
		out := make([]any, len(values))
		for i, item := range values {
			out[i] = anyValue(item)
		}
		return out
	}
	if kvlist := get(value, "kvlistValue"); IsObject(kvlist) {
		values, ok := get(kvlist, "values").([]any)
		if !ok {
			return jsval.NewObject()
		}
		return attributeMap(values)
	}
	return value
}

func numOrNil(value any) any {
	if n := Num(value); n != nil {
		return *n
	}
	return nil
}

// OTLP `[{key, value}]` attributes, or an already flat map, to a flat map.
func attributeMap(attrs any) attributes {
	if list, ok := attrs.([]any); ok {
		out := jsval.NewObject()
		for _, entry := range list {
			if key, ok := get(entry, "key").(string); ok && IsObject(entry) {
				if has(entry, "value") {
					out.Set(key, anyValue(get(entry, "value")))
				} else {
					out.Set(key, jsval.Undefined)
				}
			}
		}
		return out
	}
	return jsval.ToOrdered(attrs)
}

func attr(a attributes, key string) any {
	v, _ := a.Get(key)
	return v
}

func attrHas(a attributes, key string) bool {
	_, ok := a.Get(key)
	return ok
}

func isSpan(record any) bool {
	return has(record, "attributes") &&
		(has(record, "spanId") || has(record, "span_id") || IsObject(get(record, "context")) || has(record, "name"))
}

func detectOtel(record any) bool {
	if !IsObject(record) {
		return false
	}
	if _, ok := get(record, "resourceSpans").([]any); ok {
		return true
	}
	if _, ok := get(record, "resource_spans").([]any); ok {
		return true
	}
	if !isSpan(record) {
		return false
	}
	return slices.ContainsFunc(attributeMap(get(record, "attributes")).Keys(), func(key string) bool {
		return strings.HasPrefix(key, "gen_ai.") || strings.HasPrefix(key, "llm.")
	})
}

// Spans in an OTLP export, or the record itself when it is one span.
func spansOf(record any) []any {
	resourceSpans, ok := nullishOr(get(record, "resourceSpans"), get(record, "resource_spans")).([]any)
	if !ok {
		return []any{record}
	}
	spans := []any{}
	for _, resource := range resourceSpans {
		if !IsObject(resource) {
			continue
		}
		scopes := nullishOr(get(resource, "scopeSpans"), nullishOr(get(resource, "scope_spans"), get(resource, "instrumentationLibrarySpans")))
		for _, scope := range listOf(scopes) {
			if !IsObject(scope) {
				continue
			}
			for _, span := range listOf(get(scope, "spans")) {
				if IsObject(span) {
					spans = append(spans, span)
				}
			}
		}
	}
	return spans
}

var skippedOperations = []string{"embeddings", "execute_tool", "invoke_agent", "create_agent"}

func isLlmSpan(a attributes) bool {
	operation, isString := attr(a, "gen_ai.operation.name").(string)
	if isString && slices.Contains(skippedOperations, operation) {
		return false
	}
	return attrHas(a, "gen_ai.request.model") ||
		attrHas(a, "gen_ai.input.messages") ||
		attrHas(a, "gen_ai.prompt.0.content") ||
		attrHas(a, "gen_ai.prompt.0.role") ||
		(isString && slices.Contains([]string{"chat", "text_completion", "generate_content"}, operation))
}

type jsonAttr struct {
	value any // jsval.Undefined when absent
	cut   bool
}

// A JSON-valued attribute. A string that does not parse was most likely cut
// at a length limit.
func jsonAttribute(a attributes, key string) jsonAttr {
	raw := attr(a, key)
	if jsval.Nullish(raw) {
		return jsonAttr{value: jsval.Undefined}
	}
	s, ok := raw.(string)
	if !ok {
		return jsonAttr{value: raw}
	}
	parsed, err := jsval.Parse(s)
	if err != nil {
		return jsonAttr{value: jsval.Undefined, cut: true}
	}
	return jsonAttr{value: parsed}
}

// JavaScript's `.` matches anything but a line terminator.
const anyButLineTerminator = `[^\n\r\x{2028}\x{2029}]`

// `prefix.0.role`, `prefix.0.content`, `prefix.1.role`... grouped by index.
func indexed(a attributes, prefix string) []attributes {
	pattern := regexp.MustCompile("^" + regexp.QuoteMeta(prefix) + `\.(\d+)\.(` + anyButLineTerminator + `+)$`)
	groups := map[float64]attributes{}
	order := []float64{}
	for _, key := range a.Keys() {
		match := pattern.FindStringSubmatch(key)
		if match == nil {
			continue
		}
		index := jsval.StringToNumber(match[1])
		group, ok := groups[index]
		if !ok {
			group = jsval.NewObject()
			groups[index] = group
			order = append(order, index)
		}
		group.Set(match[2], attr(a, key))
	}
	sort.SliceStable(order, func(i, j int) bool { return order[i] < order[j] })
	out := make([]attributes, len(order))
	for i, index := range order {
		out[i] = groups[index]
	}
	return out
}

// Content that an instrumentation stored as a JSON list of blocks stays a list.
func legacyContent(value any) any {
	if s, ok := value.(string); ok && strings.HasPrefix(jsval.TrimStart(s), "[") {
		if parsed, err := jsval.Parse(s); err == nil {
			if list, ok := parsed.([]any); ok {
				blocks := true
				for _, block := range list {
					if !IsObject(block) || !has(block, "type") {
						blocks = false
						break
					}
				}
				if blocks {
					return list
				}
			}
		}
		// Plain text that happens to start with a bracket.
	}
	return value
}

func legacyToolCalls(group attributes) []any {
	calls := indexed(group, "tool_calls")
	out := make([]any, len(calls))
	for i, tool := range calls {
		call := jsval.NewObject()
		call.Set("id", nullishOr(attr(tool, "id"), nil))
		call.Set("name", nullishOr(attr(tool, "name"), nil))
		call.Set("arguments", nullishOr(attr(tool, "arguments"), nil))
		out[i] = call
	}
	return out
}

func legacyMessages(a attributes) []any {
	groups := indexed(a, "gen_ai.prompt")
	if len(groups) == 0 {
		return nil
	}
	out := make([]any, len(groups))
	for i, group := range groups {
		message := jsval.NewObject()
		message.Set("role", nullishOr(attr(group, "role"), "user"))
		message.Set("content", legacyContent(nullishOr(attr(group, "content"), nil)))
		if toolCalls := legacyToolCalls(group); len(toolCalls) > 0 {
			message.Set("tool_calls", toolCalls)
		}
		if id, ok := group.Get("tool_call_id"); ok && id != jsval.Undefined {
			message.Set("role", "tool")
		}
		out[i] = message
	}
	return out
}

func legacyOutput(a attributes) *ParsedResponse {
	groups := indexed(a, "gen_ai.completion")
	if len(groups) == 0 {
		return nil
	}
	messages := make([]any, len(groups))
	for i, group := range groups {
		message := jsval.NewObject()
		message.Set("role", nullishOr(attr(group, "role"), "assistant"))
		message.Set("content", legacyContent(nullishOr(attr(group, "content"), nil)))
		message.Set("finish_reason", nullishOr(attr(group, "finish_reason"), nil))
		if toolCalls := legacyToolCalls(group); len(toolCalls) > 0 {
			message.Set("tool_calls", toolCalls)
		}
		messages[i] = message
	}
	parsed := OtelOutput(messages)
	return &parsed
}

func legacyTools(a attributes) []any {
	groups := indexed(a, "llm.request.functions")
	if len(groups) == 0 {
		return nil
	}
	out := make([]any, len(groups))
	for i, fn := range groups {
		parameters := attr(fn, "parameters")
		if s, ok := parameters.(string); ok {
			if parsed, err := jsval.Parse(s); err == nil {
				parameters = parsed
			} else {
				parameters = jsval.Undefined
			}
		}
		if !attrHas(fn, "parameters") {
			parameters = jsval.Undefined
		}
		tool := jsval.NewObject()
		tool.Set("type", "function")
		tool.Set("name", nullishOr(attr(fn, "name"), nil))
		tool.Set("description", nullishOr(attr(fn, "description"), nil))
		tool.Set("parameters", parameters)
		out[i] = tool
	}
	return out
}

func otelStopSequences(value any) any {
	if s, ok := value.(string); ok {
		return []any{s}
	}
	if list, ok := value.([]any); ok {
		stops := []any{}
		for _, item := range list {
			if s, ok := item.(string); ok {
				stops = append(stops, s)
			}
		}
		return stops
	}
	return nil
}

func strValue(s *string) any {
	if s == nil {
		return nil
	}
	return *s
}

func numValue(n *float64) any {
	if n == nil {
		return nil
	}
	return *n
}

func readSpan(span any, resource attributes, context RecordContext) *types.CapturedCall {
	a := resource.Copy()
	spanAttributes := attributeMap(get(span, "attributes"))
	for _, key := range spanAttributes.Keys() {
		a.Set(key, attr(spanAttributes, key))
	}
	if !isLlmSpan(a) {
		return nil
	}
	missing := []string{}

	input := jsonAttribute(a, "gen_ai.input.messages")
	system := jsonAttribute(a, "gen_ai.system_instructions")
	definitions := jsonAttribute(a, "gen_ai.tool.definitions")
	schema := jsonAttribute(a, "gen_ai.request.structured_output_schema")
	truncated := input.cut || system.cut || definitions.cut

	messages, ok := input.value.([]any)
	if !ok {
		messages = legacyMessages(a)
	}
	tools, ok := definitions.value.([]any)
	if !ok {
		tools = legacyTools(a)
	}
	model := FirstStr(attr(a, "gen_ai.request.model"), attr(a, "gen_ai.response.model"))

	var request *jsval.Object
	if messages == nil && !input.cut {
		AddMissing(&missing, "input_messages")
	} else {
		params := jsval.NewObject()
		params.Set("temperature", numValue(Num(attr(a, "gen_ai.request.temperature"))))
		params.Set("max_tokens", numValue(FirstNum(attr(a, "gen_ai.request.max_tokens"), attr(a, "gen_ai.request.max_completion_tokens"))))
		params.Set("top_p", numValue(Num(attr(a, "gen_ai.request.top_p"))))
		params.Set("stop", otelStopSequences(attr(a, "gen_ai.request.stop_sequences")))
		params.Set("seed", numValue(Num(attr(a, "gen_ai.request.seed"))))
		request = jsval.NewObject()
		if model != nil {
			request.Set("model", *model)
		} else {
			request.Set("model", nil)
		}
		if messages == nil {
			messages = []any{}
		}
		request.Set("messages", messages)
		request.Set("params", params)
		if system.value != jsval.Undefined {
			request.Set("system", system.value)
		}
		if tools != nil {
			request.Set("tools", tools)
		}
		if IsObject(schema.value) {
			jsonSchema := jsval.NewObject()
			jsonSchema.Set("name", "output")
			jsonSchema.Set("schema", schema.value)
			format := jsval.NewObject()
			format.Set("type", "json_schema")
			format.Set("json_schema", jsonSchema)
			request.Set("response_format", format)
		}
	}
	if tools == nil {
		AddMissing(&missing, "tools")
	} else {
		AddMissing(&missing, "tool_choice")
	}
	if !IsObject(schema.value) {
		AddMissing(&missing, "response_format")
	}

	output := jsonAttribute(a, "gen_ai.output.messages")
	truncated = truncated || output.cut
	var parsed *ParsedResponse
	if list, ok := output.value.([]any); ok {
		p := OtelOutput(list)
		parsed = &p
	} else {
		parsed = legacyOutput(a)
	}
	if parsed == nil {
		AddMissing(&missing, "output_messages")
		parsed = &ParsedResponse{}
	}
	reasons := attr(a, "gen_ai.response.finish_reasons")
	var finish *string
	if list, ok := reasons.([]any); ok {
		finish = FirstStr(list...)
	} else {
		finish = FirstStr(reasons)
	}
	if finish != nil {
		parsed.FinishReason = finish
	}

	spanContext := objectOr(get(span, "context"))
	spanID := FirstStr(get(span, "spanId"), get(span, "span_id"), get(spanContext, "span_id"))
	start := nullishOr(get(span, "startTimeUnixNano"), nullishOr(get(span, "start_time_unix_nano"), get(span, "start_time")))
	end := nullishOr(get(span, "endTimeUnixNano"), nullishOr(get(span, "end_time_unix_nano"), get(span, "end_time")))
	var body any
	if request != nil {
		body = request
	}
	captured := newCall(types.CapturedCall{
		ID:       *orDefault(spanID, ptr(HashID(context.Raw+"#"+itoa(context.Line)))),
		TraceID:  FirstStr(get(span, "traceId"), get(span, "trace_id"), get(spanContext, "trace_id")),
		TS:       IsoTime(start),
		Source:   "otel",
		Provider: NormalizeProvider(strValue(FirstStr(attr(a, "gen_ai.provider.name"), attr(a, "gen_ai.system")))),
		Model:    model,
		Request:  body,
		Response: CapturedResponse(*parsed, ResponseExtra{
			Usage: UsageOf(types.Usage{
				InputTokens:  FirstNum(attr(a, "gen_ai.usage.input_tokens"), attr(a, "gen_ai.usage.prompt_tokens")),
				OutputTokens: FirstNum(attr(a, "gen_ai.usage.output_tokens"), attr(a, "gen_ai.usage.completion_tokens")),
				CachedInputTokens: FirstNum(
					attr(a, "gen_ai.usage.cache_read.input_tokens"),
					attr(a, "gen_ai.usage.cache_read_input_tokens"),
				),
				ReasoningTokens: FirstNum(
					attr(a, "gen_ai.usage.reasoning.output_tokens"),
					attr(a, "gen_ai.usage.reasoning_tokens"),
				),
			}),
			LatencyMs: DurationMs(start, end),
		}),
		Truncated: truncated,
		Missing:   missing,
		Part:      otelPart(span, a, context),
	})
	return &captured
}

func readOtel(record any, context RecordContext) []types.CapturedCall {
	if !IsObject(record) {
		return nil
	}
	resourceSpans, ok := nullishOr(get(record, "resourceSpans"), get(record, "resource_spans")).([]any)
	calls := []types.CapturedCall{}
	if !ok {
		if one := readSpan(record, jsval.NewObject(), context); one != nil {
			return []types.CapturedCall{*one}
		}
		return nil
	}
	for _, resource := range resourceSpans {
		if !IsObject(resource) {
			continue
		}
		resourceAttributes := jsval.NewObject()
		if r := get(resource, "resource"); IsObject(r) {
			resourceAttributes = attributeMap(get(r, "attributes"))
		}
		// Resource attributes describe the service, not the call: keep only GenAI ones.
		genAiResource := jsval.NewObject()
		for _, key := range resourceAttributes.Keys() {
			if strings.HasPrefix(key, "gen_ai.") {
				genAiResource.Set(key, attr(resourceAttributes, key))
			}
		}
		one := jsval.NewObject()
		one.Set("resourceSpans", []any{resource})
		for _, span := range spansOf(one) {
			if captured := readSpan(span, genAiResource, context); captured != nil {
				calls = append(calls, *captured)
			}
		}
	}
	return calls
}

// Otel reads OpenTelemetry GenAI spans.
var Otel Adapter = adapter{name: "otel", detect: detectOtel, read: readOtel}

// otelPart is the part name under the developer's key: a span or resource
// attribute, or the span name when the key is "span.name". Span names are
// instrumentation defaults such as "chat gpt-4o-mini" unless the developer
// set them, so they are read only when asked for.
func otelPart(span any, a attributes, context RecordContext) *string {
	if context.PartKey == "" {
		return nil
	}
	if attrHas(a, context.PartKey) {
		if part := CleanPart(attr(a, context.PartKey)); part != nil {
			return part
		}
	}
	if context.PartKey == "span.name" {
		return CleanPart(get(span, "name"))
	}
	return nil
}
