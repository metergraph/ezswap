package record

// A streamed answer reaches the app untouched. The recorder rebuilds, from
// the events as they pass, the body the provider would have sent without
// streaming, which is the shape the trace reader understands.

import (
	"bytes"
	"encoding/json"
	"errors"
	"sort"
	"strings"

	"github.com/metergraph/ezswap/internal/cases/jsval"
)

// assembler rebuilds one provider's response body from its stream.
type assembler interface {
	// event takes one stream event: an SSE event's name and data, or (Gemini
	// without SSE) one element of the JSON array with an empty name.
	event(name string, data []byte)
	// body is the rebuilt body, or the reason there is none: skipStreamCut
	// when the stream did not reach its final event, skipNotJSON when an
	// event could not be read. ended says the response body reached its end.
	body(ended bool) (any, string)
}

func newAssembler(kind shape) assembler {
	switch kind {
	case shapeChat:
		return &chatAssembler{choices: map[int]*chatChoice{}}
	case shapeResponses:
		return &responsesAssembler{}
	case shapeAnthropic:
		return &anthropicAssembler{blocks: map[int]*anthropicBlock{}}
	}
	return &geminiAssembler{top: jsval.NewObject(), candidates: map[int]*geminiCandidate{}}
}

// object parses event data that must be a JSON object.
func object(data []byte) (*jsval.Object, bool) {
	value, err := parseBounded(string(data))
	if err != nil {
		return nil, false
	}
	o, ok := value.(*jsval.Object)
	return o, ok
}

func text(value any) string {
	s, _ := value.(string)
	return s
}

// indexOf is an "index" field; streams that leave it out mean 0.
func indexOf(value any) int {
	n, _ := value.(float64)
	return int(n)
}

func list(value any) []any {
	items, _ := value.([]any)
	return items
}

