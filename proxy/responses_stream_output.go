package proxy

import "encoding/json"

type responsesStreamOutput struct {
	send   func(string, interface{})
	items  []ResponseOutputItem
	active int
}

func newResponsesStreamOutput(send func(string, interface{})) *responsesStreamOutput {
	return &responsesStreamOutput{send: send, items: []ResponseOutputItem{}, active: -1}
}

func (output *responsesStreamOutput) text(text string, thinking bool) {
	if text == "" {
		return
	}
	kind, prefix := "message", "msg"
	if thinking {
		kind, prefix = "reasoning", "rs"
	}
	if output.active < 0 || output.items[output.active].Type != kind {
		output.finish("completed")
		item := ResponseOutputItem{ID: generateOutputItemID(prefix), Type: kind, Status: "in_progress"}
		if thinking {
			item.Summary = []ResponseContentPart{{Type: "summary_text", Text: ""}}
		} else {
			item.Role = "assistant"
			item.Content = []ResponseContentPart{{Type: "output_text", Text: ""}}
		}
		output.active = len(output.items)
		output.items = append(output.items, item)
		output.send("response.output_item.added", map[string]interface{}{"type": "response.output_item.added", "output_index": output.active, "item": item})
		partEvent, indexKey, part := "response.content_part.added", "content_index", ResponseContentPart{Type: "output_text"}
		if thinking {
			partEvent, indexKey, part = "response.reasoning_summary_part.added", "summary_index", ResponseContentPart{Type: "summary_text"}
		}
		output.send(partEvent, map[string]interface{}{"type": partEvent, "item_id": item.ID, "output_index": output.active, indexKey: 0, "part": part})
	}
	item := &output.items[output.active]
	event, indexKey := "response.output_text.delta", "content_index"
	if thinking {
		item.Summary[0].Text += text
		event, indexKey = "response.reasoning_summary_text.delta", "summary_index"
	} else {
		item.Content[0].Text += text
	}
	output.send(event, map[string]interface{}{"type": event, "item_id": item.ID, "output_index": output.active, indexKey: 0, "delta": text})
}

func (output *responsesStreamOutput) finish(status string) {
	if output.active < 0 {
		return
	}
	item := &output.items[output.active]
	textEvent, partEvent, indexKey := "response.output_text.done", "response.content_part.done", "content_index"
	var part ResponseContentPart
	if item.Type == "reasoning" {
		textEvent, partEvent, indexKey = "response.reasoning_summary_text.done", "response.reasoning_summary_part.done", "summary_index"
		part = item.Summary[0]
	} else {
		part = item.Content[0]
	}
	output.send(textEvent, map[string]interface{}{"type": textEvent, "item_id": item.ID, "output_index": output.active, indexKey: 0, "text": part.Text})
	output.send(partEvent, map[string]interface{}{"type": partEvent, "item_id": item.ID, "output_index": output.active, indexKey: 0, "part": part})
	item.Status = status
	output.send("response.output_item.done", map[string]interface{}{"type": "response.output_item.done", "output_index": output.active, "item": *item})
	output.active = -1
}

func (output *responsesStreamOutput) tool(tool KiroToolUse) {
	output.finish("completed")
	args, _ := json.Marshal(tool.Input)
	item := ResponseOutputItem{ID: generateOutputItemID("fc"), Type: "function_call", Status: "in_progress", CallID: tool.ToolUseID, Name: tool.Name}
	index := len(output.items)
	output.send("response.output_item.added", map[string]interface{}{"type": "response.output_item.added", "output_index": index, "item": item})
	output.send("response.function_call_arguments.delta", map[string]interface{}{"type": "response.function_call_arguments.delta", "item_id": item.ID, "output_index": index, "delta": string(args)})
	item.Arguments, item.Status = string(args), "completed"
	output.send("response.function_call_arguments.done", map[string]interface{}{"type": "response.function_call_arguments.done", "item_id": item.ID, "output_index": index, "arguments": item.Arguments})
	output.items = append(output.items, item)
	output.send("response.output_item.done", map[string]interface{}{"type": "response.output_item.done", "output_index": index, "item": item})
}

func addResponsesReasoning(response *ResponsesObject, reasoning string) {
	if reasoning == "" {
		return
	}
	item := ResponseOutputItem{ID: generateOutputItemID("rs"), Type: "reasoning", Status: "completed", Summary: []ResponseContentPart{{Type: "summary_text", Text: reasoning}}}
	response.Output = append([]ResponseOutputItem{item}, response.Output...)
}
