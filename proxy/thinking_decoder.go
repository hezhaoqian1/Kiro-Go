package proxy

import (
	"errors"
	"fmt"
	"io"
	"strings"
	"unicode/utf8"
)

var errIncompleteThinking = errors.New("upstream ended inside a thinking block")
var thinkingTags = []string{"thinking", "think", "reasoning", "thought"}
var thinkingControlTags = []string{"thinking_mode", "max_thinking_length", "thinking_effort"}

type thinkingStreamSource int

const (
	thinkingSourceUnknown thinkingStreamSource = iota
	thinkingSourceReasoningEvent
	thinkingSourceTagBlock
)

func allowReasoningSource(source *thinkingStreamSource) bool {
	if *source == thinkingSourceTagBlock {
		return false
	}
	*source = thinkingSourceReasoningEvent
	return true
}

func allowTagSource(source *thinkingStreamSource) bool {
	if *source == thinkingSourceReasoningEvent {
		return false
	}
	if *source == thinkingSourceUnknown {
		*source = thinkingSourceTagBlock
	}
	return *source == thinkingSourceTagBlock
}

type thinkingDecoder struct {
	buffer        string
	closing       string
	answerStarted bool
	previous      byte
	emit          func(string, bool)
	onTag         func()
}

func (decoder *thinkingDecoder) output(text string, thinking bool) {
	if text == "" {
		return
	}
	decoder.previous = text[len(text)-1]
	decoder.emit(text, thinking)
}

func quotedThinkingTag(previous byte, suffix string) bool {
	quoted := func(value byte) bool { return value == '`' || value == '\'' || value == '"' || value == '\\' }
	return quoted(previous) || len(suffix) > 0 && quoted(suffix[0])
}

func (decoder *thinkingDecoder) Feed(text string, final bool, allowIncomplete bool) error {
	decoder.buffer += text
	for {
		if decoder.closing != "" {
			search := 0
			found := -1
			for search < len(decoder.buffer) {
				position := strings.Index(decoder.buffer[search:], decoder.closing)
				if position < 0 {
					break
				}
				position += search
				end := position + len(decoder.closing)
				if end == len(decoder.buffer) && !final {
					break
				}
				previous := decoder.previous
				if position > 0 {
					previous = decoder.buffer[position-1]
				}
				if !quotedThinkingTag(previous, decoder.buffer[end:]) {
					found = position
					break
				}
				search = end
			}
			if found >= 0 {
				decoder.output(decoder.buffer[:found], true)
				decoder.buffer = decoder.buffer[found+len(decoder.closing):]
				decoder.closing = ""
				decoder.previous = 0
				continue
			}
			if final {
				decoder.output(decoder.buffer, true)
				decoder.buffer = ""
				if !allowIncomplete {
					return errIncompleteThinking
				}
				decoder.closing = ""
				return nil
			}
			safe := len(decoder.buffer) - len(decoder.closing) - 1
			for safe > 0 && !utf8.RuneStart(decoder.buffer[safe]) {
				safe--
			}
			if safe > 0 {
				decoder.output(decoder.buffer[:safe], true)
				decoder.buffer = decoder.buffer[safe:]
			}
			return nil
		}
		if decoder.answerStarted {
			decoder.output(decoder.buffer, false)
			decoder.buffer = ""
			return nil
		}
		trimmed := strings.TrimLeft(decoder.buffer, " \r\n\t")
		if trimmed == "" {
			if final {
				decoder.output(decoder.buffer, false)
				decoder.buffer = ""
			}
			return nil
		}
		waiting := false
		matched := false
		for _, tag := range thinkingTags {
			opening := "<" + tag + ">"
			if strings.HasPrefix(opening, trimmed) && trimmed != opening {
				waiting = true
			}
			if !strings.HasPrefix(trimmed, opening) {
				continue
			}
			suffix := trimmed[len(opening):]
			if suffix == "" && !final {
				waiting = true
				continue
			}
			if quotedThinkingTag(0, suffix) {
				continue
			}
			decoder.buffer = suffix
			decoder.closing = "</" + tag + ">"
			if decoder.onTag != nil {
				decoder.onTag()
			}
			decoder.previous = 0
			matched = true
			break
		}
		if matched {
			continue
		}
		for _, tag := range thinkingControlTags {
			opening, closing := "<"+tag+">", "</"+tag+">"
			if strings.HasPrefix(opening, trimmed) && trimmed != opening {
				waiting = true
			}
			if !strings.HasPrefix(trimmed, opening) {
				continue
			}
			end := strings.Index(trimmed, closing)
			if end < 0 {
				if len(trimmed) > 256 || final {
					return fmt.Errorf("invalid upstream thinking control prelude")
				}
				waiting = true
				continue
			}
			value := strings.TrimSpace(trimmed[len(opening):end])
			valid := value == "enabled" || value == "adaptive" || value == "low" || value == "medium" || value == "high" || value == "xhigh" || value == "max"
			if tag == "max_thinking_length" {
				valid = value != "" && strings.Trim(value, "0123456789") == ""
			}
			if !valid {
				continue
			}
			decoder.buffer = trimmed[end+len(closing):]
			matched = true
			break
		}
		if matched {
			continue
		}
		if waiting && !final {
			return nil
		}
		if waiting && final {
			return errIncompleteThinking
		}
		decoder.answerStarted = true
	}
}

func parseNormalizedEventStream(body io.Reader, callback *KiroStreamCallback, enabled bool) (bool, error) {
	if !enabled {
		return parseEventStreamTracked(body, callback)
	}
	if callback == nil {
		callback = &KiroStreamCallback{}
	}
	wrapped := *callback
	var source thinkingStreamSource
	var parseErr error
	var stopReason string
	var inputTokens, outputTokens int
	decoder := &thinkingDecoder{onTag: func() { allowTagSource(&source) }, emit: func(text string, thinking bool) {
		if thinking && !allowTagSource(&source) {
			return
		}
		if callback.OnText != nil {
			callback.OnText(text, thinking)
		}
	}}
	wrapped.OnText = func(text string, thinking bool) {
		if parseErr != nil {
			return
		}
		if thinking {
			if allowReasoningSource(&source) && callback.OnText != nil {
				callback.OnText(text, true)
			}
			return
		}
		parseErr = decoder.Feed(text, false, false)
	}
	wrapped.OnToolUse = func(tool KiroToolUse) {
		if parseErr == nil {
			parseErr = decoder.Feed("", true, false)
		}
		if parseErr == nil && callback.OnToolUse != nil {
			callback.OnToolUse(tool)
		}
	}
	wrapped.OnStopReason = func(reason string) {
		stopReason = reason
		if callback.OnStopReason != nil {
			callback.OnStopReason(reason)
		}
	}
	wrapped.OnComplete = func(input, output int) { inputTokens, outputTokens = input, output }
	emitted, err := parseEventStreamTracked(body, &wrapped)
	if err != nil {
		return emitted, err
	}
	if parseErr == nil {
		parseErr = decoder.Feed("", true, stopReason == "max_tokens" || stopReason == "model_context_window_exceeded")
	}
	if parseErr != nil {
		return emitted, parseErr
	}
	if callback.OnComplete != nil {
		callback.OnComplete(inputTokens, outputTokens)
	}
	return emitted, nil
}