func sortedKeys[T any](m map[int]T) []int {
	keys := make([]int, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	sort.Ints(keys)
	return keys
}

// keep sets *dst to the first value that says something.
func keep(dst *any, value any) {
	if *dst == nil && !jsval.Nullish(value) && value != "" {
		*dst = value
	}
}

// ---------------------------------------------------------------------------
// OpenAI Chat Completions: chat.completion.chunk events, then "data: [DONE]".

type chatAssembler struct {
	id, model, created any
	choices            map[int]*chatChoice
	usage              any
	done, bad          bool
}

type chatChoice struct {
	content    strings.Builder
	hasContent bool
	refusal    strings.Builder
	hasRefusal bool
	tools      map[int]*chatTool
	finish     any
}

type chatTool struct {
	id, kind, name any
	arguments      strings.Builder
}

func (a *chatAssembler) event(_ string, data []byte) {
	if a.done {
		return
	}
	if string(bytes.TrimSpace(data)) == "[DONE]" {
		a.done = true
		return
	}
	chunk, ok := object(data)
	if !ok {
		a.bad = true
		return
	}
	keep(&a.id, jsval.Get(chunk, "id"))
	keep(&a.model, jsval.Get(chunk, "model"))
	keep(&a.created, jsval.Get(chunk, "created"))
	if usage := jsval.Get(chunk, "usage"); jsval.IsObject(usage) {
		a.usage = usage
	}
	for _, piece := range list(jsval.Get(chunk, "choices")) {
		index := indexOf(jsval.Get(piece, "index"))
		choice := a.choices[index]
		if choice == nil {
			choice = &chatChoice{tools: map[int]*chatTool{}}
			a.choices[index] = choice
		}
		if reason := jsval.Get(piece, "finish_reason"); !jsval.Nullish(reason) {
			choice.finish = reason
		}
		delta := jsval.Get(piece, "delta")
		if s, ok := jsval.Get(delta, "content").(string); ok {
			choice.content.WriteString(s)
			choice.hasContent = true
		}
		if s, ok := jsval.Get(delta, "refusal").(string); ok {
			choice.refusal.WriteString(s)
			choice.hasRefusal = true
		}
		for _, part := range list(jsval.Get(delta, "tool_calls")) {
			toolIndex := indexOf(jsval.Get(part, "index"))
			tool := choice.tools[toolIndex]
			if tool == nil {
				tool = &chatTool{}
				choice.tools[toolIndex] = tool
			}
			function := jsval.Get(part, "function")
			keep(&tool.id, jsval.Get(part, "id"))
			keep(&tool.kind, jsval.Get(part, "type"))
			keep(&tool.name, jsval.Get(function, "name"))
			tool.arguments.WriteString(text(jsval.Get(function, "arguments")))
		}
	}
}

func (a *chatAssembler) body(bool) (any, string) {
	if a.bad {
		return nil, skipNotJSON
	}
	// A stream with no choice at all has no answer to record.
	if !a.done || len(a.choices) == 0 {
		return nil, skipStreamCut
	}
	choices := []any{}
	for _, index := range sortedKeys(a.choices) {
		choice := a.choices[index]
		message := jsval.NewObject()
		message.Set("role", "assistant")
		// A turn that only calls tools has null content, as without streaming.
		if choice.hasContent {
			message.Set("content", choice.content.String())
		} else {
			message.Set("content", nil)
		}
		if choice.hasRefusal {
			message.Set("refusal", choice.refusal.String())
		}
		if len(choice.tools) > 0 {
			tools := []any{}
			for _, toolIndex := range sortedKeys(choice.tools) {
				tool := choice.tools[toolIndex]
				function := jsval.NewObject()
				function.Set("name", tool.name)
				function.Set("arguments", tool.arguments.String())
				entry := jsval.NewObject()
				entry.Set("id", tool.id)
				if tool.kind == nil {
					tool.kind = "function"
				}
				entry.Set("type", tool.kind)
				entry.Set("function", function)
				tools = append(tools, entry)
			}
			message.Set("tool_calls", tools)
		}
		entry := jsval.NewObject()
		entry.Set("index", index)
		entry.Set("message", message)
		entry.Set("finish_reason", choice.finish)
		choices = append(choices, entry)
	}
	out := jsval.NewObject()
	out.Set("id", a.id)
	out.Set("object", "chat.completion")
	out.Set("created", a.created)
	out.Set("model", a.model)
	out.Set("choices", choices)
	if a.usage != nil {
		out.Set("usage", a.usage)
	}
	return out, ""
}

// ---------------------------------------------------------------------------
// OpenAI Responses: the response.completed event carries the whole body.

type responsesAssembler struct {
	response any
	bad      bool
}

func (a *responsesAssembler) event(name string, data []byte) {
	if a.response != nil {
		return
	}
	event, ok := object(data)
	if !ok {
		a.bad = true
		return
	}
	kind := text(jsval.Get(event, "type"))
	if kind == "" {
		kind = name
	}
	if response := jsval.Get(event, "response"); kind == "response.completed" && jsval.IsObject(response) {
		a.response = response
	}
}

func (a *responsesAssembler) body(bool) (any, string) {
	switch {
	case a.bad:
		return nil, skipNotJSON
	case a.response == nil:
		return nil, skipStreamCut
	}
	return a.response, ""
}

// ---------------------------------------------------------------------------
// Anthropic Messages: message_start, content blocks built from deltas,
// message_delta, message_stop.

type anthropicAssembler struct {
	message   *jsval.Object
	blocks    map[int]*anthropicBlock
	done, bad bool
}

type anthropicBlock struct {
	block *jsval.Object
	// Text fields being joined ("text", "thinking"), set on the block when
	// it stops.
	joined map[string]*strings.Builder
	// The pieces of a tool call's input, parsed when the block stops.
	input    strings.Builder
	hasInput bool
}

func (b *anthropicBlock) join(field, piece string) {
	builder := b.joined[field]
	if builder == nil {
		builder = &strings.Builder{}
		builder.WriteString(text(jsval.Get(b.block, field)))
		b.joined[field] = builder
	}
	builder.WriteString(piece)
}

func (a *anthropicAssembler) event(name string, data []byte) {
	if a.done {
		return
	}
	event, ok := object(data)
	if !ok {
		a.bad = true
		return
	}
	kind := text(jsval.Get(event, "type"))
	if kind == "" {
		kind = name
	}
	index := indexOf(jsval.Get(event, "index"))
	switch kind {
	case "message_start":
		if message, ok := jsval.Get(event, "message").(*jsval.Object); ok {
			a.message = message
		}
	case "content_block_start":
		if block, ok := jsval.Get(event, "content_block").(*jsval.Object); ok {
			a.blocks[index] = &anthropicBlock{block: block, joined: map[string]*strings.Builder{}}
		}
	case "content_block_delta":
		block := a.blocks[index]
		if block == nil {
			return
		}
		delta := jsval.Get(event, "delta")
		switch jsval.Get(delta, "type") {
		case "text_delta":
			block.join("text", text(jsval.Get(delta, "text")))
		case "thinking_delta":
			block.join("thinking", text(jsval.Get(delta, "thinking")))
		case "signature_delta":
			// A thinking block's signature arrives whole, in one delta.
			block.block.Set("signature", jsval.Get(delta, "signature"))
		case "input_json_delta":
			block.input.WriteString(text(jsval.Get(delta, "partial_json")))
			block.hasInput = true
		}
	case "content_block_stop":
		block := a.blocks[index]
		if block == nil {
			return
		}
		for field, builder := range block.joined {
			block.block.Set(field, builder.String())
		}
		if block.hasInput {
			// A tool called with no arguments streams an empty string.
			var input any = jsval.NewObject()
			if source := block.input.String(); strings.TrimSpace(source) != "" {
				parsed, err := parseBounded(source)
				if err != nil {
					a.bad = true
					return
				}
				input = parsed
			}
			block.block.Set("input", input)
		}
	case "message_delta":
		if a.message == nil {
			return
		}
		// stop_reason and stop_sequence.
		delta := jsval.Get(event, "delta")
		for _, key := range jsval.Keys(delta) {
			a.message.Set(key, jsval.Get(delta, key))
		}
		// The usage here completes message_start's: it adds the output tokens.
		if usage := jsval.Get(event, "usage"); jsval.IsObject(usage) {
			merged, ok := jsval.Get(a.message, "usage").(*jsval.Object)
			if !ok {
				merged = jsval.NewObject()
				a.message.Set("usage", merged)
			}
			for _, key := range jsval.Keys(usage) {
				if value := jsval.Get(usage, key); !jsval.Nullish(value) {
					merged.Set(key, value)
				}
			}
		}
	case "message_stop":
		a.done = true
	}
}

func (a *anthropicAssembler) body(bool) (any, string) {
	switch {
	case a.bad:
		return nil, skipNotJSON
	case !a.done || a.message == nil:
		return nil, skipStreamCut
	}
	content := []any{}
	for _, index := range sortedKeys(a.blocks) {
		content = append(content, a.blocks[index].block)
	}
	a.message.Set("content", content)
	return a.message, ""
}

// ---------------------------------------------------------------------------
// Gemini: every event (or array element) is a whole response holding the
// next piece of each candidate.

type geminiAssembler struct {
	// Everything beside the candidates (usageMetadata, modelVersion, ...):
	// the last response that carries a field wins.
	top        *jsval.Object
	candidates map[int]*geminiCandidate
	finished   bool // some candidate reported its finishReason
	bad        bool
}

type geminiCandidate struct {
	candidate *jsval.Object
	content   *jsval.Object
	parts     []any
	// The last part while it is text that a following text part extends.
	tail     *jsval.Object
	tailText strings.Builder
}

// textPart reports whether a part is plain text, and whether it is a
// thought (thoughts and answer text are never joined with each other).
func textPart(part any) (isText, thought bool) {
	if _, ok := jsval.Get(part, "text").(string); !ok {
		return false, false
	}
	return true, jsval.Get(part, "thought") == true
}

func (c *geminiCandidate) add(part any) {
	o, ok := part.(*jsval.Object)
	if !ok {
		c.flush()
		c.parts = append(c.parts, part)
		return
	}
	isText, thought := textPart(o)
	if c.tail != nil && isText {
		if _, tailThought := textPart(c.tail); tailThought == thought {
			c.tailText.WriteString(text(jsval.Get(o, "text")))
			// Anything else the piece carries stays with the joined part.
			for _, key := range o.Keys() {
				if key != "text" {
					c.tail.Set(key, jsval.Get(o, key))
				}
			}
			return
		}
	}
	c.flush()
	c.parts = append(c.parts, o)
	if isText {
		c.tail = o
		c.tailText.WriteString(text(jsval.Get(o, "text")))
	}
}

// flush writes the joined text back to the part it belongs to.
func (c *geminiCandidate) flush() {
	if c.tail != nil {
		c.tail.Set("text", c.tailText.String())
		c.tail = nil
		c.tailText.Reset()
	}
}

func (a *geminiAssembler) event(_ string, data []byte) {
	response, ok := object(data)
	if !ok {
		a.bad = true
		return
	}
	a.response(response)
}

func (a *geminiAssembler) response(response *jsval.Object) {
	for _, key := range response.Keys() {
		if value := jsval.Get(response, key); key != "candidates" && !jsval.Nullish(value) {
			a.top.Set(key, value)
		}
	}
	for _, piece := range list(jsval.Get(response, "candidates")) {
		index := indexOf(jsval.Get(piece, "index"))
		c := a.candidates[index]
		if c == nil {
			c = &geminiCandidate{candidate: jsval.NewObject()}
			a.candidates[index] = c
		}
		for _, key := range jsval.Keys(piece) {
			value := jsval.Get(piece, key)
			switch {
			case key == "content":
				if c.content == nil {
					c.content = jsval.NewObject()
					c.candidate.Set("content", c.content)
				}
				for _, field := range jsval.Keys(value) {
					if field != "parts" {
						c.content.Set(field, jsval.Get(value, field))
					}
				}
				for _, part := range list(jsval.Get(value, "parts")) {
					c.add(part)
				}
			case !jsval.Nullish(value):
				c.candidate.Set(key, value)
			}
		}
		if _, ok := jsval.Get(piece, "finishReason").(string); ok {
			a.finished = true
		}
	}
}

func (a *geminiAssembler) body(ended bool) (any, string) {
	switch {
	case a.bad:
		return nil, skipNotJSON
	case !ended || !a.finished:
		return nil, skipStreamCut
	}
	candidates := []any{}
	for _, index := range sortedKeys(a.candidates) {
		c := a.candidates[index]
		c.flush()
		if c.content != nil {
			// "parts" first, as Gemini writes it.
			content := jsval.NewObject()
			content.Set("parts", append([]any{}, c.parts...))
			for _, key := range c.content.Keys() {
				content.Set(key, jsval.Get(c.content, key))
			}
			c.candidate.Set("content", content)
		}
		candidates = append(candidates, c.candidate)
	}
	out := jsval.NewObject()
	out.Set("candidates", candidates)
	for _, key := range a.top.Keys() {
		out.Set(key, jsval.Get(a.top, key))
	}
	return out, ""
}

// feedArray reads a Gemini JSON-array stream, buffered whole, into its
// assembler. It returns the reason the body cannot be read, or "".
func feedArray(a assembler, data []byte) string {
	gemini, ok := a.(*geminiAssembler)
	if !ok {
		return skipNotJSON
	}
	value, err := parseBounded(string(data))
	if err != nil {
		// An array that stops part way is a stream that was cut.
		if bytes.HasPrefix(bytes.TrimSpace(data), []byte("[")) {
			return skipStreamCut
		}
		return skipNotJSON
	}
	responses, isList := value.([]any)
	if !isList {
		responses = []any{value} // a single response, not wrapped in an array
	}
	for _, item := range responses {
		response, ok := item.(*jsval.Object)
		if !ok {
			return skipNotJSON
		}
		gemini.response(response)
	}
	return ""
}

// parseBounded is jsval.Parse behind encoding/json's validity check, which
// refuses nesting deeper than 10000. jsval.Parse recurses without a limit,
// so a stream event of nothing but "[[[[..." would otherwise overflow the
// stack and take the recorder down.
func parseBounded(text string) (any, error) {
	if !json.Valid([]byte(text)) {
		return nil, errors.New("not valid JSON")
	}
	return jsval.Parse(text)
}
