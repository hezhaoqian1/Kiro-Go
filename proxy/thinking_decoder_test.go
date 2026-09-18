package proxy

import (
	"bytes"
	"errors"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestThinkingDecoderEveryFragmentBoundary(t *testing.T) {
	for _, scenario := range []struct {
		name, wire, answer, reasoning string
		incomplete                    bool
	}{
		{"thinking", "<thinking>推理😀</thinking>答案", "答案", "推理😀", false},
		{"think", "<think>short summary</think>answer", "answer", "short summary", false},
		{"reasoning", "\n<reasoning>summary</reasoning>answer", "answer", "summary", false},
		{"thought", "<thought>summary</thought>answer", "answer", "summary", false},
		{"quoted opening", "`<thinking>` is an XML tag", "`<thinking>` is an XML tag", "", false},
		{"fenced code", "```xml\n<thinking>literal</thinking>\n```", "```xml\n<thinking>literal</thinking>\n```", "", false},
		{"quoted closing", "<thinking>use `</thinking>` literally</thinking>answer", "answer", "use `</thinking>` literally", false},
		{"ordinary XML", "<message>hello</message>", "<message>hello</message>", "", false},
		{"inline example", "Example: <thinking>literal</thinking>", "Example: <thinking>literal</thinking>", "", false},
		{"control prelude", "<thinking_mode>adaptive</thinking_mode>\n<thinking_effort>medium</thinking_effort>\n<think>summary</think>answer", "answer", "summary", false},
		{"unclosed", "<thinking>unfinished", "", "unfinished", true},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			for boundary := 0; boundary <= len(scenario.wire); boundary++ {
				if !utf8.ValidString(scenario.wire[:boundary]) {
					continue
				}
				var answer, reasoning strings.Builder
				decoder := &thinkingDecoder{emit: func(text string, thinking bool) {
					if thinking {
						reasoning.WriteString(text)
					} else {
						answer.WriteString(text)
					}
				}}
				for _, fragment := range []string{scenario.wire[:boundary], scenario.wire[boundary:]} {
					if err := decoder.Feed(fragment, false, false); err != nil {
						t.Fatalf("boundary=%d: %v", boundary, err)
					}
				}
				err := decoder.Feed("", true, false)
				if errors.Is(err, errIncompleteThinking) != scenario.incomplete {
					t.Fatalf("boundary=%d error=%v", boundary, err)
				}
				if answer.String() != scenario.answer || reasoning.String() != scenario.reasoning {
					t.Fatalf("boundary=%d answer=%q reasoning=%q", boundary, answer.String(), reasoning.String())
				}
			}
		})
	}
}

func TestThinkingDecoderLimitAndControlBounds(t *testing.T) {
	decoder := &thinkingDecoder{emit: func(string, bool) {}}
	if err := decoder.Feed("<thinking>unfinished", true, true); err != nil {
		t.Fatal(err)
	}
	decoder = &thinkingDecoder{emit: func(string, bool) {}}
	if err := decoder.Feed("<thinking_mode>"+strings.Repeat("x", 300), false, false); err == nil {
		t.Fatal("oversized control prelude accepted")
	}
}

func TestNormalizedThinkingDoesNotDuplicateSources(t *testing.T) {
	for _, nativeFirst := range []bool{true, false} {
		native := awsEventStreamFrame(t, "reasoningContentEvent", map[string]interface{}{"text": "native"})
		tagged := awsEventStreamFrame(t, "assistantResponseEvent", map[string]interface{}{"content": "<think>tagged</think>"})
		var wire bytes.Buffer
		if nativeFirst {
			wire.Write(native)
			wire.Write(tagged)
		} else {
			wire.Write(tagged)
			wire.Write(native)
		}
		wire.Write(awsEventStreamFrame(t, "assistantResponseEvent", map[string]interface{}{"content": "answer"}))
		wire.Write(awsEventStreamFrame(t, "metadataEvent", map[string]interface{}{"stopReason": "end_turn"}))
		var reasoning, answer strings.Builder
		_, err := parseNormalizedEventStream(&wire, &KiroStreamCallback{OnText: func(text string, thinking bool) {
			if thinking {
				reasoning.WriteString(text)
			} else {
				answer.WriteString(text)
			}
		}}, true)
		if err != nil {
			t.Fatal(err)
		}
		if answer.String() != "answer" {
			t.Fatalf("answer=%q", answer.String())
		}
		want := "tagged"
		if nativeFirst {
			want = "native"
		}
		if reasoning.String() != want {
			t.Fatalf("first reasoning source must win: got %q want %q", reasoning.String(), want)
		}
	}
}
